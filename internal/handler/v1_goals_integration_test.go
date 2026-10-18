package handler

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"schautrack/internal/service"
)

// Behaviour of the goal-adjustment endpoints (ADR 0007): a Tagesziel-Anpassung
// is a complete per-date snapshot of the Basisziel, filled from the Basisziel
// at save time and frozen thereafter.

// seedBasisziel sets the account's permanent goal and the macros it tracks.
func seedBasisziel(t *testing.T, e *v1Env, goals, enabled string) {
	t.Helper()
	if _, err := e.Pool.Exec(e.Ctx,
		`UPDATE users SET macro_goals = $2::jsonb, macros_enabled = $3::jsonb, daily_goal = NULL WHERE id = $1`,
		e.UserID, goals, enabled); err != nil {
		t.Fatalf("seeding Basisziel: %v", err)
	}
}

const allMacrosOn = `{"calories":true,"protein":true,"carbs":true,"fat":true,"fiber":true,"sugar":true}`
const baseGoals = `{"calories":2400,"protein":150,"carbs":300,"fat":70,"fiber":30,"sugar":50}`

func intp(n int) *int { return &n }

func requireGoalInt(t *testing.T, field string, got *int, want int) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %d", field, got, want)
	}
}

// TestV1GoalPutFillsGapsFromBasisziel is the headline story: send only what
// differs ("calories 3100, carbs 480") and receive a complete goal.
func TestV1GoalPutFillsGapsFromBasisziel(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeGoalsWrite)

	rec := e.do(call{Method: http.MethodPut, Path: "/api/v1/goals/2026-08-05", Token: token,
		Body: `{"calories":3100,"carbs":480,"reason":"  Lauf 25 km "}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/goals/2026-08-05" {
		t.Errorf("Location = %q", loc)
	}
	requireSchema(t, rec, "Goal")

	var g v1Goal
	decodeJSON(t, rec, &g)
	if g.Date != "2026-08-05" || !g.Adjusted {
		t.Errorf("date/adjusted = %q/%v", g.Date, g.Adjusted)
	}
	if g.Reason == nil || *g.Reason != "Lauf 25 km" {
		t.Errorf("reason = %v, want trimmed %q", g.Reason, "Lauf 25 km")
	}
	requireGoalInt(t, "calories", g.Calories, 3100)
	requireGoalInt(t, "carbs", g.Carbs, 480)
	requireGoalInt(t, "protein", g.Protein, 150)
	requireGoalInt(t, "fat", g.Fat, 70)
	requireGoalInt(t, "fiber", g.Fiber, 30)
	requireGoalInt(t, "sugar", g.Sugar, 50)
	if g.Base == nil {
		t.Fatal("base missing on an adjusted day")
	}
	requireGoalInt(t, "base.calories", g.Base.Calories, 2400)
	requireGoalInt(t, "base.carbs", g.Base.Carbs, 300)
	if g.UpdatedAt == nil {
		t.Error("updated_at missing on an adjusted day")
	}
}

// TestV1GoalDeleteResetsTheDay covers DELETE, including the deliberate
// difference to weight: deleting an adjustment that is not there is a 204,
// not a 404, so a cancelled workout can always be "reset" without a lookup.
func TestV1GoalDeleteResetsTheDay(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	write := e.token(service.ScopeGoalsWrite)

	if rec := e.do(call{Method: http.MethodPut, Path: "/api/v1/goals/2026-08-05", Token: write,
		Body: `{"calories":3100}`}); rec.Code != http.StatusCreated {
		t.Fatalf("PUT: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	for i := 0; i < 2; i++ { // second pass: nothing left to delete
		rec := e.do(call{Method: http.MethodDelete, Path: "/api/v1/goals/2026-08-05", Token: write})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE #%d: status = %d, want 204 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
	}

	var g v1Goal
	decodeJSON(t, e.get("/api/v1/goals/2026-08-05", write), &g)
	if g.Adjusted || g.Base != nil || g.UpdatedAt != nil || g.Reason != nil {
		t.Errorf("after DELETE the day is still adjusted: %+v", g)
	}
	requireGoalInt(t, "calories", g.Calories, 2400)
}

func (e *v1Env) putGoal(token, date, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(call{Method: http.MethodPut, Path: "/api/v1/goals/" + date, Token: token, Body: body})
}

func (e *v1Env) getGoal(token, date string) v1Goal {
	e.t.Helper()
	rec := e.get("/api/v1/goals/"+date, token)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET goal %s: status = %d (body: %s)", date, rec.Code, rec.Body.String())
	}
	requireSchema(e.t, rec, "Goal")
	var g v1Goal
	decodeJSON(e.t, rec, &g)
	return g
}

// TestV1GoalSnapshotSurvivesBasiszielChange is ADR 0007's core promise in both
// directions: the adjusted day is frozen, an unadjusted day follows.
func TestV1GoalSnapshotSurvivesBasiszielChange(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeGoalsWrite)

	if rec := e.putGoal(token, "2026-08-05", `{"carbs":480}`); rec.Code != http.StatusCreated {
		t.Fatalf("PUT: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	seedBasisziel(t, e, `{"calories":1800,"protein":100,"carbs":200,"fat":50,"fiber":20,"sugar":40}`, allMacrosOn)

	adjusted := e.getGoal(token, "2026-08-05")
	requireGoalInt(t, "adjusted calories", adjusted.Calories, 2400)
	requireGoalInt(t, "adjusted protein", adjusted.Protein, 150)
	requireGoalInt(t, "adjusted carbs", adjusted.Carbs, 480)
	// base reports the permanent goal as it is now.
	requireGoalInt(t, "base.calories", adjusted.Base.Calories, 1800)

	plain := e.getGoal(token, "2026-08-06")
	if plain.Adjusted {
		t.Error("unadjusted day reports adjusted")
	}
	requireGoalInt(t, "unadjusted calories", plain.Calories, 1800)
	requireGoalInt(t, "unadjusted carbs", plain.Carbs, 200)
	if plain.Base != nil || plain.UpdatedAt != nil || plain.Reason != nil {
		t.Errorf("unadjusted day carries adjustment fields: %+v", plain)
	}
}

// TestV1GoalPutIsIdempotentAndReplaces: repeating yields the same state with
// 200, and a replacing PUT fills from the Basisziel again, not from the
// previous adjustment.
func TestV1GoalPutIsIdempotentAndReplaces(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeGoalsWrite)
	const day = "2026-08-05"

	first := e.putGoal(token, day, `{"calories":3100,"reason":"Lauf"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first PUT: status = %d", first.Code)
	}
	second := e.putGoal(token, day, `{"calories":3100,"reason":"Lauf"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("repeated PUT: status = %d, want 200 (body: %s)", second.Code, second.Body.String())
	}
	if second.Header().Get("Location") != "" {
		t.Error("Location set on a replacing PUT")
	}
	var a, b v1Goal
	decodeJSON(t, first, &a)
	decodeJSON(t, second, &b)
	a.UpdatedAt, b.UpdatedAt = nil, nil
	if !reflect.DeepEqual(a, b) {
		t.Errorf("repeated PUT changed the state:\n first  %+v\n second %+v", a, b)
	}

	// Replace: calories and reason were sent before, now only carbs.
	if rec := e.putGoal(token, day, `{"carbs":480}`); rec.Code != http.StatusOK {
		t.Fatalf("replacing PUT: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	g := e.getGoal(token, day)
	requireGoalInt(t, "calories", g.Calories, 2400) // back to the Basisziel
	requireGoalInt(t, "carbs", g.Carbs, 480)
	if g.Reason != nil {
		t.Errorf("reason = %q survived a PUT that did not send one", *g.Reason)
	}

	var n int
	if err := e.Pool.QueryRow(e.Ctx, `SELECT COUNT(*)::int FROM goal_adjustments WHERE user_id = $1`, e.UserID).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d rows (err %v), want 1", n, err)
	}
}

func TestV1GoalAnyDateIsAllowed(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeGoalsWrite)

	for _, day := range []string{"2001-01-01", "2026-08-05", "2200-12-31"} {
		if rec := e.putGoal(token, day, `{"calories":2000}`); rec.Code != http.StatusCreated {
			t.Errorf("PUT %s: status = %d (body: %s)", day, rec.Code, rec.Body.String())
		}
		requireGoalInt(t, day+" calories", e.getGoal(token, day).Calories, 2000)
	}
}

// TestV1GoalGetWorksWithoutAnyGoal: a fresh account has no Basisziel and no
// adjustment; the answer is still a 200 with nulls, not an error.
func TestV1GoalGetWorksWithoutAnyGoal(t *testing.T) {
	e := newV1Env(t)
	g := e.getGoal(e.token(service.ScopeGoalsRead), "2026-08-05")
	if g.Adjusted || g.Calories != nil || g.Protein != nil || g.Carbs != nil || g.Fat != nil || g.Fiber != nil || g.Sugar != nil {
		t.Errorf("fresh account goal = %+v, want all null", g)
	}
}

// TestV1GoalReportsOnlyTrackedMacros: the stored snapshot keeps raw values and
// the filter applies at read time, so enabling a macro later brings its value
// back instead of losing it.
func TestV1GoalReportsOnlyTrackedMacros(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeGoalsWrite)
	e.putGoal(token, "2026-08-05", `{"calories":3100}`)

	seedBasisziel(t, e, baseGoals, `{"calories":true,"protein":true,"carbs":true,"fat":false,"fiber":false,"sugar":false}`)
	g := e.getGoal(token, "2026-08-05")
	if g.Fat != nil || g.Fiber != nil || g.Sugar != nil {
		t.Errorf("disabled macros reported: %+v", g.v1GoalValues)
	}
	requireGoalInt(t, "protein", g.Protein, 150)
	if g.Base == nil || g.Base.Fat != nil {
		t.Errorf("base reports a disabled macro: %+v", g.Base)
	}
	// An unadjusted day is filtered the same way.
	if plain := e.getGoal(token, "2026-08-06"); plain.Fiber != nil {
		t.Errorf("unadjusted day reports disabled fiber: %v", *plain.Fiber)
	}

	seedBasisziel(t, e, baseGoals, allMacrosOn)
	requireGoalInt(t, "fat after re-enabling", e.getGoal(token, "2026-08-05").Fat, 70)
}

// TestV1GoalGapFillCopiesDisabledMacros: gap-fill copies the raw Basisziel for
// every macro, so a macro the account later switches on is not silently empty.
func TestV1GoalGapFillCopiesDisabledMacros(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, `{"calories":true,"protein":true}`)
	token := e.token(service.ScopeGoalsWrite)
	e.putGoal(token, "2026-08-05", `{"protein":160}`)

	var fat *int
	if err := e.Pool.QueryRow(e.Ctx, `SELECT fat FROM goal_adjustments WHERE user_id = $1`, e.UserID).Scan(&fat); err != nil {
		t.Fatal(err)
	}
	requireGoalInt(t, "stored fat", fat, 70)
}

func TestV1GoalZeroMeansNoGoalAndReasonIsNormalised(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	token := e.token(service.ScopeGoalsWrite)

	rec := e.putGoal(token, "2026-08-05", `{"fiber":0,"reason":"   "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var g v1Goal
	decodeJSON(t, rec, &g)
	if g.Fiber != nil {
		t.Errorf("fiber = %d, want null for 0 (no goal)", *g.Fiber)
	}
	if g.Reason != nil {
		t.Errorf("blank reason = %q, want null", *g.Reason)
	}
	requireGoalInt(t, "protein still filled", g.Protein, 150)
}

func TestV1GoalRejectsUnusableInput(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, `{"calories":true,"protein":true,"carbs":true}`)
	token := e.token(service.ScopeGoalsWrite)

	cases := []struct{ name, body, field string }{
		{"negative calories", `{"calories":-1}`, "calories"},
		{"zero calories", `{"calories":0}`, "calories"},
		{"calories too high", `{"calories":10000}`, "calories"},
		{"negative macro", `{"protein":-5}`, "protein"},
		{"macro too high", `{"carbs":10000}`, "carbs"},
		{"disabled macro", `{"fat":50}`, "fat"},
		{"reason too long", `{"reason":"` + strings.Repeat("x", 121) + `"}`, "reason"},
		{"empty body", `{}`, ""},
		{"only nulls", `{"calories":null}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.putGoal(token, "2026-08-05", c.body)
			p := requireProblem(t, rec, http.StatusUnprocessableEntity)
			if c.field == "" {
				return
			}
			if len(p.InvalidParams) == 0 || p.InvalidParams[0].Name != c.field {
				t.Errorf("invalid_params = %+v, want it to name %s", p.InvalidParams, c.field)
			}
		})
	}

	var n int
	if err := e.Pool.QueryRow(e.Ctx, `SELECT COUNT(*)::int FROM goal_adjustments WHERE user_id = $1`, e.UserID).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows after rejected writes (err %v), want 0", n, err)
	}

	// 120 characters is the limit and is accepted; so are the other bounds.
	for _, body := range []string{
		`{"reason":"` + strings.Repeat("ä", 120) + `"}`,
		`{"calories":9999,"carbs":9999,"protein":0}`,
		`{"calories":1}`,
	} {
		if rec := e.putGoal(token, "2026-08-05", body); rec.Code >= 400 {
			t.Errorf("%.40s: status = %d, want success (body: %s)", body, rec.Code, rec.Body.String())
		}
	}
}

func TestV1GoalHiddenCaloriesAreRejected(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, `{"calories":false,"protein":true}`)
	token := e.token(service.ScopeGoalsWrite)

	p := requireProblem(t, e.putGoal(token, "2026-08-05", `{"calories":2000}`), http.StatusUnprocessableEntity)
	if len(p.InvalidParams) == 0 || p.InvalidParams[0].Name != "calories" {
		t.Errorf("invalid_params = %+v, want calories", p.InvalidParams)
	}
	// Hidden calories are not reported either.
	if g := e.getGoal(token, "2026-08-05"); g.Calories != nil {
		t.Errorf("hidden calories reported: %d", *g.Calories)
	}
}

func TestV1GoalBadDateIsBadRequest(t *testing.T) {
	e := newV1Env(t)
	token := e.token(service.ScopeGoalsWrite)
	for _, day := range []string{"2026-02-31", "yesterday", "2026-1-1"} {
		requireProblem(t, e.putGoal(token, day, `{"calories":2000}`), http.StatusBadRequest)
		requireProblem(t, e.get("/api/v1/goals/"+day, token), http.StatusBadRequest)
	}
}

// TestV1GoalScopes: read tokens read, write tokens write and (by implication)
// read; neither is satisfied by unrelated scopes. The scope is checked before
// the body is looked at, so a read-only token with a garbage body still gets
// 403, not 400.
func TestV1GoalScopes(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	read := e.token(service.ScopeGoalsRead)
	write := e.token(service.ScopeGoalsWrite)
	other := e.token(service.ScopeEntriesWrite, service.ScopeSettingsWrite)

	requireProblem(t, e.putGoal(read, "2026-08-05", `{"calories":2000}`), http.StatusForbidden)
	requireProblem(t, e.putGoal(read, "2026-08-05", `not json`), http.StatusForbidden)
	requireProblem(t, e.do(call{Method: http.MethodDelete, Path: "/api/v1/goals/2026-08-05", Token: read}), http.StatusForbidden)
	requireProblem(t, e.get("/api/v1/goals/2026-08-05", other), http.StatusForbidden)
	requireProblem(t, e.putGoal(other, "2026-08-05", `{"calories":2000}`), http.StatusForbidden)

	if rec := e.get("/api/v1/goals/2026-08-05", read); rec.Code != http.StatusOK {
		t.Errorf("read token GET: status = %d", rec.Code)
	}
	if rec := e.putGoal(write, "2026-08-05", `{"calories":2000}`); rec.Code != http.StatusCreated {
		t.Errorf("write token PUT: status = %d", rec.Code)
	}
	if rec := e.get("/api/v1/goals/2026-08-05", write); rec.Code != http.StatusOK {
		t.Errorf("write token GET (implied read): status = %d", rec.Code)
	}
}

// TestV1GoalsAreOwnedByTheAccount: another account's adjustment is invisible.
func TestV1GoalsAreOwnedByTheAccount(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, baseGoals, allMacrosOn)
	other, _ := e.seedUser("-other")
	otherToken := e.tokenFor(other, service.ScopeGoalsWrite)
	e.putGoal(e.token(service.ScopeGoalsWrite), "2026-08-05", `{"calories":3100}`)

	rec := e.do(call{Method: http.MethodDelete, Path: "/api/v1/goals/2026-08-05", Token: otherToken})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if !e.getGoal(e.token(service.ScopeGoalsRead), "2026-08-05").Adjusted {
		t.Error("another account's DELETE removed my adjustment")
	}
	if e.getGoal(otherToken, "2026-08-05").Adjusted {
		t.Error("another account sees my adjustment")
	}
}
