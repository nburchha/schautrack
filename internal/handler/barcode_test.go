package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"schautrack/internal/config"
)

// withChiURLParam adds a chi URL parameter to the request context.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestBarcode_InvalidFormat(t *testing.T) {
	cfg := &config.Config{BuildVersion: "test", SupportEmail: "test@test.com"}
	h := Barcode(cfg, nil)

	tests := []struct {
		name string
		code string
	}{
		{"letters", "abcdefgh"},
		{"too short", "1234567"},
		{"too long", "12345678901234"},
		{"special chars", "1234-5678"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/barcode/"+tt.code, nil)
			r = withChiURLParam(r, "code", tt.code)
			w := httptest.NewRecorder()

			h(w, r)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp map[string]any
			json.Unmarshal(w.Body.Bytes(), &resp)
			if msg, _ := resp["error"].(string); msg != "Invalid barcode format." {
				t.Errorf("error = %q, want %q", msg, "Invalid barcode format.")
			}
		})
	}
}

func TestBarcode_ValidFormat8Digits(t *testing.T) {
	if !barcodeRe.MatchString("12345678") {
		t.Error("8-digit barcode should be valid")
	}
}

func TestBarcode_ValidFormat13Digits(t *testing.T) {
	if !barcodeRe.MatchString("1234567890123") {
		t.Error("13-digit barcode should be valid")
	}
}

func TestBarcode_RegexAcceptsEightToThirteenDigits(t *testing.T) {
	for _, code := range []string{"12345678", "123456789", "1234567890", "12345678901", "123456789012", "1234567890123"} {
		if !barcodeRe.MatchString(code) {
			t.Errorf("barcodeRe should accept %q", code)
		}
	}
	for _, code := range []string{"1234567", "12345678901234", "abcdefgh", "1234-567", ""} {
		if barcodeRe.MatchString(code) {
			t.Errorf("barcodeRe should reject %q", code)
		}
	}
}

// offServer stands in for Open Food Facts. The handler under test is pointed at
// it through barcodeHandler's base URL.
func offServer(t *testing.T, status int, body any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func lookup(t *testing.T, base, code string) *httptest.ResponseRecorder {
	t.Helper()
	cfg := &config.Config{BuildVersion: "test", SupportEmail: "test@test.com"}
	// The cases that use a nil database never get as far as storing a product.
	h := barcodeHandler(cfg, nil, base)
	r := withChiURLParam(httptest.NewRequest("GET", "/api/barcode/"+code, nil), "code", code)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestBarcode_ProductNotFound(t *testing.T) {
	base := offServer(t, 200, map[string]any{"status": 0, "product": map[string]any{}})
	w := lookup(t, base, "5449000000996")

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || resp["ok"] != false || resp["error"] != "Product not found." {
		t.Errorf("got %d %v, want 200 with ok=false and a not-found error", w.Code, resp)
	}
}

func TestBarcode_UpstreamFailureIsBadGateway(t *testing.T) {
	base := offServer(t, 503, map[string]any{})
	if w := lookup(t, base, "5449000000996"); w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

// A product without a usable calorie value cannot be an entry component, so it
// is reported as such and nothing is stored (the nil database would panic).
func TestBarcode_NoCaloriesStoresNothing(t *testing.T) {
	for name, nutriments := range map[string]map[string]any{
		"absent":      {"proteins_100g": 3.0},
		"implausible": {"energy-kcal_100g": 5000.0},
		"negative":    {"energy-kcal_100g": -1.0},
	} {
		t.Run(name, func(t *testing.T) {
			base := offServer(t, 200, map[string]any{"status": 1, "product": map[string]any{
				"product_name": "Odd", "nutriments": nutriments,
			}})
			w := lookup(t, base, "5449000000996")

			var resp map[string]any
			json.Unmarshal(w.Body.Bytes(), &resp)
			if w.Code != http.StatusOK || resp["ok"] != true || resp["caloriesPer100g"] != nil || resp["food"] != nil {
				t.Errorf("got %d %v, want ok with no calories and no food", w.Code, resp)
			}
		})
	}
}

func TestProductName(t *testing.T) {
	tests := []struct {
		name, product, brands, want string
	}{
		{"plain", "Skyr", "", "Skyr"},
		{"brand appended", "Skyr Natur", "Arla, Other", "Skyr Natur (Arla)"},
		{"brand already in name", "Arla Skyr", "Arla", "Arla Skyr"},
		{"brand match ignores case", "ARLA skyr", "arla", "ARLA skyr"},
		{"unnamed falls back to the EAN", "  ", "Arla", "Unknown product 5449000000996"},
		{"trimmed", "  Skyr  ", "", "Skyr"},
		{"NUL stripped (TEXT cannot store it)", "Sk\x00yr", "Ar\x00la", "Skyr (Arla)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := productName("5449000000996", tt.product, tt.brands); got != tt.want {
				t.Errorf("productName = %q, want %q", got, tt.want)
			}
		})
	}
	long := strings.Repeat("ä", 200)
	if got := productName("5449000000996", long, ""); len(got) > maxFoodName || !utf8.ValidString(got) {
		t.Errorf("long name = %d bytes, valid=%v; want <= %d bytes of valid UTF-8", len(got), utf8.ValidString(got), maxFoodName)
	}
}

func TestOffNumber(t *testing.T) {
	n := map[string]any{"ok": 12.345, "zero": 0.0, "big": 1001.0, "neg": -0.1, "str": "12"}
	if v := offNumber(n, "ok", 1000); v == nil || *v != 12.35 {
		t.Errorf("ok = %v, want 12.35 (two decimals, as the catalog stores)", v)
	}
	if v := offNumber(n, "zero", 1000); v == nil || *v != 0 {
		t.Errorf("zero = %v, want a real 0 (0 is data, only absence is unknown)", v)
	}
	for _, key := range []string{"big", "neg", "str", "missing"} {
		if v := offNumber(n, key, 1000); v != nil {
			t.Errorf("%s = %v, want nil (unknown)", key, *v)
		}
	}
}

func TestHasValidCheckDigit(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		// Real EAN-13 codes (verified against world.openfoodfacts.org)
		{"EAN-13 Coca-Cola valid", "5449000000996", true},
		{"EAN-13 Edeka Frischkäse valid", "4311501062449", true},
		// Last-digit flips of valid EAN-13s — typical Quagga2 misreads
		{"EAN-13 last digit off by 1", "4311501062448", false},
		{"EAN-13 last digit zero", "4311501062440", false},
		// Mid-digit flip
		{"EAN-13 mid digit flipped", "4311501162449", false},
		// Real EAN-8 (Coca-Cola mini)
		{"EAN-8 valid", "96385074", true},
		{"EAN-8 invalid", "96385075", false},
		// UPC-A (12 digits)
		{"UPC-A valid", "036000291452", true},
		{"UPC-A invalid", "036000291453", false},
		// Lengths without standard checksum: skipped (return true)
		{"9 digits skipped", "123456789", true},
		{"10 digits skipped", "1234567890", true},
		{"11 digits skipped", "12345678901", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasValidCheckDigit(tt.code); got != tt.want {
				t.Errorf("hasValidCheckDigit(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestBarcode_CheckDigitMismatch(t *testing.T) {
	cfg := &config.Config{BuildVersion: "test", SupportEmail: "test@test.com"}
	h := Barcode(cfg, nil)

	r := httptest.NewRequest("GET", "/api/barcode/4311501062448", nil)
	r = withChiURLParam(r, "code", "4311501062448") // last digit flipped
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if ok, _ := resp["ok"].(bool); ok {
		t.Errorf("expected ok=false, got %v", resp)
	}
	if code, _ := resp["code"].(string); code != "CHECK_DIGIT" {
		t.Errorf("expected code=CHECK_DIGIT, got %q", code)
	}
}

func TestOrDefault(t *testing.T) {
	if got := orDefault("hello", "world"); got != "hello" {
		t.Errorf("orDefault('hello', 'world') = %q, want 'hello'", got)
	}
	if got := orDefault("", "world"); got != "world" {
		t.Errorf("orDefault('', 'world') = %q, want 'world'", got)
	}
}
