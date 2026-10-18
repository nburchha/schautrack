package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"schautrack/internal/config"
)

// A scanned product lands in the catalog, is found by the catalog search, and
// a rescan refreshes the stored row instead of adding a second one.
func TestBarcode_StoresProductInCatalog(t *testing.T) {
	e := newV1Env(t)
	pool := e.Pool
	cfg := &config.Config{BuildVersion: "test"}

	// The EAN is the catalog key and the shared test database outlives a run,
	// so clear its row before and after.
	const code = "4311501062449"
	clean := func() { pool.Exec(t.Context(), `DELETE FROM foods WHERE source = 'off' AND source_code = $1`, code) }
	clean()
	t.Cleanup(clean)

	nutriments := map[string]any{
		"energy-kcal_100g": 63.0, "proteins_100g": 11.0, "carbohydrates_100g": 4.0,
		"fat_100g": 0.2, "fiber_100g": 7777.0, // implausible: stored as unknown, not rejected
	}
	product := map[string]any{"product_name": "Skyr Natur", "brands": "Arla", "nutriments": nutriments}
	base := offServer(t, 200, map[string]any{"status": 1, "product": product})

	scan := func() map[string]any {
		h := barcodeHandler(cfg, pool, base)
		r := withChiURLParam(httptest.NewRequest("GET", "/api/barcode/"+code, nil), "code", code)
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	first := scan()
	food, _ := first["food"].(map[string]any)
	if food == nil {
		t.Fatalf("response has no food: %v", first)
	}
	if food["source"] != "off" || food["source_code"] != code || food["name"] != "Skyr Natur (Arla)" {
		t.Errorf("food = %v, want source off, source_code %s, name %q", food, code, "Skyr Natur (Arla)")
	}
	if food["calories_per_100g"] != 63.0 {
		t.Errorf("calories_per_100g = %v, want 63", food["calories_per_100g"])
	}
	macros, _ := food["macros_per_100g"].(map[string]any)
	if macros["protein_g"] != 11.0 || macros["fiber_g"] != nil {
		t.Errorf("macros = %v, want protein 11 and unknown fiber", macros)
	}
	// The legacy fields stay for existing clients.
	if first["caloriesPer100g"] != 63.0 {
		t.Errorf("caloriesPer100g = %v, want 63", first["caloriesPer100g"])
	}

	// OFF corrects the product; the rescan updates the same catalog row.
	nutriments["energy-kcal_100g"] = 65.0
	second := scan()
	food2, _ := second["food"].(map[string]any)
	if food2["id"] != food["id"] {
		t.Errorf("rescan id = %v, want the original %v", food2["id"], food["id"])
	}
	if food2["calories_per_100g"] != 65.0 {
		t.Errorf("rescan calories = %v, want 65", food2["calories_per_100g"])
	}
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM foods WHERE source = 'off' AND source_code = $1`, code).Scan(&n); err != nil || n != 1 {
		t.Errorf("catalog rows for the EAN = %d (err %v), want 1", n, err)
	}

	// The scanned product is searchable like any catalog food.
	found, err := searchFoods(t.Context(), pool, e.UserID, "Skyr Natur (Arla)", 10)
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, f := range found {
		if f.SourceCode != nil && *f.SourceCode == code {
			hit = true
		}
	}
	if !hit {
		t.Errorf("search did not find the scanned product among %d hits", len(found))
	}
}
