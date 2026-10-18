package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"schautrack/internal/config"
)

// scanBarcode runs the lookup handler against a stand-in Open Food Facts and
// returns the status and decoded body.
func scanBarcode(t *testing.T, db querier, base, code string) (int, map[string]any) {
	t.Helper()
	h := barcodeHandler(&config.Config{BuildVersion: "test"}, db, base)
	r := withChiURLParam(httptest.NewRequest("POST", "/api/barcode/"+code, nil), "code", code)
	w := httptest.NewRecorder()
	h(w, r)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, w.Body.String())
	}
	return w.Code, resp
}

// cleanCatalog removes the global OFF row for an EAN now and after the test:
// the EAN is the catalog key and the shared test database outlives a run.
func cleanCatalog(t *testing.T, pool querier, code string) {
	t.Helper()
	clean := func() {
		pool.Exec(context.Background(), `DELETE FROM foods WHERE source = 'off' AND source_code = $1`, code)
	}
	clean()
	t.Cleanup(clean)
}

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

// A degraded OFF response (fewer nutrients, no name) must not erase what an
// earlier scan stored; whatever OFF does report still wins.
func TestBarcode_RescanKeepsKnownValues(t *testing.T) {
	e := newV1Env(t)
	const code = "4006381333931"
	cleanCatalog(t, e.Pool, code)

	scan := func(product map[string]any) map[string]any {
		t.Helper()
		base := offServer(t, 200, map[string]any{"status": 1, "product": product})
		status, resp := scanBarcode(t, e.Pool, base, code)
		if status != http.StatusOK {
			t.Fatalf("status = %d (%v)", status, resp)
		}
		food, _ := resp["food"].(map[string]any)
		if food == nil {
			t.Fatalf("response has no food: %v", resp)
		}
		return food
	}

	scan(map[string]any{"product_name": "Skyr Natur", "brands": "Arla", "nutriments": map[string]any{
		"energy-kcal_100g": 63.0, "proteins_100g": 11.0, "carbohydrates_100g": 4.0,
		"fat_100g": 0.2, "fiber_100g": 1.5, "sugars_100g": 3.9,
	}})

	// Nameless, kcal only: everything else stays as stored.
	food := scan(map[string]any{"nutriments": map[string]any{"energy-kcal_100g": 64.0}})
	if food["name"] != "Skyr Natur (Arla)" {
		t.Errorf("name = %v, want the stored name kept over %q", food["name"], "Unknown product "+code)
	}
	if food["calories_per_100g"] != 64.0 {
		t.Errorf("calories = %v, want the new 64", food["calories_per_100g"])
	}
	macros, _ := food["macros_per_100g"].(map[string]any)
	want := map[string]float64{"protein_g": 11, "carbs_g": 4, "fat_g": 0.2, "fiber_g": 1.5, "sugar_g": 3.9}
	for k, v := range want {
		if macros[k] != v {
			t.Errorf("%s = %v, want the stored %v", k, macros[k], v)
		}
	}

	// A real new name and a changed nutrient replace the stored ones; the rest still stays.
	food = scan(map[string]any{"product_name": "Skyr Vanille", "nutriments": map[string]any{
		"energy-kcal_100g": 70.0, "proteins_100g": 10.0,
	}})
	macros, _ = food["macros_per_100g"].(map[string]any)
	if food["name"] != "Skyr Vanille" || macros["protein_g"] != 10.0 || macros["fat_g"] != 0.2 {
		t.Errorf("food = %v, want name and protein replaced and fat kept", food)
	}
}

// With Open Food Facts down, a product an earlier scan stored is still answered,
// marked as such; one that was never stored keeps the upstream error.
func TestBarcode_FallsBackToStoredProduct(t *testing.T) {
	e := newV1Env(t)
	const code = "96385074"
	cleanCatalog(t, e.Pool, code)

	down := map[string]string{
		"503":         offServer(t, 503, map[string]any{}),
		"unreachable": deadOffServer(t),
	}

	for name, base := range down {
		status, _ := scanBarcode(t, e.Pool, base, code)
		wantStatus := http.StatusBadGateway
		if name == "unreachable" {
			wantStatus = http.StatusGatewayTimeout
		}
		if status != wantStatus {
			t.Errorf("%s without a stored product: status = %d, want %d", name, status, wantStatus)
		}
	}

	live := offServer(t, 200, map[string]any{"status": 1, "product": map[string]any{
		"product_name": "Mini Cola", "nutriments": map[string]any{
			"energy-kcal_100g": 42.0, "carbohydrates_100g": 10.6, "sugars_100g": 10.6,
		},
		"serving_quantity": 250.0, "serving_size": "250 ml",
	}})
	_, fresh := scanBarcode(t, e.Pool, live, code)
	if fresh["cached"] != nil {
		t.Fatalf("a live lookup must not be marked cached: %v", fresh)
	}
	stored, _ := fresh["food"].(map[string]any)

	for name, base := range down {
		status, resp := scanBarcode(t, e.Pool, base, code)
		if status != http.StatusOK || resp["ok"] != true {
			t.Errorf("%s with a stored product: got %d %v, want 200 ok", name, status, resp)
			continue
		}
		if resp["cached"] != true || resp["note"] != offUnreachableNote {
			t.Errorf("%s: cached = %v, note = %v, want cached with the unreachable note", name, resp["cached"], resp["note"])
		}
		food, _ := resp["food"].(map[string]any)
		if food["id"] != stored["id"] || resp["name"] != "Mini Cola" || resp["caloriesPer100g"] != 42.0 {
			t.Errorf("%s: got %v, want the stored Mini Cola at 42 kcal", name, resp)
		}
		macros, _ := resp["macrosPer100g"].(map[string]any)
		if macros["carbs"] != 10.6 || macros["sugar"] != 10.6 || macros["protein"] != nil {
			t.Errorf("%s: macrosPer100g = %v, want the stored carbs and sugar only", name, macros)
		}
		if resp["servingQuantity"] != nil || resp["servingSize"] != nil {
			t.Errorf("%s: serving = %v / %v, want null (it is not stored)", name, resp["servingSize"], resp["servingQuantity"])
		}
	}

	// A stored row without calories is no answer either.
	if _, err := e.Pool.Exec(t.Context(), `UPDATE foods SET kcal_100g = NULL WHERE source = 'off' AND source_code = $1`, code); err != nil {
		t.Fatal(err)
	}
	if status, _ := scanBarcode(t, e.Pool, down["503"], code); status != http.StatusBadGateway {
		t.Errorf("stored row without kcal: status = %d, want 502", status)
	}
}

// The catalog write outlives the request: a client that disconnects after the
// OFF call must not make the result vanish.
func TestUpsertOffFood_SurvivesCancelledRequest(t *testing.T) {
	e := newV1Env(t)
	const code = "036000291452"
	cleanCatalog(t, e.Pool, code)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	kcal := 120.0
	food, err := upsertOffFood(ctx, e.Pool, code, "Cancelled Cracker", &kcal, map[string]*float64{})
	if err != nil {
		t.Fatalf("upsert with a cancelled request context: %v", err)
	}
	if food.Name != "Cancelled Cracker" {
		t.Errorf("name = %q", food.Name)
	}
	var n int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM foods WHERE source = 'off' AND source_code = $1`, code).Scan(&n); err != nil || n != 1 {
		t.Errorf("stored rows = %d (err %v), want 1", n, err)
	}
}
