package handler

import (
	"testing"
)

type dashboardGoalFields struct {
	DailyGoal     *int           `json:"dailyGoal"`
	GoalStatus    string         `json:"goalStatus"`
	GoalDelta     *int           `json:"goalDelta"`
	MacroGoals    map[string]int `json:"macroGoals"`
	CalorieStatus struct {
		StatusClass string `json:"statusClass"`
	} `json:"calorieStatus"`
	MacroStatuses map[string]struct {
		StatusClass string `json:"statusClass"`
	} `json:"macroStatuses"`
	User struct {
		DailyGoal  *int           `json:"dailyGoal"`
		MacroGoals map[string]any `json:"macroGoals"`
	} `json:"user"`
	DailyStats []struct {
		Date   string `json:"date"`
		Status string `json:"status"`
	} `json:"dailyStats"`
}

// The dashboard keeps its today-based fields but resolves them against
// today's Tagesziel; the Basisziel stays visible under user.*.
func TestDashboardTodayFieldsUseTodaysTagesziel(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400,"protein":150}`, `{"calories":true,"protein":true}`)
	e.adjust(today(), `{"calories":3100,"protein":200}`)
	e.eat(e.UserID, today(), 3000)

	got := decodeRec[dashboardGoalFields](t, e.sessionGet(e.UserID, "/api/dashboard?range=7"))

	requireGoalInt(t, "dailyGoal", got.DailyGoal, 3100)
	requireGoalInt(t, "goalDelta", got.GoalDelta, 100)
	if got.GoalStatus != "under" {
		t.Errorf("goalStatus = %q, want under (3000 of 3100 kcal)", got.GoalStatus)
	}
	if got.CalorieStatus.StatusClass != "macro-stat--success" {
		t.Errorf("calorieStatus = %q, want success", got.CalorieStatus.StatusClass)
	}
	if got.MacroGoals["protein"] != 200 {
		t.Errorf("macroGoals = %v, want today's protein 200", got.MacroGoals)
	}

	if got.User.MacroGoals["calories"] != float64(2400) || got.User.MacroGoals["protein"] != float64(150) {
		t.Errorf("user.macroGoals = %v, want the unchanged Basisziel", got.User.MacroGoals)
	}
}

func TestDashboardTodayStatusIsOverAgainstBasiszielWithoutAdjustment(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400}`, `{"calories":true}`)
	e.eat(e.UserID, today(), 3000)

	got := decodeRec[dashboardGoalFields](t, e.sessionGet(e.UserID, "/api/dashboard?range=7"))

	requireGoalInt(t, "dailyGoal", got.DailyGoal, 2400)
	if got.GoalStatus != "over_threshold" {
		t.Errorf("goalStatus = %q, want over_threshold", got.GoalStatus)
	}
}

// Today's Tagesziel is used even when the requested range ends before today.
func TestDashboardTodayGoalIsResolvedOutsideTheRange(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400}`, `{"calories":true}`)
	e.adjust(today(), `{"calories":3100}`)

	end := daysAgo(3)
	got := decodeRec[dashboardGoalFields](t, e.sessionGet(e.UserID, "/api/dashboard?start="+daysAgo(9)+"&end="+end))

	requireGoalInt(t, "dailyGoal", got.DailyGoal, 3100)
}

// Overview (not used by the SPA, but part of the surface) follows the same rules.
func TestOverviewUsesEachDatesTagesziel(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400}`, `{"calories":true}`)
	adjustedDay, plainDay := daysAgo(2), daysAgo(1)
	e.adjust(adjustedDay, `{"calories":3100}`)
	e.eat(e.UserID, adjustedDay, 3000)
	e.eat(e.UserID, plainDay, 3000)
	e.adjust(today(), `{"calories":2000}`)

	got := decodeRec[dashboardGoalFields](t, e.sessionGet(e.UserID, "/overview?range=7"))

	dots := map[string]string{}
	for _, s := range got.DailyStats {
		dots[s.Date] = s.Status
	}
	if dots[adjustedDay] != "under" || dots[plainDay] != "over_threshold" {
		t.Errorf("dots = %v, want adjusted day under and plain day over_threshold", dots)
	}
	requireGoalInt(t, "dailyGoal", got.DailyGoal, 2000)
}

// Accounts without a Basisziel, or with macros switched off, keep working.
func TestGoalResolutionCopesWithMissingOrDisabledGoals(t *testing.T) {
	t.Run("no Basisziel at all", func(t *testing.T) {
		e := newV1Env(t)
		e.eat(e.UserID, daysAgo(1), 1800)

		dash := decodeRec[dashboardGoalFields](t, e.sessionGet(e.UserID, "/api/dashboard?range=7"))
		if dash.DailyGoal != nil || dash.GoalStatus != "unset" {
			t.Errorf("dailyGoal/goalStatus = %v/%q, want nil/unset", dash.DailyGoal, dash.GoalStatus)
		}
		for _, s := range dash.DailyStats {
			if s.Status != "none" {
				t.Errorf("dot for %s = %q, want none", s.Date, s.Status)
			}
		}

		g := decodeRec[dayResponse](t, e.sessionGet(e.UserID, "/entries/day?date="+daysAgo(1))).Goal
		if g == nil || g.Calories != nil || g.Adjusted || len(g.MacroGoals) != 0 || len(g.EnabledMacros) != 0 {
			t.Errorf("goal block = %+v, want an empty unadjusted goal", g)
		}
	})

	t.Run("macros disabled, calories only", func(t *testing.T) {
		e := newV1Env(t)
		// protein has a Basisziel value but is switched off
		seedBasisziel(t, e, `{"calories":2400,"protein":150}`, `{"calories":true}`)
		e.adjust(daysAgo(1), `{"calories":3100}`)

		g := decodeRec[dayResponse](t, e.sessionGet(e.UserID, "/entries/day?date="+daysAgo(1))).Goal
		if g == nil {
			t.Fatal("no goal block")
		}
		requireGoalInt(t, "calories", g.Calories, 3100)
		if len(g.MacroGoals) != 0 || len(g.EnabledMacros) != 0 {
			t.Errorf("disabled macros leak: goals=%v enabled=%v", g.MacroGoals, g.EnabledMacros)
		}
	})
}
