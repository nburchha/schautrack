package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"schautrack/internal/service"
)

// Behaviour of /api/v1/foods: catalog search ranking and scoping, and creating
// foods of one's own.

// seedCatalogFood inserts a global (bls) food directly, the way the importer
// will. The shared test database outlives a run, so callers use a unique tag.
func (e *v1Env) seedCatalogFood(code, name string, kcal *float64) {
	e.t.Helper()
	if _, err := e.Pool.Exec(e.Ctx,
		`INSERT INTO foods (source, source_code, name, kcal_100g) VALUES ('bls', $1, $2, $3)`,
		code, name, kcal); err != nil {
		e.t.Fatalf("seeding food %q: %v", name, err)
	}
}

func (e *v1Env) searchFoods(token, q string) []v1Food {
	e.t.Helper()
	rec := e.get("/api/v1/foods/search?q="+url.QueryEscape(q), token)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("search %q: status = %d (body: %s)", q, rec.Code, rec.Body.String())
	}
	var out v1List[v1Food]
	decodeJSON(e.t, rec, &out)
	return out.Data
}

func TestV1FoodSearchRanking(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsRead)
	tag := fmt.Sprintf("zq%d", time.Now().UnixNano()%1_000_000_000)
	kcal := 360.0

	e.seedCatalogFood(tag+"-1", tag+"haferflocken", &kcal)
	e.seedCatalogFood(tag+"-2", "Instant "+tag+"haferflocken zart", &kcal)
	e.seedCatalogFood(tag+"-3", tag+"haferflocken", nil)

	got := e.searchFoods(token, tag+"haferflocken")
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[2].Name != "Instant "+tag+"haferflocken zart" {
		t.Errorf("substring match ranked %q, want last", got[2].Name)
	}
	var unknown *v1Food
	for i := range got {
		if got[i].SourceCode != nil && *got[i].SourceCode == tag+"-3" {
			unknown = &got[i]
		}
	}
	if unknown == nil || unknown.Calories != nil {
		t.Errorf("unknown kcal must come back null, got %+v", unknown)
	}

	// A typo still finds the food.
	if typo := e.searchFoods(token, tag+"haferflockn"); len(typo) == 0 {
		t.Error("typo query found nothing")
	}
}

// The BLS splits compounds ("Hafer Flocken"); users type them joined.
func TestV1FoodSearchIgnoresSpacesInCompounds(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsRead)
	tag := fmt.Sprintf("zs%d", time.Now().UnixNano()%1_000_000_000)
	kcal := 348.0

	e.seedCatalogFood(tag+"-1", tag+"Hafer Flocken", &kcal)
	e.seedCatalogFood(tag+"-2", tag+"Haferflocken-Nussplätzchen", &kcal)
	e.seedCatalogFood(tag+"-3", tag+"Haferflockenauflauf mit Kakao", &kcal)

	got := e.searchFoods(token, tag+"Haferflocken")
	// The shared test database keeps other runs' rows, which trigram
	// similarity may also return; look only at this run's.
	var mine []string
	for _, f := range got {
		if strings.HasPrefix(f.Name, tag) {
			mine = append(mine, f.Name)
		}
	}
	if len(mine) != 3 || mine[0] != tag+"Hafer Flocken" {
		t.Errorf("joined query: got %v, want the split compound first of 3", mine)
	}
	if got := e.searchFoods(token, tag+"Hafer Flocken"); len(got) == 0 || got[0].Name != tag+"Hafer Flocken" {
		t.Errorf("split query: got %v", got)
	}
}

func TestV1FoodSearchTreatsWildcardsLiterally(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsRead)
	tag := fmt.Sprintf("wq%d", time.Now().UnixNano()%1_000_000_000)
	kcal := 100.0
	e.seedCatalogFood(tag+"-1", tag+"abc", &kcal)

	for _, q := range []string{"%%", "__"} {
		if got := e.searchFoods(token, q); len(got) > 0 {
			for _, f := range got {
				if f.Name != "" && !strings.Contains(strings.ToLower(f.Name), q) {
					t.Errorf("query %q matched %q via a wildcard", q, f.Name)
				}
			}
		}
	}
}

func TestV1FoodSearchValidation(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsRead)

	for _, path := range []string{"/api/v1/foods/search", "/api/v1/foods/search?q=a"} {
		p := requireProblem(t, e.get(path, token), http.StatusUnprocessableEntity)
		if len(p.InvalidParams) == 0 || p.InvalidParams[0].Name != "q" {
			t.Errorf("%s: invalid_params = %+v, want q", path, p.InvalidParams)
		}
	}
}

func TestV1CreateOwnFoodIsPrivateAndSearchable(t *testing.T) {
	e := newV1Env(t)
	write := e.token(service.ScopeFoodsWrite, service.ScopeFoodsRead)
	tag := fmt.Sprintf("bk%d", time.Now().UnixNano()%1_000_000_000)

	rec := e.post("/api/v1/foods", write,
		fmt.Sprintf(`{"name":%q,"calories_per_100g":245.5,"protein_g":8.25,"fat_g":1.5}`, tag+" Bäckerbrot"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /foods: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created v1Food
	decodeJSON(t, rec, &created)
	if created.Source != "own" || created.SourceCode != nil {
		t.Errorf("source = %q code = %v, want own/nil", created.Source, created.SourceCode)
	}
	if created.Calories == nil || *created.Calories != 245.5 {
		t.Errorf("calories = %v, want 245.5", created.Calories)
	}
	if created.Macros.CarbsG != nil {
		t.Errorf("carbs = %v, want null for an unsent macro", created.Macros.CarbsG)
	}

	if got := e.searchFoods(write, tag); len(got) != 1 || got[0].ID != created.ID {
		t.Errorf("owner search = %+v, want the created food", got)
	}

	otherID, _ := e.seedUser("other")
	other := e.tokenFor(otherID, service.ScopeFoodsRead)
	if got := e.searchFoods(other, tag); len(got) != 0 {
		t.Errorf("another account sees %d own foods of someone else", len(got))
	}

	dup := e.post("/api/v1/foods", write,
		fmt.Sprintf(`{"name":%q,"calories_per_100g":1}`, tag+" BÄCKERBROT"))
	requireProblem(t, dup, http.StatusConflict)
}

func TestV1DeleteOwnFood(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsWrite, service.ScopeFoodsRead, service.ScopeEntriesWrite, service.ScopeEntriesRead)
	tag := fmt.Sprintf("dl%d", time.Now().UnixNano()%1_000_000_000)

	foodID := e.seedOwnFood(tag+" Mozzarella", 250, nil)
	entry := e.createEntry(token, fmt.Sprintf(
		`{"name":"Pizza","components":[{"food_id":%d,"grams":100}]}`, foodID))

	path := fmt.Sprintf("/api/v1/foods/%d", foodID)
	if rec := e.do(call{Method: http.MethodDelete, Path: path, Token: token}); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := e.searchFoods(token, tag); len(got) != 0 {
		t.Errorf("deleted food still searchable: %+v", got)
	}
	requireProblem(t, e.do(call{Method: http.MethodDelete, Path: path, Token: token}), http.StatusNotFound)

	// History survives: the component keeps its captured values, minus the link.
	rec := e.get(fmt.Sprintf("/api/v1/entries/%d", entry.ID), token)
	var got v1Entry
	decodeJSON(t, rec, &got)
	if got.Calories != 250 || len(got.Components) != 1 || got.Components[0].FoodID != nil {
		t.Errorf("entry after food delete = %+v, want 250 kcal and an unlinked component", got)
	}
}

func TestV1DeleteFoodRefusesForeignAndSharedFoods(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsWrite)
	tag := fmt.Sprintf("dx%d", time.Now().UnixNano()%1_000_000_000)

	otherID, _ := e.seedUser("other")
	var foreign int
	if err := e.Pool.QueryRow(e.Ctx, `
		INSERT INTO foods (user_id, source, name, kcal_100g) VALUES ($1, 'own', $2, 100) RETURNING id`,
		otherID, tag+" foreign").Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	var shared int
	if err := e.Pool.QueryRow(e.Ctx, `
		INSERT INTO foods (source, source_code, name, kcal_100g) VALUES ('bls', $1, $2, 100) RETURNING id`,
		tag, tag+" shared").Scan(&shared); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]int{"another account's": foreign, "shared catalog": shared} {
		rec := e.do(call{Method: http.MethodDelete, Path: fmt.Sprintf("/api/v1/foods/%d", id), Token: token})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s food: status = %d, want 404", name, rec.Code)
		}
	}
	requireProblem(t, e.do(call{Method: http.MethodDelete, Path: "/api/v1/foods/abc", Token: token}), http.StatusBadRequest)
}

func TestV1CreateFoodValidation(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeFoodsWrite)

	cases := map[string]struct{ body, param string }{
		"no name":      {`{"calories_per_100g":10}`, "name"},
		"no calories":  {`{"name":"x food"}`, "calories_per_100g"},
		"kcal too big": {`{"name":"x food","calories_per_100g":1001}`, "calories_per_100g"},
		"negative":     {`{"name":"x food","calories_per_100g":10,"fat_g":-1}`, "fat_g"},
		"macro > 100":  {`{"name":"x food","calories_per_100g":10,"carbs_g":100.5}`, "carbs_g"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := requireProblem(t, e.post("/api/v1/foods", token, c.body), http.StatusUnprocessableEntity)
			if len(p.InvalidParams) == 0 || p.InvalidParams[0].Name != c.param {
				t.Errorf("invalid_params = %+v, want first %q", p.InvalidParams, c.param)
			}
		})
	}
}

func TestV1FoodsRequireScopes(t *testing.T) {
	e := newV1Env(t)
	if rec := e.get("/api/v1/foods/search?q=haferflocken", e.token(service.ScopeEntriesRead)); rec.Code != http.StatusForbidden {
		t.Errorf("search without foods:read: status = %d, want 403", rec.Code)
	}
	if rec := e.post("/api/v1/foods", e.token(service.ScopeFoodsRead), `{"name":"x food","calories_per_100g":1}`); rec.Code != http.StatusForbidden {
		t.Errorf("create without foods:write: status = %d, want 403", rec.Code)
	}
	if rec := e.do(call{Method: http.MethodDelete, Path: "/api/v1/foods/1", Token: e.token(service.ScopeFoodsRead)}); rec.Code != http.StatusForbidden {
		t.Errorf("delete without foods:write: status = %d, want 403", rec.Code)
	}
}

func TestV1FoodSearchRejectsNUL(t *testing.T) {
	e := newV1Env(t)
	rec := e.get("/api/v1/foods/search?q=ab%00cd", e.token(service.ScopeFoodsRead))
	requireProblem(t, rec, http.StatusBadRequest)
}
