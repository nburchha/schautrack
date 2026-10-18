package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"schautrack/internal/apierr"
	"schautrack/internal/middleware"
	"schautrack/internal/model"
	"schautrack/internal/service"
)

// The meal detail page reads and edits an entry's components through the
// session surface. /api/v1 stays bearer-only (CLAUDE.md invariant 1), so the
// SPA cannot reuse those routes; these handlers share the v1 SQL and the
// recompute rule instead, and answer in the legacy {"ok": ...} shape.

// entryDetail loads one entry with its components, with times rendered in tz.
// pgx.ErrNoRows means the entry does not exist or belongs to someone else.
func (h *EntriesHandler) entryDetail(r *http.Request, q querier, entryID, ownerID int, tz string) (*v1Entry, error) {
	e, err := scanEntry(q.QueryRow(r.Context(),
		"SELECT "+entrySelect+" FROM calorie_entries WHERE id = $1 AND user_id = $2", entryID, ownerID), tz)
	if err != nil {
		return nil, err
	}
	if err := attachComponents(r.Context(), q, []*v1Entry{e}); err != nil {
		return nil, err
	}
	return e, nil
}

// entryDetailPayload is the response shared by the read and the two writes.
func entryDetailPayload(e *v1Entry, mu service.MacroUser) map[string]any {
	caloriesEnabled := service.CaloriesEnabled(mu)
	return map[string]any{
		"ok":              true,
		"entry":           e,
		"enabledMacros":   service.GetEnabledMacros(mu),
		"caloriesEnabled": caloriesEnabled,
	}
}

// EntryDetail handles GET /api/entries/{id}. With ?user= it reads a linked
// account's entry (RequireLinkAuth has already checked the share), rendered in
// the creator's timezone like /entries/day.
func (h *EntriesHandler) EntryDetail(w http.ResponseWriter, r *http.Request) {
	entryID, ok := model.ParseID(chi.URLParam(r, "id"))
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid entry id")
		return
	}

	user := middleware.GetCurrentUser(r)
	target := middleware.GetTargetUser(r)
	if target == nil {
		target = user
	}
	tz := getUserTimezone(r, user)
	if target.ID != user.ID {
		tz = target.GetTimezone()
		if tz == "" {
			tz = "UTC"
		}
	}

	e, err := h.entryDetail(r, h.Pool, entryID, target.ID, tz)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ErrorJSON(w, http.StatusNotFound, "Entry not found")
			return
		}
		ErrorJSON(w, http.StatusInternalServerError, "Failed to load entry")
		return
	}
	mu := service.ParseMacroUser(target.MacrosEnabled, target.MacroGoals, target.DailyGoal, target.GoalThreshold)
	JSON(w, http.StatusOK, entryDetailPayload(e, mu))
}

// mutateComponent runs one component write for the caller's own entry: lock,
// apply, recompute the sum, answer with the fresh entry. apply reports how many
// rows it changed; zero is a 404 for the component.
func (h *EntriesHandler) mutateComponent(w http.ResponseWriter, r *http.Request,
	apply func(tx pgx.Tx, entryID, componentID int) (int64, error)) {
	entryID, ok := model.ParseID(chi.URLParam(r, "id"))
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid entry id")
		return
	}
	componentID, ok := model.ParseID(chi.URLParam(r, "cid"))
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid component id")
		return
	}
	user := middleware.GetCurrentUser(r)
	ctx := r.Context()

	tx, err := lockUserEntry(ctx, h.Pool, entryID, user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ErrorJSON(w, http.StatusNotFound, "Entry not found")
			return
		}
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update component")
		return
	}
	defer tx.Rollback(ctx)

	n, err := apply(tx, entryID, componentID)
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update component")
		return
	}
	if n == 0 {
		ErrorJSON(w, http.StatusNotFound, "Component not found")
		return
	}
	h.finishEntryWrite(w, r, tx, entryID, http.StatusOK)
}

// finishEntryWrite recomputes the entry's sum, commits and answers with the
// fresh entry detail. Shared by every session write that touches components.
func (h *EntriesHandler) finishEntryWrite(w http.ResponseWriter, r *http.Request, tx pgx.Tx, entryID, status int) {
	ctx := r.Context()
	user := middleware.GetCurrentUser(r)
	if err := recomputeEntryTotals(ctx, tx, entryID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			ErrorJSON(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("The components add up past the per-entry limits (%d kcal, %d g per macro).",
					MaxEntryCalories, MaxEntryMacro))
			return
		}
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update component")
		return
	}
	e, err := h.entryDetail(r, tx, entryID, user.ID, getUserTimezone(r, user))
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update component")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update component")
		return
	}
	h.Broker.BroadcastEntryChange(user.ID)
	mu := service.ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold)
	JSON(w, status, entryDetailPayload(e, mu))
}

// writeProblemLegacy answers a v1-style problem on the SPA surface, which uses
// the {"ok": false, "error": ...} shape. The first invalid parameter is spelled
// out, since the SPA shows the message as a toast.
func writeProblemLegacy(w http.ResponseWriter, p *apierr.Problem) {
	msg := p.Detail
	if len(p.InvalidParams) > 0 {
		msg = fmt.Sprintf("%s (%s: %s)", p.Detail, p.InvalidParams[0].Name, p.InvalidParams[0].Reason)
	}
	ErrorJSON(w, p.Status, msg)
}

// AddComponent handles POST /entries/{id}/components: one catalog food in
// grams, or an ad-hoc component with its own values per 100 g. The entry's sum
// becomes the sum of its components.
func (h *EntriesHandler) AddComponent(w http.ResponseWriter, r *http.Request) {
	entryID, ok := model.ParseID(chi.URLParam(r, "id"))
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid entry id")
		return
	}
	var in v1ComponentInput
	if err := ReadJSON(r, &in); err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	user := middleware.GetCurrentUser(r)
	ctx := r.Context()

	tx, err := lockUserEntry(ctx, h.Pool, entryID, user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ErrorJSON(w, http.StatusNotFound, "Entry not found")
			return
		}
		ErrorJSON(w, http.StatusInternalServerError, "Failed to add component")
		return
	}
	defer tx.Rollback(ctx)

	rc, prob := resolveComponent(ctx, tx, user.ID, in, "")
	if prob != nil {
		writeProblemLegacy(w, prob)
		return
	}
	var count int
	if err := tx.QueryRow(ctx,
		"SELECT COUNT(*)::int FROM entry_components WHERE entry_id = $1", entryID).Scan(&count); err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to add component")
		return
	}
	if count >= maxComponentsPerEntry {
		ErrorJSON(w, http.StatusConflict, fmt.Sprintf("An entry holds at most %d components.", maxComponentsPerEntry))
		return
	}
	if err := insertComponent(ctx, tx, entryID, rc); err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to add component")
		return
	}
	h.finishEntryWrite(w, r, tx, entryID, http.StatusCreated)
}

// SearchFoods handles GET /api/foods/search?q=: the catalog (global foods and
// the caller's own), ranked like GET /api/v1/foods/search.
func (h *EntriesHandler) SearchFoods(w http.ResponseWriter, r *http.Request) {
	q, prob := normalizeSearchQuery(r.URL.Query().Get("q"))
	if prob != nil {
		writeProblemLegacy(w, prob)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			ErrorJSON(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxSearchLimit)
	}
	foods, err := searchFoods(r.Context(), h.Pool, middleware.GetCurrentUser(r).ID, q, limit)
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to search foods")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"ok": true, "foods": foods})
}

// UpdateComponent handles POST /entries/{id}/components/{cid}/update. Only the
// grams change; the per-100 g snapshot stays as captured.
func (h *EntriesHandler) UpdateComponent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Grams *float64 `json:"grams"`
	}
	if err := ReadJSON(r, &body); err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !validGrams(body.Grams) {
		ErrorJSON(w, http.StatusBadRequest, "Grams must be between "+
			strconv.FormatFloat(minComponentGrams, 'f', -1, 64)+" and "+
			strconv.FormatFloat(maxComponentGrams, 'f', -1, 64))
		return
	}
	h.mutateComponent(w, r, func(tx pgx.Tx, entryID, componentID int) (int64, error) {
		tag, err := tx.Exec(r.Context(),
			"UPDATE entry_components SET grams = $1 WHERE id = $2 AND entry_id = $3", *body.Grams, componentID, entryID)
		return tag.RowsAffected(), err
	})
}

// DeleteComponent handles POST /entries/{id}/components/{cid}/delete. Deleting
// the last component leaves the entry's totals as they were: it becomes a
// direct entry (ADR 0002).
func (h *EntriesHandler) DeleteComponent(w http.ResponseWriter, r *http.Request) {
	h.mutateComponent(w, r, func(tx pgx.Tx, entryID, componentID int) (int64, error) {
		tag, err := tx.Exec(r.Context(),
			"DELETE FROM entry_components WHERE id = $1 AND entry_id = $2", componentID, entryID)
		return tag.RowsAffected(), err
	})
}
