package handler

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"schautrack/internal/service"
)

type sharedViewsResponse struct {
	SharedViews []struct {
		UserID        int         `json:"userId"`
		IsSelf        bool        `json:"isSelf"`
		DailyGoal     *int        `json:"dailyGoal"`
		GoalThreshold *int        `json:"goalThreshold"`
		DailyStats    []dailyStat `json:"dailyStats"`
	} `json:"sharedViews"`
}

type sharedView struct {
	dots          map[string]string
	goal          *int
	goalThreshold *int
}

func (r sharedViewsResponse) viewOf(t *testing.T, userID int) sharedView {
	t.Helper()
	for _, v := range r.SharedViews {
		if v.UserID == userID {
			out := sharedView{dots: map[string]string{}, goal: v.DailyGoal, goalThreshold: v.GoalThreshold}
			for _, s := range v.DailyStats {
				out.dots[s.Date] = s.Status
			}
			return out
		}
	}
	t.Fatalf("no shared view for user %d", userID)
	return sharedView{}
}

// A friend's adjustments are nutrition data: with nutrition shared the viewer
// sees the friend's dots and day goal against the friend's Tagesziel; without
// it neither the dots nor any goal is exposed.
func TestFriendTageszielFollowsTheNutritionShare(t *testing.T) {
	e := newV1Env(t)
	friendID, _ := e.seedUser("-friend")
	if _, err := e.Pool.Exec(e.Ctx,
		`UPDATE users SET macro_goals = '{"calories":2400}'::jsonb, macros_enabled = '{"calories":true}'::jsonb WHERE id = $1`, friendID); err != nil {
		t.Fatal(err)
	}
	// The viewer's own Basisziel differs, to prove it is never applied to the friend.
	seedBasisziel(t, e, `{"calories":1000}`, `{"calories":true}`)

	day := daysAgo(1)
	e.adjustFor(friendID, day, `{"calories":3100,"reason":"Lauf 25 km"}`)
	e.eat(friendID, day, 3000)

	friendDay := "/entries/day?date=" + day + "&user=" + strconv.Itoa(friendID)

	t.Run("nutrition shared", func(t *testing.T) {
		e.link(friendID, e.UserID, map[string]bool{service.ShareNutrition: true}, noShares())

		view := decodeRec[sharedViewsResponse](t, e.sessionGet(e.UserID, "/api/dashboard?range=7")).viewOf(t, friendID)
		if s := view.dots[day]; s != "under" {
			t.Errorf("friend dot = %q, want under (3000 kcal on the friend's 3100 kcal day)", s)
		}
		if view.goal == nil || view.goalThreshold == nil {
			t.Errorf("shared friend goal/threshold = %v/%v, want both exposed", view.goal, view.goalThreshold)
		}

		g := decodeRec[dayResponse](t, e.sessionGet(e.UserID, friendDay)).Goal
		if g == nil || !g.Adjusted {
			t.Fatalf("friend's day goal = %+v, want the adjusted goal", g)
		}
		requireGoalInt(t, "friend calories", g.Calories, 3100)
		requireGoalInt(t, "friend baseCalories", g.BaseCalories, 2400)
	})

	if _, err := e.Pool.Exec(e.Ctx, `DELETE FROM account_links WHERE requester_id = $1`, friendID); err != nil {
		t.Fatal(err)
	}

	t.Run("nutrition not shared", func(t *testing.T) {
		e.link(friendID, e.UserID, map[string]bool{service.ShareNutrition: false, service.ShareWeight: true}, noShares())

		view := decodeRec[sharedViewsResponse](t, e.sessionGet(e.UserID, "/api/dashboard?range=7")).viewOf(t, friendID)
		if len(view.dots) != 0 || view.goal != nil || view.goalThreshold != nil {
			t.Errorf("unshared friend exposes dots=%v goal=%v threshold=%v", view.dots, view.goal, view.goalThreshold)
		}

		rec := e.sessionGet(e.UserID, friendDay)
		if rec.Code != http.StatusForbidden {
			t.Errorf("/entries/day without the nutrition share = %d, want 403", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "goal") || strings.Contains(rec.Body.String(), "3100") {
			t.Errorf("forbidden response leaks goal data: %s", rec.Body.String())
		}
	})
}
