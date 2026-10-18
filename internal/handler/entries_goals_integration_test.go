package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"schautrack/internal/config"
	"schautrack/internal/database"
	"schautrack/internal/middleware"
	"schautrack/internal/service"
	"schautrack/internal/sse"
)

// The session surface resolves goals per date (ADR 0007): day dots, the
// dashboard's today fields and GET /entries/day all use the Tagesziel of the
// date they describe, and a friend's goals are exposed only when nutrition is
// shared.

// sessionGet drives the real dashboard / day / overview handlers as userID,
// behind the same RequireLinkAuth gate main.go puts in front of them.
func (e *v1Env) sessionGet(userID int, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	h := &EntriesHandler{
		Pool: e.Pool, Broker: sse.NewBroker(e.Pool),
		Cfg: &config.Config{}, Settings: database.NewSettingsCache(e.Pool),
	}
	r := chi.NewRouter()
	r.Get("/api/dashboard", h.Dashboard)
	r.With(middleware.RequireLinkAuth(e.Pool, service.ShareNutrition)).Get("/entries/day", h.DayEntries)
	r.With(middleware.RequireLinkAuth(e.Pool, service.ShareNutrition)).Get("/overview", h.Overview)

	user, err := middleware.GetUserByID(e.Ctx, e.Pool, userID)
	if err != nil {
		e.t.Fatalf("loading user %d: %v", userID, err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(middleware.WithTestUser(e.Ctx, user))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (e *v1Env) eat(userID int, date string, kcal int) {
	e.t.Helper()
	if _, err := e.Pool.Exec(e.Ctx,
		`INSERT INTO calorie_entries (user_id, entry_date, amount) VALUES ($1, $2, $3)`,
		userID, date, kcal); err != nil {
		e.t.Fatalf("seeding entry: %v", err)
	}
}

// adjust sets a Tagesziel-Anpassung for the env's user through the public v1 API.
func (e *v1Env) adjust(date, body string) {
	e.t.Helper()
	e.adjustFor(e.UserID, date, body)
}

func (e *v1Env) adjustFor(userID int, date, body string) {
	e.t.Helper()
	rec := e.do(call{Method: http.MethodPut, Path: "/api/v1/goals/" + date,
		Token: e.tokenFor(userID, service.ScopeGoalsWrite), Body: body})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		e.t.Fatalf("adjusting %s: %d %s", date, rec.Code, rec.Body.String())
	}
}

func today() string { return time.Now().UTC().Format("2006-01-02") }
func daysAgo(n int) string {
	return service.SubtractDaysUTC(today(), n)
}

type dotsResponse struct {
	DailyStats []struct {
		Date   string `json:"date"`
		Status string `json:"status"`
	} `json:"dailyStats"`
}

func (d dotsResponse) status(t *testing.T, date string) string {
	t.Helper()
	for _, s := range d.DailyStats {
		if s.Date == date {
			return s.Status
		}
	}
	t.Fatalf("no dot for %s in %+v", date, d.DailyStats)
	return ""
}

func decodeRec[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return out
}

// Story 20/21: an adjusted past day is judged against its own Tagesziel, an
// unadjusted day against the Basisziel.
func TestDashboardDotsUseEachDatesTagesziel(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400}`, `{"calories":true}`)
	adjustedDay, plainDay := daysAgo(2), daysAgo(1)
	e.adjust(adjustedDay, `{"calories":3100,"reason":"Lauf 25 km"}`)
	e.eat(e.UserID, adjustedDay, 3000) // over the Basisziel by 25%, under the adjusted goal
	e.eat(e.UserID, plainDay, 3000)

	got := decodeRec[dotsResponse](t, e.sessionGet(e.UserID, "/api/dashboard?range=7"))

	if s := got.status(t, adjustedDay); s != "under" {
		t.Errorf("adjusted day dot = %q, want under (3000 kcal on a 3100 kcal day)", s)
	}
	if s := got.status(t, plainDay); s != "over_threshold" {
		t.Errorf("unadjusted day dot = %q, want over_threshold (3000 kcal against the 2400 kcal Basisziel)", s)
	}
}

// dayGoalBlock is the `goal` block of GET /entries/day. The keys are the
// contract the Today panel and the Android client consume.
type dayGoalBlock struct {
	Date            string            `json:"date"`
	Adjusted        bool              `json:"adjusted"`
	Reason          *string           `json:"reason"`
	Calories        *int              `json:"calories"`
	BaseCalories    *int              `json:"baseCalories"`
	MacroGoals      map[string]int    `json:"macroGoals"`
	EnabledMacros   []string          `json:"enabledMacros"`
	MacroModes      map[string]string `json:"macroModes"`
	GoalThreshold   int               `json:"goalThreshold"`
	CaloriesEnabled bool              `json:"caloriesEnabled"`
}

type dayResponse struct {
	OK   bool          `json:"ok"`
	Goal *dayGoalBlock `json:"goal"`
}

func TestDayEntriesReturnsTheDaysTagesziel(t *testing.T) {
	e := newV1Env(t)
	seedBasisziel(t, e, `{"calories":2400,"protein":150,"carbs":300,"carbs_mode":"target"}`,
		`{"calories":true,"protein":true,"carbs":true}`)
	adjusted, plain := daysAgo(1), daysAgo(2)
	e.adjust(adjusted, `{"calories":3100,"carbs":480,"reason":"Lauf 25 km"}`)

	t.Run("adjusted day", func(t *testing.T) {
		got := decodeRec[dayResponse](t, e.sessionGet(e.UserID, "/entries/day?date="+adjusted))
		g := got.Goal
		if g == nil {
			t.Fatal("no goal block")
		}
		if !g.Adjusted || g.Reason == nil || *g.Reason != "Lauf 25 km" {
			t.Errorf("adjusted/reason = %v/%v", g.Adjusted, g.Reason)
		}
		requireGoalInt(t, "calories", g.Calories, 3100)
		requireGoalInt(t, "baseCalories", g.BaseCalories, 2400)
		if g.MacroGoals["carbs"] != 480 || g.MacroGoals["protein"] != 150 {
			t.Errorf("macroGoals = %v, want carbs 480 and gap-filled protein 150", g.MacroGoals)
		}
		if g.Date != adjusted {
			t.Errorf("date = %q", g.Date)
		}
		if len(g.EnabledMacros) != 2 || g.MacroModes["carbs"] != "target" || g.GoalThreshold != 10 || !g.CaloriesEnabled {
			t.Errorf("settings = %v %v %d %v", g.EnabledMacros, g.MacroModes, g.GoalThreshold, g.CaloriesEnabled)
		}
	})

	t.Run("unadjusted day follows the Basisziel", func(t *testing.T) {
		g := decodeRec[dayResponse](t, e.sessionGet(e.UserID, "/entries/day?date="+plain)).Goal
		if g == nil {
			t.Fatal("no goal block")
		}
		if g.Adjusted || g.Reason != nil || g.BaseCalories != nil {
			t.Errorf("adjusted/reason/base = %v/%v/%v, want an unadjusted day", g.Adjusted, g.Reason, g.BaseCalories)
		}
		requireGoalInt(t, "calories", g.Calories, 2400)
		if g.MacroGoals["carbs"] != 300 {
			t.Errorf("macroGoals = %v", g.MacroGoals)
		}
	})
}
