package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"schautrack/internal/service"
)

// Behaviour of entry components: the sum rule, snapshots, and the endpoints
// that change them.

func (e *v1Env) seedOwnFood(name string, kcal float64, protein *float64) int {
	e.t.Helper()
	var id int
	if err := e.Pool.QueryRow(e.Ctx, `
		INSERT INTO foods (user_id, source, name, kcal_100g, protein_100g)
		VALUES ($1, 'own', $2, $3, $4) RETURNING id`, e.UserID, name, kcal, protein).Scan(&id); err != nil {
		e.t.Fatalf("seeding food: %v", err)
	}
	return id
}

func uniq(prefix string) string { return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()) }

func TestV1EntryWithComponentsSumsThem(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	oats := e.seedOwnFood(uniq("oats"), 372, ptr(13.5))

	// 50 g oats: 186 kcal, 6.75 g protein. 200 g milk (ad hoc): 128 kcal, protein unknown.
	got := e.createEntry(token, fmt.Sprintf(`{"name":"Porridge","components":[
		{"food_id":%d,"grams":50},
		{"name":"Milk","grams":200,"calories_per_100g":64}]}`, oats))

	if got.Calories != 314 {
		t.Errorf("calories = %d, want 314", got.Calories)
	}
	if got.Macros.ProteinG == nil || *got.Macros.ProteinG != 7 {
		t.Errorf("protein = %v, want 7 (6.75 rounded; milk's unknown value skipped)", got.Macros.ProteinG)
	}
	if got.Macros.FatG != nil {
		t.Errorf("fat = %v, want null when no component knows it", got.Macros.FatG)
	}
	if len(got.Components) != 2 || got.Components[0].FoodID == nil || got.Components[1].FoodID != nil {
		t.Fatalf("components = %+v", got.Components)
	}

	// The list and single-entry reads carry the components too.
	list := e.get("/api/v1/entries?date="+got.Date, token)
	var page v1List[v1Entry]
	decodeJSON(t, list, &page)
	found := false
	for _, en := range page.Data {
		if en.ID == got.ID {
			found = len(en.Components) == 2
		}
	}
	if !found {
		t.Error("GET /entries did not return the entry's components")
	}
}

func TestV1ComponentSnapshotSurvivesCatalogChange(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	food := e.seedOwnFood(uniq("snap"), 100, nil)
	entry := e.createEntry(token, fmt.Sprintf(`{"components":[{"food_id":%d,"grams":100}]}`, food))

	if _, err := e.Pool.Exec(e.Ctx, `UPDATE foods SET kcal_100g = 999, name = 'renamed' WHERE id = $1`, food); err != nil {
		t.Fatal(err)
	}
	rec := e.patch(fmt.Sprintf("/api/v1/entries/%d/components/%d", entry.ID, entry.Components[0].ID), token, `{"grams":200}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH component: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got v1Entry
	decodeJSON(t, rec, &got)
	if got.Calories != 200 {
		t.Errorf("calories = %d, want 200 (snapshot 100/100 g, not the changed catalog value)", got.Calories)
	}
	if got.Components[0].Name == "renamed" {
		t.Error("component name followed the catalog")
	}
}

func TestV1ComponentEndpoints(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	entry := e.createEntry(token, `{"calories":900,"name":"Mensa"}`)
	base := fmt.Sprintf("/api/v1/entries/%d/components", entry.ID)

	// Adding a component to a direct entry: components win.
	rec := e.post(base, token, `{"name":"Rice","grams":150,"calories_per_100g":130}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST component: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got v1Entry
	decodeJSON(t, rec, &got)
	if got.Calories != 195 {
		t.Errorf("calories after add = %d, want 195", got.Calories)
	}

	rec = e.post(base, token, `{"name":"Beans","grams":100,"calories_per_100g":80}`)
	decodeJSON(t, rec, &got)
	if got.Calories != 275 || len(got.Components) != 2 {
		t.Errorf("after second add: calories = %d components = %d", got.Calories, len(got.Components))
	}

	// Deleting one recomputes; deleting the last keeps the totals.
	rec = e.do(call{Method: http.MethodDelete, Path: fmt.Sprintf("%s/%d", base, got.Components[1].ID), Token: token})
	decodeJSON(t, rec, &got)
	if rec.Code != http.StatusOK || got.Calories != 195 {
		t.Errorf("after delete: status = %d calories = %d, want 200/195", rec.Code, got.Calories)
	}
	rec = e.do(call{Method: http.MethodDelete, Path: fmt.Sprintf("%s/%d", base, got.Components[0].ID), Token: token})
	decodeJSON(t, rec, &got)
	if got.Calories != 195 || len(got.Components) != 0 {
		t.Errorf("after deleting the last: calories = %d components = %d, want 195/0", got.Calories, len(got.Components))
	}

	// Another account's entry is invisible.
	otherID, _ := e.seedUser("other-comp")
	other := e.tokenFor(otherID, service.ScopeEntriesWrite)
	requireProblem(t, e.post(base, other, `{"name":"x","grams":1,"calories_per_100g":1}`), http.StatusNotFound)
}

func TestV1ComponentValidation(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite)
	noKcal := uniq("nokcal")
	if _, err := e.Pool.Exec(e.Ctx, `INSERT INTO foods (user_id, source, name) VALUES ($1, 'own', $2)`, e.UserID, noKcal); err != nil {
		t.Fatal(err)
	}
	var noKcalID int
	if err := e.Pool.QueryRow(e.Ctx, `SELECT id FROM foods WHERE name = $1`, noKcal).Scan(&noKcalID); err != nil {
		t.Fatal(err)
	}
	otherID, _ := e.seedUser("other-food")
	var foreign int
	if err := e.Pool.QueryRow(e.Ctx, `INSERT INTO foods (user_id, source, name, kcal_100g) VALUES ($1, 'own', $2, 1) RETURNING id`,
		otherID, uniq("foreign")).Scan(&foreign); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct{ body, param string }{
		"with calories":      {`{"calories":5,"components":[{"name":"a","grams":1,"calories_per_100g":1}]}`, "components"},
		"no grams":           {`{"components":[{"name":"a","calories_per_100g":1}]}`, "components[0].grams"},
		"zero grams":         {`{"components":[{"name":"a","grams":0,"calories_per_100g":1}]}`, "components[0].grams"},
		"food and adhoc":     {`{"components":[{"food_id":1,"name":"a","grams":1}]}`, "components[0].food_id"},
		"unknown food":       {`{"components":[{"food_id":2000000000,"grams":1}]}`, "components[0].food_id"},
		"food without kcal":  {fmt.Sprintf(`{"components":[{"food_id":%d,"grams":1}]}`, noKcalID), "components[0].food_id"},
		"someone's own food": {fmt.Sprintf(`{"components":[{"food_id":%d,"grams":1}]}`, foreign), "components[0].food_id"},
		"adhoc no name":      {`{"components":[{"grams":1,"calories_per_100g":1}]}`, "components[0].name"},
		"adhoc no kcal":      {`{"components":[{"name":"a","grams":1}]}`, "components[0].calories_per_100g"},
		"total too large":    {`{"components":[{"name":"a","grams":10000,"calories_per_100g":1000}]}`, "grams"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := requireProblem(t, e.post("/api/v1/entries", token, c.body), http.StatusUnprocessableEntity)
			if len(p.InvalidParams) == 0 || p.InvalidParams[0].Name != c.param {
				t.Errorf("invalid_params = %+v, want first %q", p.InvalidParams, c.param)
			}
		})
	}

	// A rejected create must leave nothing behind.
	if n := e.entryCount(); n != 0 {
		t.Errorf("%d entries exist after only rejected creates", n)
	}
}
