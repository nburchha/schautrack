package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"schautrack/internal/middleware"
	"schautrack/internal/service"
	"schautrack/internal/session"
	"schautrack/internal/sse"
)

// GoalsHandler serves the session-authed goal routes the SPA's adjustment sheet
// uses (ADR 0007). They are not part of the OpenAPI document and never live
// under /api/v1; the v1 twin is V1Handler.PutGoalV1 and friends. Both call the
// same service functions, so validation exists once. Errors use the legacy
// {ok:false,error} envelope of the session surface.
type GoalsHandler struct {
	Pool   *pgxpool.Pool
	Broker *sse.Broker
}

// Mount registers the routes with their middleware. It is a method (rather than
// lines in main.go, like most session routes) so tests drive the real chain:
// login on all three, CSRF on the two writes, self only (none takes a user).
func (h *GoalsHandler) Mount(r chi.Router) {
	login := r.With(middleware.RequireLogin)
	write := r.With(middleware.RequireLogin, session.CsrfProtection)
	login.Get("/api/goals/recent", h.Recent)
	write.Put("/api/goals/{date}", h.Put)
	write.Post("/api/goals/{date}/reset", h.Reset)
}

func goalDate(r *http.Request) (string, bool) {
	date := strings.TrimSpace(chi.URLParam(r, "date"))
	return date, isValidDate(date)
}

// Put handles PUT /api/goals/{date} — replaces the day's adjustment and answers
// {ok:true, goal} with the resulting Tagesziel. The body is the thin v1 body.
func (h *GoalsHandler) Put(w http.ResponseWriter, r *http.Request) {
	date, ok := goalDate(r)
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid date")
		return
	}
	var in v1GoalInput
	if err := ReadJSON(r, &in); err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid request.")
		return
	}

	user := middleware.GetCurrentUser(r)
	g, _, err := service.SetDayGoal(r.Context(), h.Pool, user, date, service.GoalInput{
		GoalValues: service.GoalValues{
			Calories: in.Calories, Protein: in.Protein, Carbs: in.Carbs,
			Fat: in.Fat, Fiber: in.Fiber, Sugar: in.Sugar,
		},
		Reason: in.Reason,
	})
	var verr *service.GoalValidationError
	if errors.As(err, &verr) {
		ErrorJSON(w, http.StatusBadRequest, verr.Error())
		return
	}
	if err != nil {
		slog.Error("failed to set goal", "error", err)
		ErrorJSON(w, http.StatusInternalServerError, "Could not save the goal.")
		return
	}
	h.Broker.BroadcastEntryChange(user.ID)
	JSON(w, http.StatusOK, map[string]any{"ok": true, "goal": toV1Goal(g)})
}

// Reset handles POST /api/goals/{date}/reset — removes the adjustment, so the
// day falls back to the Basisziel. Idempotent: a day without an adjustment is
// answered the same way. Responds {ok:true, goal} with the Tagesziel now in
// force.
func (h *GoalsHandler) Reset(w http.ResponseWriter, r *http.Request) {
	date, ok := goalDate(r)
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid date")
		return
	}
	user := middleware.GetCurrentUser(r)
	if err := service.DeleteDayGoal(r.Context(), h.Pool, user.ID, date); err != nil {
		slog.Error("failed to reset goal", "error", err)
		ErrorJSON(w, http.StatusInternalServerError, "Could not reset the goal.")
		return
	}
	g, err := service.ResolveDayGoal(r.Context(), h.Pool, user, date)
	if err != nil {
		slog.Error("failed to resolve goal", "error", err)
		ErrorJSON(w, http.StatusInternalServerError, "Could not load the goal.")
		return
	}
	h.Broker.BroadcastEntryChange(user.ID)
	JSON(w, http.StatusOK, map[string]any{"ok": true, "goal": toV1Goal(g)})
}

// recentGoalJSON is one quick pick: the values and reason of an earlier
// adjustment and when it was last used. Values are null where no goal is set or
// the account does not track the macro.
type recentGoalJSON struct {
	Reason *string `json:"reason"`
	v1GoalValues
	UsedAt time.Time `json:"used_at"`
}

// Recent handles GET /api/goals/recent — the latest distinct adjustments, most
// recently used first, for the sheet's quick picks.
func (h *GoalsHandler) Recent(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetCurrentUser(r)
	recent, err := service.RecentGoals(r.Context(), h.Pool, user, service.RecentGoalLimit)
	if err != nil {
		slog.Error("failed to list recent goals", "error", err)
		ErrorJSON(w, http.StatusInternalServerError, "Could not load recent adjustments.")
		return
	}
	out := make([]recentGoalJSON, 0, len(recent))
	for _, g := range recent {
		out = append(out, recentGoalJSON{Reason: g.Reason, v1GoalValues: toV1GoalValues(g.GoalValues), UsedAt: g.UsedAt})
	}
	JSON(w, http.StatusOK, map[string]any{"ok": true, "recent": out})
}
