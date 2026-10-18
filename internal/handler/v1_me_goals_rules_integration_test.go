package handler

import (
	"net/http"
	"testing"

	"schautrack/internal/service"
)

func TestV1MePatchNullClearsOneMacroGoal(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)

	if rec := e.patch("/api/v1/me", token, `{"goals":{"fiber":null}}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	g := e.meGoals(token)
	if g.Fiber != nil {
		t.Errorf("fiber = %d, want cleared", *g.Fiber)
	}
	requireGoalInt(t, "protein", g.Protein, 150)
	requireGoalInt(t, "carbs", g.Carbs, 300)
	requireGoalInt(t, "calories", g.Calories, 2400)
}

// 0 means "no goal", exactly as in the Settings form, so it reads back as null.
func TestV1MePatchZeroMeansNoGoal(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)

	if rec := e.patch("/api/v1/me", token, `{"goals":{"sugar":0}}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if g := e.meGoals(token); g.Sugar != nil {
		t.Errorf("sugar = %d, want null", *g.Sugar)
	}
}

// Calories and macros may travel in one request; both land in macro_goals.
func TestV1MePatchCaloriesAndMacrosTogether(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"protein":100}`, allMacrosOn)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)

	rec := e.patch("/api/v1/me", token, `{"daily_goal":2800,"goals":{"carbs":450}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	g := e.meGoals(token)
	requireGoalInt(t, "calories", g.Calories, 2800)
	requireGoalInt(t, "carbs", g.Carbs, 450)
	requireGoalInt(t, "protein", g.Protein, 100)
}

// A token that may change settings but not goals is refused when the body
// carries macro goals, and nothing in the request is applied.
func TestV1MePatchMacroGoalsNeedGoalsWrite(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	settingsOnly := e.token(service.ScopeSettingsWrite)

	rec := e.patch("/api/v1/me", settingsOnly, `{"daily_goal":2000,"goals":{"carbs":999}}`)
	p := requireProblem(t, rec, http.StatusForbidden)
	if p.Type != "https://schautrack.com/problems/insufficient-scope" {
		t.Errorf("problem type = %q, want insufficient-scope", p.Type)
	}
	g := e.meGoals(settingsOnly)
	requireGoalInt(t, "calories (untouched)", g.Calories, 2400)
	requireGoalInt(t, "carbs (untouched)", g.Carbs, 300)

	// goals:read is not enough either; write is what implies read, not the reverse.
	readToo := e.token(service.ScopeSettingsWrite, service.ScopeGoalsRead)
	requireProblem(t, e.patch("/api/v1/me", readToo, `{"goals":{"carbs":999}}`), http.StatusForbidden)
}

// The same token can still set the calorie goal alone, and a calories-only
// PATCH leaves macro goals and modes as they were.
func TestV1MeCaloriesOnlyPatchKeepsMacroGoalsAndModes(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400,"protein":150,"carbs":300,"protein_mode":"limit","fat_mode":"target"}`, allMacrosOn)
	settingsOnly := e.token(service.ScopeSettingsWrite)

	if rec := e.patch("/api/v1/me", settingsOnly, `{"daily_goal":2100}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	g := e.meGoals(settingsOnly)
	requireGoalInt(t, "calories", g.Calories, 2100)
	requireGoalInt(t, "protein", g.Protein, 150)
	requireGoalInt(t, "carbs", g.Carbs, 300)

	stored := e.storedMacroGoals()
	if stored["protein_mode"] != "limit" || stored["fat_mode"] != "target" {
		t.Errorf("modes were not preserved: %v", stored)
	}

	// And clearing the calorie goal likewise leaves the rest.
	if rec := e.patch("/api/v1/me", settingsOnly, `{"daily_goal":null}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	g = e.meGoals(settingsOnly)
	if g.Calories != nil {
		t.Errorf("calories = %d, want cleared", *g.Calories)
	}
	requireGoalInt(t, "protein", g.Protein, 150)
}

// A macro goal patch must not disturb the modes either.
func TestV1MeMacroPatchKeepsModes(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"protein":150,"protein_mode":"limit"}`, allMacrosOn)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)

	if rec := e.patch("/api/v1/me", token, `{"goals":{"protein":180}}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if stored := e.storedMacroGoals(); stored["protein_mode"] != "limit" {
		t.Errorf("protein_mode lost: %v", stored)
	}
}

func TestV1MePatchMacroGoalValidation(t *testing.T) {
	e := newV1Env(t)
	// fiber is not enabled for this account.
	seedBasisziel(t, e, `{"calories":2400,"protein":150,"fiber":30}`, `{"calories":true,"protein":true,"carbs":true,"fat":true}`)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)

	cases := []struct{ name, body, param string }{
		{"negative", `{"goals":{"protein":-1}}`, "goals.protein"},
		{"too high", `{"goals":{"carbs":10000}}`, "goals.carbs"},
		{"disabled macro", `{"goals":{"fiber":40}}`, "goals.fiber"},
		{"disabled macro with valid sibling", `{"goals":{"protein":160,"sugar":10}}`, "goals.sugar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := requireProblem(t, e.patch("/api/v1/me", token, c.body), http.StatusUnprocessableEntity)
			if len(p.InvalidParams) == 0 || p.InvalidParams[0].Name != c.param {
				t.Errorf("invalid_params = %+v, want it to name %s", p.InvalidParams, c.param)
			}
		})
	}

	// Rejections apply nothing, including the valid sibling.
	g := e.meGoals(token)
	requireGoalInt(t, "protein", g.Protein, 150)

	// The upper bound itself is fine, and a disabled macro may still be cleared.
	if rec := e.patch("/api/v1/me", token, `{"goals":{"carbs":9999,"fiber":null}}`); rec.Code != http.StatusOK {
		t.Fatalf("boundary: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	g = e.meGoals(token)
	requireGoalInt(t, "carbs", g.Carbs, 9999)
	if g.Fiber != nil {
		t.Errorf("fiber = %d, want cleared", *g.Fiber)
	}
}

// An empty goals object changes nothing and, like an empty body, is not a
// request; it does not need goals:write.
func TestV1MePatchEmptyGoalsObjectIsNoUpdate(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeSettingsWrite)
	requireProblem(t, e.patch("/api/v1/me", token, `{"goals":{}}`), http.StatusBadRequest)
}

func TestV1MePatchRejectsUnknownGoalKey(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)
	requireProblem(t, e.patch("/api/v1/me", token, `{"goals":{"water":3}}`), http.StatusBadRequest)
}
