package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The SPA's catalog surface: food search, adding a component to an entry, and
// creating an entry with components and an eaten_at. These are the session
// counterparts of the v1 routes and share their SQL, so the tests pin the
// session-specific parts: the response shape, ownership and the legacy errors.

func searchNames(t *testing.T, e *v1Env, q string) []string {
	t.Helper()
	rec, _ := sessionCall(t, e, e.UserID, http.MethodGet, "/api/foods/search?q="+url.QueryEscape(q), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search %q: status = %d (body: %s)", q, rec.Code, rec.Body.String())
	}
	var out struct {
		OK    bool     `json:"ok"`
		Foods []v1Food `json:"foods"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatalf("search %q: bad body %s", q, rec.Body.String())
	}
	names := make([]string, len(out.Foods))
	for i, f := range out.Foods {
		names[i] = f.Name
	}
	return names
}

func TestSessionFoodSearchFindsOwnFoodsAndIgnoresSpaces(t *testing.T) {
	e := newV1Env(t)
	name := uniq("Hafer Flocken")
	e.seedFoodWithFiber(name, 372, 13.5, 10)

	compact := strings.ReplaceAll(name, " ", "")
	found := false
	for _, n := range searchNames(t, e, compact) {
		found = found || n == name
	}
	if !found {
		t.Errorf("search %q did not find %q", compact, name)
	}
}

func TestSessionFoodSearchValidatesTheQuery(t *testing.T) {
	e := newV1Env(t)
	for path, want := range map[string]int{
		"/api/foods/search?q=a":              http.StatusUnprocessableEntity,
		"/api/foods/search":                  http.StatusUnprocessableEntity,
		"/api/foods/search?q=oats&limit=0":   http.StatusBadRequest,
		"/api/foods/search?q=oats&limit=abc": http.StatusBadRequest,
	} {
		if rec, _ := sessionCall(t, e, e.UserID, http.MethodGet, path, ""); rec.Code != want {
			t.Errorf("GET %s: status = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestSessionFoodSearchDoesNotShowOtherUsersFoods(t *testing.T) {
	e := newV1Env(t)
	name := uniq("Private Bread")
	e.seedFoodWithFiber(name, 250, 8, 4)

	otherID, _ := e.seedUser("-other")
	rec, _ := sessionCall(t, e, otherID, http.MethodGet, "/api/foods/search?q="+url.QueryEscape(name), "")
	if strings.Contains(rec.Body.String(), name) {
		t.Errorf("another account sees %q: %s", name, rec.Body.String())
	}
}

func TestAddComponentRecomputesAndChecksOwnership(t *testing.T) {
	e := newV1Env(t)
	token := e.token("entries:write", "entries:read")
	oats := e.seedFoodWithFiber(uniq("oats"), 400, 10, 10)
	entry := e.createEntry(token, fmt.Sprintf(`{"components":[{"food_id":%d,"grams":50}]}`, oats))
	path := fmt.Sprintf("/api/entries/%d/components", entry.ID)

	rec, got := sessionCall(t, e, e.UserID, http.MethodPost, path, fmt.Sprintf(`{"food_id":%d,"grams":100}`, oats))
	if rec.Code != http.StatusCreated || !got.OK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got.Entry.Calories != 600 || len(got.Entry.Components) != 2 {
		t.Errorf("calories = %d, components = %d; want 600 and 2", got.Entry.Calories, len(got.Entry.Components))
	}

	// An ad-hoc component works the same way.
	rec, got = sessionCall(t, e, e.UserID, http.MethodPost, path, `{"name":"Milk","grams":200,"calories_per_100g":64}`)
	if rec.Code != http.StatusCreated || got.Entry.Calories != 728 {
		t.Errorf("ad hoc: status = %d, calories = %d; want 201 and 728", rec.Code, got.Entry.Calories)
	}

	for body, want := range map[string]int{
		`{"food_id":999999999,"grams":10}`:            http.StatusUnprocessableEntity,
		fmt.Sprintf(`{"food_id":%d}`, oats):           http.StatusUnprocessableEntity,
		fmt.Sprintf(`{"food_id":%d,"grams":0}`, oats): http.StatusUnprocessableEntity,
		`not json`: http.StatusBadRequest,
	} {
		if rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, path, body); rec.Code != want {
			t.Errorf("body %s: status = %d, want %d", body, rec.Code, want)
		}
	}

	otherID, _ := e.seedUser("-other")
	if rec, _ := sessionCall(t, e, otherID, http.MethodPost, path, fmt.Sprintf(`{"food_id":%d,"grams":10}`, oats)); rec.Code != http.StatusNotFound {
		t.Errorf("foreign entry: status = %d, want 404", rec.Code)
	}
	// A foreign account's own food cannot be added either.
	otherFood := e.seedFoodForUser(otherID, uniq("secret"), 100)
	if rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, path, fmt.Sprintf(`{"food_id":%d,"grams":10}`, otherFood)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("foreign food: status = %d, want 422", rec.Code)
	}
}

func (e *v1Env) seedFoodForUser(userID int, name string, kcal float64) int {
	e.t.Helper()
	var id int
	if err := e.Pool.QueryRow(e.Ctx, `
		INSERT INTO foods (user_id, source, name, kcal_100g) VALUES ($1, 'own', $2, $3) RETURNING id`,
		userID, name, kcal).Scan(&id); err != nil {
		e.t.Fatalf("seeding food: %v", err)
	}
	return id
}

func createdID(t *testing.T, body []byte) int {
	t.Helper()
	var out struct {
		OK bool `json:"ok"`
		ID int  `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !out.OK || out.ID == 0 {
		t.Fatalf("create answered %s", body)
	}
	return out.ID
}

func TestCreateEntryWithComponentsAndEatenAt(t *testing.T) {
	e := newV1Env(t)
	oats := e.seedFoodWithFiber(uniq("oats"), 400, 10, 10)

	body := fmt.Sprintf(`{"entry_name":"Porridge","entry_date":"2026-03-04","eaten_at":"2026-03-04T09:00:00Z",
		"components":[{"food_id":%d,"grams":50},{"name":"Milk","grams":200,"calories_per_100g":64}]}`, oats)
	rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, "/entries", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	id := createdID(t, rec.Body.Bytes())

	_, got := sessionCall(t, e, e.UserID, http.MethodGet, fmt.Sprintf("/api/entries/%d", id), "")
	if got.Entry.Calories != 328 || len(got.Entry.Components) != 2 || got.Entry.Name == nil || *got.Entry.Name != "Porridge" {
		t.Errorf("entry = %+v", got.Entry)
	}
	if got.Entry.Date != "2026-03-04" || got.Entry.EatenAt.UTC().Format("15:04") != "09:00" {
		t.Errorf("date = %s, eaten_at = %s; want 2026-03-04 and 09:00", got.Entry.Date, got.Entry.EatenAt)
	}
}

func TestCreateEntryWithComponentsRefusesTotalsAndMismatchedDay(t *testing.T) {
	e := newV1Env(t)
	comp := `"components":[{"name":"Rice","grams":100,"calories_per_100g":130}]`
	for name, body := range map[string]string{
		"amount with components":     `{"amount":"500",` + comp + `}`,
		"macro with components":      `{"protein_g":"20",` + comp + `}`,
		"date differs from eaten_at": `{"entry_date":"2026-03-05","eaten_at":"2026-03-04T09:00:00Z",` + comp + `}`,
		"bad eaten_at":               `{"eaten_at":"yesterday",` + comp + `}`,
		"unknown food":               `{"components":[{"food_id":999999999,"grams":10}]}`,
	} {
		rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, "/entries", body)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("%s: status = %d, want 4xx (body: %s)", name, rec.Code, rec.Body.String())
		}
	}
	var n int
	if err := e.Pool.QueryRow(e.Ctx, "SELECT COUNT(*) FROM calorie_entries WHERE user_id = $1", e.UserID).Scan(&n); err != nil || n != 0 {
		t.Errorf("rejected requests left %d entries behind (err %v)", n, err)
	}
}

func TestCreateDirectEntryAcceptsEatenAt(t *testing.T) {
	e := newV1Env(t)
	rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, "/entries",
		`{"entry_name":"Mensa","amount":"750","eaten_at":"2026-03-04T12:30:00Z"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var date, hhmm string
	var amount int
	if err := e.Pool.QueryRow(e.Ctx, `
		SELECT entry_date::text, amount, to_char(eaten_at AT TIME ZONE 'UTC', 'HH24:MI')
		FROM calorie_entries WHERE user_id = $1`, e.UserID).Scan(&date, &amount, &hhmm); err != nil {
		t.Fatal(err)
	}
	if date != "2026-03-04" || amount != 750 || hhmm != "12:30" {
		t.Errorf("stored %s %d %s; want 2026-03-04 750 12:30", date, amount, hhmm)
	}
}

func TestUpdateEntryEatenAtMovesTheDayAndNullClearsIt(t *testing.T) {
	e := newV1Env(t)
	token := e.token("entries:write", "entries:read")
	entry := e.createEntry(token, `{"components":[{"name":"Rice","grams":100,"calories_per_100g":130}]}`)
	path := fmt.Sprintf("/entries/%d/update", entry.ID)

	rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, path, `{"eaten_at":"2026-03-04T18:45:00Z"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Entry struct {
			Date string `json:"date"`
			Time string `json:"time"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Entry.Date != "2026-03-04" || resp.Entry.Time == "" {
		t.Errorf("entry = %+v; want the moved day and a time", resp.Entry)
	}
	var hhmm string
	_ = e.Pool.QueryRow(e.Ctx, `SELECT to_char(eaten_at AT TIME ZONE 'UTC', 'HH24:MI') FROM calorie_entries WHERE id = $1`, entry.ID).Scan(&hhmm)
	if hhmm != "18:45" {
		t.Errorf("stored time = %q, want 18:45", hhmm)
	}

	rec, _ = sessionCall(t, e, e.UserID, http.MethodPost, path, `{"eaten_at":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var cleared bool
	_ = e.Pool.QueryRow(e.Ctx, `SELECT eaten_at IS NULL FROM calorie_entries WHERE id = $1`, entry.ID).Scan(&cleared)
	if !cleared {
		t.Error("eaten_at null did not clear the column")
	}

	if rec, _ := sessionCall(t, e, e.UserID, http.MethodPost, path, `{"eaten_at":"soon"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad eaten_at: status = %d, want 422", rec.Code)
	}
}
