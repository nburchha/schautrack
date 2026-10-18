package handler

import (
	"net/http"
	"testing"

	"schautrack/internal/service"
)

// Basisziel macros over /me (spec #22, ADR 0007): GET reports the raw
// permanent goal next to daily_goal, PATCH merges macro keys into it.

// meGoals reads user.goals off GET /me.
func (e *v1Env) meGoals(token string) v1GoalValues {
	e.t.Helper()
	rec := e.get("/api/v1/me", token)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET /me: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	requireSchema(e.t, rec, "Me")
	var me v1Me
	decodeJSON(e.t, rec, &me)
	return me.User.Goals
}

// storedMacroGoals returns users.macro_goals as stored, for the tests that
// must see keys /me does not report (the *_mode settings).
func (e *v1Env) storedMacroGoals() map[string]any {
	e.t.Helper()
	var raw map[string]any
	if err := e.Pool.QueryRow(e.Ctx, `SELECT COALESCE(macro_goals, '{}'::jsonb) FROM users WHERE id = $1`, e.UserID).Scan(&raw); err != nil {
		e.t.Fatalf("reading macro_goals: %v", err)
	}
	return raw
}

func TestV1MeReportsTheRawBasisziel(t *testing.T) {
	e := newV1Env(t)
	// sugar and fat are disabled but have stored goals: /me reports the raw
	// Basisziel, not the enabled-filtered view the day goal uses.
	seedBasisziel(t, e, baseGoals, `{"calories":true,"protein":true,"carbs":true,"fiber":true}`)
	token := e.token(service.ScopeSettingsWrite)

	g := e.meGoals(token)
	requireGoalInt(t, "calories", g.Calories, 2400)
	requireGoalInt(t, "protein", g.Protein, 150)
	requireGoalInt(t, "carbs", g.Carbs, 300)
	requireGoalInt(t, "fat", g.Fat, 70)
	requireGoalInt(t, "fiber", g.Fiber, 30)
	requireGoalInt(t, "sugar", g.Sugar, 50)

	var me v1Me
	decodeJSON(t, e.get("/api/v1/me", token), &me)
	requireGoalInt(t, "daily_goal", me.User.DailyGoal, 2400)
}
