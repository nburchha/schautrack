package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"schautrack/internal/config"
)

var barcodeRe = regexp.MustCompile(`^\d{8,13}$`)

// hasValidCheckDigit verifies the GS1 check digit for EAN-8 (8), UPC-A (12),
// and EAN-13 (13). Other lengths skip validation (no standard checksum to
// verify). The standard GS1 algorithm: from the right, multiply each non-check
// digit alternately by 3 and 1, sum, and the check digit is (10 - sum%10) % 10.
func hasValidCheckDigit(code string) bool {
	n := len(code)
	if n != 8 && n != 12 && n != 13 {
		return true
	}
	var sum int
	for i := 0; i < n-1; i++ {
		d := int(code[i] - '0')
		if (n-1-i)%2 == 1 {
			sum += d * 3
		} else {
			sum += d
		}
	}
	expected := (10 - sum%10) % 10
	return int(code[n-1]-'0') == expected
}

const openFoodFactsURL = "https://world.openfoodfacts.org"

// offClient is shared so lookups reuse connections to Open Food Facts.
var offClient = &http.Client{Timeout: 10 * time.Second}

// upsertTimeout bounds the catalog write that follows a lookup. It runs
// detached from the request, so it needs a limit of its own.
const upsertTimeout = 5 * time.Second

const offUnreachableNote = "Open Food Facts is unreachable; showing the stored values."

// Barcode handles POST /api/barcode/{code} (and, injected, GET /api/v1/barcode/{code}).
// The handler does not look at the method: a lookup writes to the shared catalog,
// which is why the session route is a POST behind CSRF and the v1 route needs
// foods:write.
//
// A barcode names exactly one product, so there is nothing to choose between:
// the lookup answers with that product or with "not found". A product with a
// calorie value is also stored in the catalog (source "off", source_code = the
// EAN) and returned as `food`, so the caller can use it as an entry component
// by id. A rescan refreshes the stored values; components captured earlier
// keep theirs (they are snapshots). When Open Food Facts cannot be reached, a
// product stored by an earlier scan is answered from the catalog (`cached`).
func Barcode(cfg *config.Config, db querier) http.HandlerFunc {
	return barcodeHandler(cfg, db, openFoodFactsURL)
}

func barcodeHandler(cfg *config.Config, db querier, baseURL string) http.HandlerFunc {
	userAgent := fmt.Sprintf("Schautrack/%s (%s)", cfg.BuildVersion, orDefault(cfg.SupportEmail, "noreply@schautrack.app"))

	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if !barcodeRe.MatchString(code) {
			ErrorJSON(w, http.StatusBadRequest, "Invalid barcode format.")
			return
		}
		if !hasValidCheckDigit(code) {
			JSON(w, http.StatusOK, map[string]any{
				"ok":    false,
				"error": "Barcode check digit invalid — likely misread. Please rescan or enter manually.",
				"code":  "CHECK_DIGIT",
			})
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), "GET",
			fmt.Sprintf("%s/api/v2/product/%s?fields=product_name,brands,nutriments,serving_quantity,serving_size", baseURL, code), nil)
		if err != nil {
			ErrorJSON(w, http.StatusInternalServerError, "Failed to create request.")
			return
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := offClient.Do(req)
		if err != nil {
			if !writeCachedProduct(w, r, db, code) {
				ErrorJSON(w, http.StatusGatewayTimeout, "Food database request timed out.")
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			if !writeCachedProduct(w, r, db, code) {
				ErrorJSON(w, http.StatusBadGateway, "Failed to reach food database.")
			}
			return
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var data struct {
			Status  int `json:"status"`
			Product struct {
				Name            string         `json:"product_name"`
				Brands          string         `json:"brands"`
				Nutriments      map[string]any `json:"nutriments"`
				ServingSize     string         `json:"serving_size"`
				ServingQuantity any            `json:"serving_quantity"`
			} `json:"product"`
		}
		if err := json.Unmarshal(body, &data); err != nil || data.Status != 1 {
			JSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Product not found."})
			return
		}

		p := data.Product
		name := productName(code, p.Name, p.Brands)
		kcal := offNumber(p.Nutriments, "energy-kcal_100g", maxCatalogKcal)
		if kcal == nil {
			note := "No calorie data available for this product."
			if raw, present := offRaw(p.Nutriments, "energy-kcal_100g"); present {
				note = fmt.Sprintf("Open Food Facts reports an implausible calorie value (%s kcal per 100 g); it was not stored.",
					strconv.FormatFloat(raw, 'f', -1, 64))
			}
			JSON(w, http.StatusOK, map[string]any{
				"ok": true, "name": name, "caloriesPer100g": nil,
				"macrosPer100g": map[string]any{}, "servingSize": nil,
				"servingQuantity": nil, "note": note,
			})
			return
		}

		macroFields := map[string]string{
			"protein": "proteins_100g", "carbs": "carbohydrates_100g",
			"fat": "fat_100g", "fiber": "fiber_100g", "sugar": "sugars_100g",
		}
		macros := map[string]any{}
		stored := map[string]*float64{}
		for key, field := range macroFields {
			if v := offNumber(p.Nutriments, field, maxCatalogMacro); v != nil {
				macros[key] = math.Round(*v*10) / 10
				stored[key] = v
			}
		}

		var servingQuantity *float64
		if sq, ok := p.ServingQuantity.(float64); ok && sq > 0 {
			servingQuantity = &sq
		}

		var servingSize *string
		if p.ServingSize != "" {
			servingSize = &p.ServingSize
		}

		food, err := upsertOffFood(r.Context(), db, code, name, kcal, stored)
		if err != nil {
			slog.Error("storing scanned product in the catalog", "error", err)
			ErrorJSON(w, http.StatusInternalServerError, "Failed to save the product.")
			return
		}

		JSON(w, http.StatusOK, map[string]any{
			"ok": true, "name": name, "caloriesPer100g": int(math.Round(*kcal)),
			"macrosPer100g": macros, "servingSize": servingSize,
			"servingQuantity": servingQuantity, "food": food,
		})
	}
}

// offRaw reads one per-100 g nutriment as OFF reports it, range unchecked.
func offRaw(nutriments map[string]any, key string) (float64, bool) {
	v, ok := nutriments[key].(float64)
	return v, ok && !math.IsNaN(v)
}

// offNumber reads one per-100 g nutriment. Open Food Facts is crowdsourced, so
// a value outside what the catalog accepts (a typo'd 5000 kcal) is treated as
// unknown rather than stored: NULL means "no data", and the insert would trip
// the foods_*_range checks otherwise.
func offNumber(nutriments map[string]any, key string, max float64) *float64 {
	v, ok := offRaw(nutriments, key)
	if !ok || v < 0 || v > max {
		return nil
	}
	r := math.Round(v*100) / 100
	return &r
}

// productName is the catalog name of a scanned product: the product name, with
// the first brand appended unless the name already carries it, so two skyrs
// from different brands tell apart in the search. A product nobody named gets
// its EAN, which keeps it findable.
func productName(code, name, brands string) string {
	// Postgres TEXT cannot hold NUL, and Open Food Facts text is user-entered.
	clean := func(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\x00", "")) }
	name = clean(name)
	brand := clean(strings.Split(brands, ",")[0])
	switch {
	case name == "":
		name = "Unknown product " + code
	case brand != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(brand)):
		name += " (" + brand + ")"
	}
	return truncateUTF8(name, maxFoodName)
}

// unknownProductName is the name of a product Open Food Facts has no name for.
func unknownProductName(code string) string { return "Unknown product " + code }

// upsertOffFood stores a scanned product as a global catalog food keyed on its
// EAN, and returns the stored row.
//
// A rescan must never make the catalog worse, so the nutrients keep their stored
// value when the new response lacks them (OFF's data is patchy and changes
// between calls), and the name keeps the stored one when OFF no longer has a name.
//
// The write is detached from the request's cancellation: the OFF call is the
// slow, expensive part, and a client that disconnects right after it should not
// throw the result away.
func upsertOffFood(parent context.Context, db querier, code, name string, kcal *float64, macros map[string]*float64) (*v1Food, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), upsertTimeout)
	defer cancel()
	return scanFood(db.QueryRow(ctx, `
		INSERT INTO foods (source, source_code, name, kcal_100g, protein_100g, carbs_100g, fat_100g, fiber_100g, sugar_100g)
		VALUES ('off', $1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, source_code) WHERE user_id IS NULL DO UPDATE SET
			name = CASE WHEN EXCLUDED.name = $9::text THEN foods.name ELSE EXCLUDED.name END,
			kcal_100g = COALESCE(EXCLUDED.kcal_100g, foods.kcal_100g),
			protein_100g = COALESCE(EXCLUDED.protein_100g, foods.protein_100g),
			carbs_100g = COALESCE(EXCLUDED.carbs_100g, foods.carbs_100g),
			fat_100g = COALESCE(EXCLUDED.fat_100g, foods.fat_100g),
			fiber_100g = COALESCE(EXCLUDED.fiber_100g, foods.fiber_100g),
			sugar_100g = COALESCE(EXCLUDED.sugar_100g, foods.sugar_100g),
			updated_at = NOW()
		RETURNING `+foodSelect,
		code, name, kcal, macros["protein"], macros["carbs"], macros["fat"], macros["fiber"], macros["sugar"],
		unknownProductName(code)))
}

// writeCachedProduct answers a lookup from the catalog row an earlier scan
// stored, for when Open Food Facts cannot be reached. It reports whether it
// answered; without a stored product with calories the caller keeps its error.
func writeCachedProduct(w http.ResponseWriter, r *http.Request, db querier, code string) bool {
	food, err := scanFood(db.QueryRow(r.Context(), `
		SELECT `+foodSelect+`
		FROM foods WHERE source = 'off' AND source_code = $1 AND user_id IS NULL`, code))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("reading the stored product for an unreachable Open Food Facts", "error", err)
		}
		return false
	}
	if food.Calories == nil {
		return false
	}

	macros := map[string]any{}
	for key, v := range map[string]*float64{
		"protein": food.Macros.ProteinG, "carbs": food.Macros.CarbsG, "fat": food.Macros.FatG,
		"fiber": food.Macros.FiberG, "sugar": food.Macros.SugarG,
	} {
		if v != nil {
			macros[key] = math.Round(*v*10) / 10
		}
	}
	JSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": food.Name, "caloriesPer100g": int(math.Round(*food.Calories)),
		"macrosPer100g": macros, "servingSize": nil, "servingQuantity": nil,
		"food": food, "cached": true, "note": offUnreachableNote,
	})
	return true
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
