package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"schautrack/internal/middleware"
	"schautrack/internal/session"
	"schautrack/internal/sse"
)

// Behaviour of the session-authed goal routes the SPA's adjustment sheet uses
// (ADR 0007): set and reset a day's Tagesziel-Anpassung, list recent ones for
// the quick picks. They share SetDayGoal / DeleteDayGoal with the v1 API, so
// these tests pin the session-surface contract (legacy {ok,error} envelope,
// login + CSRF gates, self only, SSE fan-out), not the validation rules again.

const goalsCSRF = "goals-test-csrf-token"

// goalsEnv drives GoalsHandler.Mount through the same middleware chain
// production uses. The injector stands in for session.Middleware + AttachUser:
// it loads the user from the database on every request (so a macro toggled
// mid-test is seen, as it would be in production) and attaches a session
// holding a known CSRF token.
type goalsEnv struct {
	*v1Env
	Broker *sse.Broker
	router http.Handler
}

type sessCall struct {
	Method string
	Path   string
	Body   string
	// UserID overrides the env's user; 0 means the env's own.
	UserID int
	// Anonymous sends the request without any session user.
	Anonymous bool
	// NoCSRF omits the X-CSRF-Token header.
	NoCSRF bool
}

func newGoalsEnv(t *testing.T) *goalsEnv {
	t.Helper()
	e := &goalsEnv{v1Env: newV1Env(t)}
	e.Broker = sse.NewBroker(e.Pool)
	h := &GoalsHandler{Pool: e.Pool, Broker: e.Broker}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-Test-Anonymous") == "" {
				id := e.UserID
				if v := req.Header.Get("X-Test-User"); v != "" {
					id = atoiOrFail(t, v)
				}
				u, err := middleware.GetUserByID(req.Context(), e.Pool, id)
				if err != nil {
					t.Fatalf("loading user %d: %v", id, err)
				}
				ctx := middleware.WithTestUser(req.Context(), u)
				sess := &session.Session{Data: map[string]any{}}
				sess.Set("csrfToken", goalsCSRF)
				ctx = session.WithTestSession(ctx, sess)
				req = req.WithContext(ctx)
			}
			next.ServeHTTP(w, req)
		})
	})
	h.Mount(r)
	e.router = r
	return e
}

func atoiOrFail(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("bad id %q: %v", s, err)
	}
	return n
}

func (e *goalsEnv) do(c sessCall) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(c.Method, c.Path, strings.NewReader(c.Body))
	req.Header.Set("Content-Type", "application/json")
	if c.Anonymous {
		req.Header.Set("X-Test-Anonymous", "1")
	}
	if c.UserID != 0 {
		req.Header.Set("X-Test-User", itoa(c.UserID))
	}
	if !c.NoCSRF {
		req.Header.Set("X-CSRF-Token", goalsCSRF)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req.WithContext(context.Background()))
	return rec
}

func itoa(n int) string { return strconv.Itoa(n) }

// goalResp is the success body shared by PUT and reset: {ok, goal}.
type goalResp struct {
	OK   bool   `json:"ok"`
	Goal v1Goal `json:"goal"`
}

type errResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func (e *goalsEnv) put(date, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(sessCall{Method: http.MethodPut, Path: "/api/goals/" + date, Body: body})
}

func (e *goalsEnv) reset(date string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(sessCall{Method: http.MethodPost, Path: "/api/goals/" + date + "/reset"})
}

// TestSessionGoalPutFillsGapsFromBasisziel is the headline story, through the
// session surface: send only what differs, receive a complete goal.
func TestSessionGoalPutFillsGapsFromBasisziel(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)

	rec := e.put("2026-08-05", `{"calories":3100,"carbs":480,"reason":"  Lauf 25 km "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got goalResp
	decodeJSON(t, rec, &got)
	if !got.OK {
		t.Errorf("ok = false in %s", rec.Body.String())
	}
	g := got.Goal
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

	// Replacing is a plain 200 with the new snapshot, filled from the Basisziel
	// again (not merged with the previous adjustment).
	rec = e.put("2026-08-05", `{"protein":200}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &got)
	requireGoalInt(t, "calories after replace", got.Goal.Calories, 2400)
	requireGoalInt(t, "protein after replace", got.Goal.Protein, 200)
	if got.Goal.Reason != nil {
		t.Errorf("reason after replace = %q, want none", *got.Goal.Reason)
	}
}

// TestSessionGoalResetRemovesTheAdjustment: reset is idempotent and answers
// with the Basisziel the day falls back to.
func TestSessionGoalResetRemovesTheAdjustment(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)

	if rec := e.put("2026-08-05", `{"calories":3100}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	for i := 0; i < 2; i++ { // second pass: nothing left to remove
		rec := e.reset("2026-08-05")
		if rec.Code != http.StatusOK {
			t.Fatalf("reset #%d: status = %d, want 200 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
		var got goalResp
		decodeJSON(t, rec, &got)
		if !got.OK || got.Goal.Adjusted || got.Goal.Base != nil || got.Goal.Reason != nil {
			t.Errorf("reset #%d: day still adjusted: %s", i+1, rec.Body.String())
		}
		requireGoalInt(t, "calories", got.Goal.Calories, 2400)
	}

	var n int
	if err := e.Pool.QueryRow(e.Ctx,
		`SELECT count(*) FROM goal_adjustments WHERE user_id = $1`, e.UserID).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows left = %d (err %v), want 0", n, err)
	}
}

// recentResp is the body of GET /api/goals/recent.
type recentResp struct {
	OK     bool `json:"ok"`
	Recent []struct {
		Reason   *string    `json:"reason"`
		Calories *int       `json:"calories"`
		Protein  *int       `json:"protein"`
		Carbs    *int       `json:"carbs"`
		Fat      *int       `json:"fat"`
		Fiber    *int       `json:"fiber"`
		Sugar    *int       `json:"sugar"`
		UsedAt   *time.Time `json:"used_at"`
	} `json:"recent"`
}

func (e *goalsEnv) recent(userID int) recentResp {
	e.t.Helper()
	rec := e.do(sessCall{Method: http.MethodGet, Path: "/api/goals/recent", UserID: userID})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("recent: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var r recentResp
	decodeJSON(e.t, rec, &r)
	if !r.OK {
		e.t.Fatalf("recent: ok = false in %s", rec.Body.String())
	}
	return r
}

// seedAdjustment writes an adjustment with an explicit updated_at, so ordering
// tests do not depend on wall-clock gaps between requests.
func (e *goalsEnv) seedAdjustment(date string, calories int, reason *string, ago time.Duration) {
	e.t.Helper()
	if _, err := e.Pool.Exec(e.Ctx, `
		INSERT INTO goal_adjustments (user_id, goal_date, calories, protein, carbs, fat, fiber, sugar, reason, updated_at)
		VALUES ($1, $2, $3, 150, 300, 70, 30, 50, $4, NOW() - $5::interval)`,
		e.UserID, date, calories, reason, ago.String()); err != nil {
		e.t.Fatalf("seeding adjustment %s: %v", date, err)
	}
}

func strp(s string) *string { return &s }

// TestSessionGoalRecentOrdersDedupesAndCaps: the quick-pick list is newest
// first, shows one entry per distinct (reason, values), and holds at most 8.
func TestSessionGoalRecentOrdersDedupesAndCaps(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)

	if r := e.recent(0); len(r.Recent) != 0 {
		t.Fatalf("fresh account: recent = %d entries, want 0", len(r.Recent))
	}

	// Oldest to newest. The 06-02 and 06-09 rows are the same pick used twice.
	e.seedAdjustment("2026-06-01", 3000, strp("Rest"), 50*time.Hour)
	e.seedAdjustment("2026-06-02", 3500, strp("Lauf 25 km"), 40*time.Hour)
	e.seedAdjustment("2026-06-09", 3500, strp("Lauf 25 km"), 10*time.Hour)
	e.seedAdjustment("2026-06-05", 2800, nil, 20*time.Hour)

	r := e.recent(0)
	if len(r.Recent) != 3 {
		t.Fatalf("recent = %d entries, want 3 distinct (body: %+v)", len(r.Recent), r.Recent)
	}
	wantCals := []int{3500, 2800, 3000}
	for i, w := range wantCals {
		requireGoalInt(t, "recent["+itoa(i)+"].calories", r.Recent[i].Calories, w)
	}
	if r.Recent[0].Reason == nil || *r.Recent[0].Reason != "Lauf 25 km" {
		t.Errorf("recent[0].reason = %v", r.Recent[0].Reason)
	}
	if r.Recent[1].Reason != nil {
		t.Errorf("recent[1].reason = %q, want none", *r.Recent[1].Reason)
	}
	requireGoalInt(t, "recent[0].carbs", r.Recent[0].Carbs, 300)
	if r.Recent[0].UsedAt == nil {
		t.Error("recent[0].used_at missing")
	}

	// Cap: 12 more distinct picks, newest first, only 8 come back.
	for i := 0; i < 12; i++ {
		e.seedAdjustment("2026-07-"+pad2(i+1), 4000+i, nil, time.Duration(12-i)*time.Minute)
	}
	r = e.recent(0)
	if len(r.Recent) != 8 {
		t.Fatalf("capped recent = %d entries, want 8", len(r.Recent))
	}
	requireGoalInt(t, "newest", r.Recent[0].Calories, 4011)
	requireGoalInt(t, "eighth", r.Recent[7].Calories, 4004)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// TestSessionGoalRecentHidesDisabledMacros: a quick pick pre-fills the sheet,
// which only has fields for enabled macros, so a macro switched off since the
// adjustment was written must not come back (and two picks that differ only in
// it collapse into one).
func TestSessionGoalRecentHidesDisabledMacros(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)
	e.seedAdjustment("2026-06-01", 3000, strp("Lauf"), 3*time.Hour)
	if _, err := e.Pool.Exec(e.Ctx,
		`INSERT INTO goal_adjustments (user_id, goal_date, calories, protein, carbs, fat, fiber, sugar, reason)
		 VALUES ($1, '2026-06-02', 3000, 150, 300, 70, 30, 99, 'Lauf')`, e.UserID); err != nil {
		t.Fatal(err)
	}
	seedBasisziel(t, e.v1Env, baseGoals, `{"calories":true,"protein":true,"carbs":true,"fat":true,"fiber":true,"sugar":false}`)

	r := e.recent(0)
	if len(r.Recent) != 1 {
		t.Fatalf("recent = %d entries, want the two Lauf picks collapsed to 1", len(r.Recent))
	}
	if r.Recent[0].Sugar != nil {
		t.Errorf("sugar = %d, want hidden", *r.Recent[0].Sugar)
	}
}

// TestSessionGoalValidationErrorsUseTheLegacyEnvelope: the same rules as v1,
// reported as {ok:false,error} with the offending field named in the text.
func TestSessionGoalValidationErrorsUseTheLegacyEnvelope(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals,
		`{"calories":true,"protein":true,"carbs":true,"fat":true,"fiber":true,"sugar":false}`)

	cases := []struct {
		name, date, body, errContains string
		status                        int
	}{
		{"disabled macro", "2026-08-05", `{"sugar":10}`, "sugar", http.StatusBadRequest},
		{"negative", "2026-08-05", `{"carbs":-1}`, "carbs", http.StatusBadRequest},
		{"out of range", "2026-08-05", `{"calories":100000}`, "calories", http.StatusBadRequest},
		{"zero calories", "2026-08-05", `{"calories":0}`, "calories", http.StatusBadRequest},
		{"reason too long", "2026-08-05", `{"reason":"` + strings.Repeat("x", 121) + `"}`, "reason", http.StatusBadRequest},
		{"empty body object", "2026-08-05", `{}`, "at least one", http.StatusBadRequest},
		{"not json", "2026-08-05", `nope`, "", http.StatusBadRequest},
		{"bad date", "2026-13-45", `{"calories":3000}`, "date", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.put(c.date, c.body)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, c.status, rec.Body.String())
			}
			var got errResp
			decodeJSON(t, rec, &got)
			if got.OK || got.Error == "" {
				t.Errorf("envelope = %s, want {ok:false,error:...}", rec.Body.String())
			}
			if !strings.Contains(strings.ToLower(got.Error), c.errContains) {
				t.Errorf("error = %q, want it to mention %q", got.Error, c.errContains)
			}
		})
	}

	if rec := e.reset("not-a-date"); rec.Code != http.StatusBadRequest {
		t.Errorf("reset with bad date: status = %d, want 400", rec.Code)
	}

	var n int
	if err := e.Pool.QueryRow(e.Ctx,
		`SELECT count(*) FROM goal_adjustments WHERE user_id = $1`, e.UserID).Scan(&n); err != nil || n != 0 {
		t.Errorf("rejected writes left %d rows (err %v)", n, err)
	}
}

// TestSessionGoalRoutesRefuseUnauthenticatedAndMissingCSRF: login on every
// route, a CSRF token on the writes (GET carries none).
func TestSessionGoalRoutesRefuseUnauthenticatedAndMissingCSRF(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)

	routes := []struct{ method, path, body string }{
		{http.MethodPut, "/api/goals/2026-08-05", `{"calories":3100}`},
		{http.MethodPost, "/api/goals/2026-08-05/reset", ""},
		{http.MethodGet, "/api/goals/recent", ""},
	}
	for _, rt := range routes {
		t.Run("anonymous "+rt.method+" "+rt.path, func(t *testing.T) {
			rec := e.do(sessCall{Method: rt.method, Path: rt.path, Body: rt.body, Anonymous: true})
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
			}
		})
		if rt.method == http.MethodGet {
			continue
		}
		t.Run("no csrf "+rt.method+" "+rt.path, func(t *testing.T) {
			rec := e.do(sessCall{Method: rt.method, Path: rt.path, Body: rt.body, NoCSRF: true})
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}

	var n int
	if err := e.Pool.QueryRow(e.Ctx,
		`SELECT count(*) FROM goal_adjustments WHERE user_id = $1`, e.UserID).Scan(&n); err != nil || n != 0 {
		t.Errorf("refused requests wrote %d rows (err %v)", n, err)
	}
}

// TestSessionGoalRoutesAreSelfOnly: the routes take no user parameter, and
// another account's adjustments neither show up in the quick picks nor get
// touched by a reset.
func TestSessionGoalRoutesAreSelfOnly(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)
	otherID, _ := e.seedUser("-other")
	if _, err := e.Pool.Exec(e.Ctx, `UPDATE users SET macro_goals = $2::jsonb, macros_enabled = $3::jsonb WHERE id = $1`,
		otherID, baseGoals, allMacrosOn); err != nil {
		t.Fatal(err)
	}

	// The other account adjusts the same date.
	if rec := e.do(sessCall{Method: http.MethodPut, Path: "/api/goals/2026-08-05", UserID: otherID,
		Body: `{"calories":5000,"reason":"secret"}`}); rec.Code != http.StatusOK {
		t.Fatalf("other PUT: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	if r := e.recent(0); len(r.Recent) != 0 {
		t.Errorf("own recent shows %d entries of someone else's", len(r.Recent))
	}
	if r := e.recent(otherID); len(r.Recent) != 1 {
		t.Errorf("other's own recent = %d entries, want 1", len(r.Recent))
	}

	// Our reset of the same date leaves their adjustment alone, and our own PUT
	// on it does not overwrite theirs.
	if rec := e.reset("2026-08-05"); rec.Code != http.StatusOK {
		t.Fatalf("reset: status = %d", rec.Code)
	}
	if rec := e.put("2026-08-05", `{"calories":2000}`); rec.Code != http.StatusOK {
		t.Fatalf("own PUT: status = %d", rec.Code)
	}
	var cal int
	if err := e.Pool.QueryRow(e.Ctx,
		`SELECT calories FROM goal_adjustments WHERE user_id = $1 AND goal_date = '2026-08-05'`, otherID).Scan(&cal); err != nil || cal != 5000 {
		t.Errorf("other account's calories = %d (err %v), want untouched 5000", cal, err)
	}
}

// TestSessionGoalWritesBroadcastAnEntryChange: open dashboards of the user (and
// linked accounts) revalidate when a goal is set or reset. Events travel via
// pg_notify, so the test runs the broker's real listener; it connects
// asynchronously, hence the trigger is repeated (both writes are idempotent)
// until the first event lands.
func TestSessionGoalWritesBroadcastAnEntryChange(t *testing.T) {
	e := newGoalsEnv(t)
	seedBasisziel(t, e.v1Env, baseGoals, allMacrosOn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Broker.Listen(ctx, os.Getenv("TEST_DATABASE_URL"))

	ch := e.Broker.Subscribe(e.UserID)
	defer e.Broker.Unsubscribe(e.UserID, ch)

	expectEvent := func(what string, trigger func()) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			trigger()
			select {
			case msg := <-ch:
				if !strings.Contains(string(msg), "entry-change") {
					t.Errorf("%s: event = %q, want entry-change", what, msg)
				}
				time.Sleep(200 * time.Millisecond)
				for len(ch) > 0 { // drain the retries' duplicates
					<-ch
				}
				return
			case <-time.After(300 * time.Millisecond):
			case <-deadline:
				t.Fatalf("%s: no entry-change event within 10s", what)
			}
		}
	}

	expectEvent("PUT", func() { e.put("2026-08-05", `{"calories":3100}`) })
	expectEvent("reset", func() { e.reset("2026-08-05") })
}
