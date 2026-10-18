package handler

import (
	"net/http"
	"testing"

	"schautrack/internal/service"
)

func TestV1MePatchWritesMacroGoalsAndReadsThemBack(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400,"protein":150}`, allMacrosOn)
	token := e.token(service.ScopeSettingsWrite, service.ScopeGoalsWrite)

	rec := e.patch("/api/v1/me", token, `{"goals":{"carbs":420,"fiber":35}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	requireSchema(t, rec, "Me")
	var me v1Me
	decodeJSON(t, rec, &me)
	requireGoalInt(t, "response carbs", me.User.Goals.Carbs, 420)

	g := e.meGoals(token)
	requireGoalInt(t, "carbs", g.Carbs, 420)
	requireGoalInt(t, "fiber", g.Fiber, 35)
	// Merged into the stored goal, never replacing it wholesale.
	requireGoalInt(t, "protein", g.Protein, 150)
	requireGoalInt(t, "calories", g.Calories, 2400)
	if g.Fat != nil || g.Sugar != nil {
		t.Errorf("fat/sugar = %v/%v, want both unset", g.Fat, g.Sugar)
	}
}
