package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"schautrack/internal/middleware"
	"schautrack/internal/model"
	"schautrack/internal/service"
	"schautrack/internal/sse"
)

// The meal detail page's session routes: read an entry with its components,
// change a component's grams, delete a component. Entries are created through
// the v1 API (the only way to make components today) and read back through the
// session handlers, so the test also pins that both surfaces agree on the sum.

type detailResponse struct {
	OK              bool     `json:"ok"`
	Error           string   `json:"error"`
	Entry           v1Entry  `json:"entry"`
	EnabledMacros   []string `json:"enabledMacros"`
	CaloriesEnabled bool     `json:"caloriesEnabled"`
}

// sessionCall drives the real handlers for userID through a chi router, which
// supplies the {id}/{cid} URL params the routes in main.go would.
func sessionCall(t *testing.T, e *v1Env, userID int, method, path, body string) (*httptest.ResponseRecorder, detailResponse) {
	t.Helper()
	h := &EntriesHandler{Pool: e.Pool, Broker: sse.NewBroker(e.Pool)}
	r := chi.NewRouter()
	r.Get("/api/entries/{id}", h.EntryDetail)
	r.Post("/api/entries/{id}/components/{cid}/update", h.UpdateComponent)
	r.Post("/api/entries/{id}/components/{cid}/delete", h.DeleteComponent)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithTestUser(req.Context(), &model.User{
		ID: userID, Email: e.Email,
		MacrosEnabled: json.RawMessage(`{"calories":true,"protein":true,"fiber":true}`),
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var out detailResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (e *v1Env) seedFoodWithFiber(name string, kcal, protein, fiber float64) int {
	e.t.Helper()
	var id int
	if err := e.Pool.QueryRow(e.Ctx, `
		INSERT INTO foods (user_id, source, name, kcal_100g, protein_100g, fiber_100g)
		VALUES ($1, 'own', $2, $3, $4, $5) RETURNING id`, e.UserID, name, kcal, protein, fiber).Scan(&id); err != nil {
		e.t.Fatalf("seeding food: %v", err)
	}
	return id
}

func TestEntryDetailReturnsComponentsAndSettings(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	oats := e.seedFoodWithFiber(uniq("oats"), 372, 13.5, 10)
	entry := e.createEntry(token, fmt.Sprintf(`{"name":"Porridge","components":[
		{"food_id":%d,"grams":50},
		{"name":"Milk","grams":200,"calories_per_100g":64}]}`, oats))

	rec, got := sessionCall(t, e, e.UserID, http.MethodGet, fmt.Sprintf("/api/entries/%d", entry.ID), "")
	if rec.Code != http.StatusOK || !got.OK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got.Entry.ID != entry.ID || got.Entry.Calories != 314 || len(got.Entry.Components) != 2 {
		t.Errorf("entry = %+v", got.Entry)
	}
	if f := got.Entry.Components[0].Macros.FiberG; f == nil || *f != 10 {
		t.Errorf("fiber per 100 g = %v, want 10", f)
	}
	if got.Entry.Components[1].FoodID != nil {
		t.Error("the ad hoc component must come back without a food id")
	}
	if !got.CaloriesEnabled || strings.Join(got.EnabledMacros, ",") != "protein,fiber" {
		t.Errorf("settings = calories %v macros %v", got.CaloriesEnabled, got.EnabledMacros)
	}
}

func TestEntryDetailHidesOtherUsersEntries(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	entry := e.createEntry(token, `{"components":[{"name":"Rice","grams":100,"calories_per_100g":130}]}`)

	otherID, _ := e.seedUser("-other")
	path := fmt.Sprintf("/api/entries/%d", entry.ID)
	if rec, _ := sessionCall(t, e, otherID, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
		t.Errorf("read: status = %d, want 404", rec.Code)
	}
	cpath := fmt.Sprintf("%s/components/%d", path, entry.Components[0].ID)
	if rec, _ := sessionCall(t, e, otherID, http.MethodPost, cpath+"/update", `{"grams":500}`); rec.Code != http.StatusNotFound {
		t.Errorf("update: status = %d, want 404", rec.Code)
	}
	if rec, _ := sessionCall(t, e, otherID, http.MethodPost, cpath+"/delete", ``); rec.Code != http.StatusNotFound {
		t.Errorf("delete: status = %d, want 404", rec.Code)
	}
	// The owner's entry is untouched.
	if _, got := sessionCall(t, e, e.UserID, http.MethodGet, path, ""); got.Entry.Components[0].Grams != 100 {
		t.Errorf("grams = %v, want 100", got.Entry.Components[0].Grams)
	}
}

func TestUpdateComponentRecomputesTheSum(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	food := e.seedFoodWithFiber(uniq("lentils"), 100, 10, 8)
	entry := e.createEntry(token, fmt.Sprintf(`{"components":[{"food_id":%d,"grams":100}]}`, food))
	path := fmt.Sprintf("/api/entries/%d/components/%d/update", entry.ID, entry.Components[0].ID)

	rec, got := sessionCall(t, e, e.UserID, http.MethodPost, path, `{"grams":250}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got.Entry.Calories != 250 || got.Entry.Components[0].Grams != 250 {
		t.Errorf("after update: calories %d, grams %v; want 250 and 250", got.Entry.Calories, got.Entry.Components[0].Grams)
	}
	if f := got.Entry.Macros.FiberG; f == nil || *f != 20 {
		t.Errorf("entry fiber = %v, want 20", f)
	}

	// The same entry read through v1 agrees: one rule, two surfaces.
	var v1 v1Entry
	decodeJSON(t, e.get(fmt.Sprintf("/api/v1/entries/%d", entry.ID), token), &v1)
	if v1.Calories != got.Entry.Calories {
		t.Errorf("v1 calories = %d, session calories = %d", v1.Calories, got.Entry.Calories)
	}

	for _, body := range []string{`{"grams":0}`, `{"grams":-5}`, `{"grams":10001}`, `{}`, `{"grams":"abc"}`} {
		if rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
	if rec, _ := sessionCall(t, e, e.UserID, http.MethodPost,
		fmt.Sprintf("/api/entries/%d/components/999999/update", entry.ID), `{"grams":10}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown component: status = %d, want 404", rec.Code)
	}
}

func TestDeleteComponentKeepsTheEntryAsADirectEntry(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	entry := e.createEntry(token, `{"components":[
		{"name":"Bread","grams":100,"calories_per_100g":250},
		{"name":"Butter","grams":10,"calories_per_100g":740}]}`)
	if entry.Calories != 324 {
		t.Fatalf("seed calories = %d, want 324", entry.Calories)
	}
	base := fmt.Sprintf("/api/entries/%d/components/", entry.ID)

	rec, got := sessionCall(t, e, e.UserID, http.MethodPost, base+fmt.Sprint(entry.Components[1].ID)+"/delete", "")
	if rec.Code != http.StatusOK || got.Entry.Calories != 250 || len(got.Entry.Components) != 1 {
		t.Fatalf("after first delete: status %d, calories %d, components %d (body: %s)",
			rec.Code, got.Entry.Calories, len(got.Entry.Components), rec.Body.String())
	}

	// Removing the last component leaves the last computed totals in place
	// (ADR 0002): the entry becomes a direct entry rather than dropping to 0.
	rec, got = sessionCall(t, e, e.UserID, http.MethodPost, base+fmt.Sprint(entry.Components[0].ID)+"/delete", "")
	if rec.Code != http.StatusOK || got.Entry.Calories != 250 || len(got.Entry.Components) != 0 {
		t.Errorf("after last delete: status %d, calories %d, components %d", rec.Code, got.Entry.Calories, len(got.Entry.Components))
	}
	if got.Entry.Components == nil {
		t.Error("components must be an empty list, never null")
	}
}

func TestDayListCountsComponents(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeEntriesWrite, service.ScopeEntriesRead)
	withParts := e.createEntry(token, `{"date":"2026-08-05","components":[
		{"name":"A","grams":10,"calories_per_100g":100},{"name":"B","grams":10,"calories_per_100g":100}]}`)
	direct := e.createEntry(token, `{"date":"2026-08-05","calories":300,"name":"Mensa"}`)

	h := &EntriesHandler{Pool: e.Pool, Broker: sse.NewBroker(e.Pool)}
	req := httptest.NewRequest(http.MethodGet, "/entries/day?date=2026-08-05", nil)
	req = req.WithContext(middleware.WithTestUser(req.Context(), &model.User{ID: e.UserID, Email: e.Email}))
	rec := httptest.NewRecorder()
	h.DayEntries(rec, req)

	var out struct {
		Entries []struct {
			ID             int `json:"id"`
			ComponentCount int `json:"componentCount"`
		} `json:"entries"`
	}
	decodeJSON(t, rec, &out)
	counts := map[int]int{}
	for _, en := range out.Entries {
		counts[en.ID] = en.ComponentCount
	}
	if counts[withParts.ID] != 2 || counts[direct.ID] != 0 || len(counts) != 2 {
		t.Errorf("componentCount by entry = %v, want %d:2 and %d:0", counts, withParts.ID, direct.ID)
	}
}
