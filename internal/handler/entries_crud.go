package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"schautrack/internal/middleware"
	"schautrack/internal/service"
)

// CreateEntry handles POST /entries
func (h *EntriesHandler) CreateEntry(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := ReadJSON(r, &body); err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid request.")
		return
	}

	user := middleware.GetCurrentUser(r)
	userTz := getUserTimezone(r, user)
	mu := service.ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold)

	// An absent or null amount reads as the empty string, which ParseAmount
	// rejects — the same outcome the old fmt.Sprintf("%v", nil) => "<nil>"
	// coercion reached, without the sentinel.
	rawAmount := ""
	if v, _ := optionalString(body, "amount"); v != nil {
		rawAmount = *v
	}
	amount := classifyAmount(rawAmount, MaxEntryCalories)

	entryDate, _ := body["entry_date"].(string)
	entryDate = strings.TrimSpace(entryDate)
	explicitDate := entryDate
	if entryDate == "" {
		entryDate = service.FormatDateInTz(time.Now(), userTz)
	}
	if !isValidDate(entryDate) {
		ErrorJSON(w, http.StatusBadRequest, "Invalid date")
		return
	}
	// eaten_at (RFC 3339) is when the food was eaten. Without it the entry
	// falls back to its creation time. It decides the day, so an explicit
	// entry_date must agree with it.
	var eatenAt *time.Time
	if v, _ := optionalString(body, "eaten_at"); v != nil && *v != "" {
		t, day, prob := parseEatenAt(*v, explicitDate, userTz)
		if prob != nil {
			writeProblemLegacy(w, prob)
			return
		}
		eatenAt, entryDate = &t, day
	}
	// Absent and null both mean "no name"; there is nothing to preserve on a
	// create, so the two collapse to a NULL entry_name.
	entryName, _ := optionalEntryName(body, "entry_name")

	if comps, ok := body["components"].([]any); ok && len(comps) > 0 {
		h.createEntryWithComponents(w, r, body, comps, entryDate, entryName, eatenAt)
		return
	}

	// Parse weight (frontend may send as string or number).
	//
	// An unparseable weight is a 400, not a shrug (#319). This used to leave
	// hasWeight false and carry on, so the request answered 200 having stored
	// nothing: the user typed a weight, saw success, and it was not there.
	// Every sibling field on this handler — calories, macros, body fat —
	// already rejects a value it cannot parse, so silently dropping this one
	// was also the odd case out.
	var weightVal float64
	hasWeight := false
	if wv, _ := optionalString(body, "weight"); wv != nil && *wv != "" {
		wr := service.ParseWeight(*wv)
		if !wr.Ok {
			ErrorJSON(w, http.StatusBadRequest,
				fmt.Sprintf("Weight must be a number greater than 0 and at most %g", service.MaxWeight))
			return
		}
		weightVal = wr.Value
		hasWeight = true
	}

	bodyFat, ok := parseBodyFatUpdate(body)
	if !ok {
		ErrorJSON(w, http.StatusBadRequest, "Invalid body fat")
		return
	}

	// Parse macros
	macroValues := map[string]int{}
	for _, key := range service.MacroKeys {
		fieldName := key + "_g"
		v, _ := optionalString(body, fieldName)
		if v == nil || *v == "" {
			continue
		}
		n, err := strconv.Atoi(*v)
		if err != nil || n < 0 || n > MaxEntryMacro {
			ErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Macro values must be between 0 and %d", MaxEntryMacro))
			return
		}
		macroValues[key] = n
	}
	hasMacroEntry := len(macroValues) > 0

	// Auto-calc calories
	if service.IsAutoCalcCalories(mu) && hasMacroEntry {
		p := macroValues["protein"]
		c := macroValues["carbs"]
		f := macroValues["fat"]
		if computed := service.ComputeCaloriesFromMacros(p, c, f); computed != nil && *computed > 0 {
			if !entryCaloriesInRange(*computed) {
				ErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Calories computed from macros exceed the maximum of %d", MaxEntryCalories))
				return
			}
			// A computed amount replaces whatever the request sent, so an
			// unparseable `amount` field is no longer grounds for a 400.
			amount = amountDecision{Value: *computed, HasCalorieEntry: true}
		}
	}

	if amount.Reject {
		ErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Calories must be between -%d and %d", MaxEntryCalories, MaxEntryCalories))
		return
	}

	// bodyFat.Set counts as content: a body-fat-only request is a real write
	// now that it is no longer discarded (#327).
	if !amount.HasCalorieEntry && !hasMacroEntry && !hasWeight && !bodyFat.Set {
		ErrorJSON(w, http.StatusBadRequest, "Invalid entry data")
		return
	}

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to save entry")
		return
	}
	defer tx.Rollback(r.Context())

	if amount.HasCalorieEntry || hasMacroEntry {
		entryAmount := 0
		if amount.HasCalorieEntry {
			entryAmount = amount.Value
		}
		// Build dynamic query
		cols := "user_id, entry_date, amount, entry_name"
		vals := "$1, $2, $3, $4"
		args := []any{user.ID, entryDate, entryAmount, entryName}
		idx := 5
		if eatenAt != nil {
			cols += ", eaten_at"
			vals += fmt.Sprintf(", $%d", idx)
			args = append(args, *eatenAt)
			idx++
		}
		for _, key := range service.MacroKeys {
			if v, ok := macroValues[key]; ok {
				cols += ", " + key + "_g"
				vals += fmt.Sprintf(", $%d", idx)
				args = append(args, v)
				idx++
			}
		}
		_, err := tx.Exec(r.Context(), fmt.Sprintf("INSERT INTO calorie_entries (%s) VALUES (%s)", cols, vals), args...)
		if err != nil {
			ErrorJSON(w, http.StatusInternalServerError, "Failed to save entry")
			return
		}
	}

	// A body fat with no weight is still a reading worth keeping (#327).
	//
	// This used to write only when hasWeight, so
	// POST /entries {"body_fat": "24.3"} answered 200 having stored nothing —
	// while an out-of-range body fat got a 400 from parseBodyFatUpdate above.
	// A validation error for a bad value and a silent success for a good one
	// is the worst pairing available.
	//
	// UpsertWeightEntryPartial is exactly the shape for this: KeepWeight
	// applies the body fat to an existing row for that date and reports
	// ErrNoWeightEntry when there is none, rather than inventing a weight to
	// hang the reading on. That mirrors what the weight endpoint already
	// answers for the same request.
	switch {
	case hasWeight:
		_, err := service.UpsertWeightEntry(r.Context(), tx, user.ID, entryDate, weightVal, bodyFat)
		if err != nil {
			ErrorJSON(w, http.StatusInternalServerError, "Failed to save weight")
			return
		}
	case bodyFat.Set:
		_, err := service.UpsertWeightEntryPartial(r.Context(), tx, user.ID, entryDate, service.KeepWeight, bodyFat)
		if errors.Is(err, service.ErrNoWeightEntry) {
			ErrorJSON(w, http.StatusBadRequest, "Log a weight for that date first")
			return
		}
		if err != nil {
			ErrorJSON(w, http.StatusInternalServerError, "Failed to save body fat")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to save entry")
		return
	}

	if amount.HasCalorieEntry || hasMacroEntry {
		h.Broker.BroadcastEntryChange(user.ID)
	}
	OkJSON(w)
}

// createEntryWithComponents is the components branch of POST /entries. The
// sum is computed from the components, so amount, macros and weight must be
// left out. The answer carries the new entry's id.
func (h *EntriesHandler) createEntryWithComponents(w http.ResponseWriter, r *http.Request,
	body map[string]any, comps []any, entryDate string, entryName *string, eatenAt *time.Time) {
	for _, key := range append([]string{"amount", "weight", "body_fat"}, macroFieldNames()...) {
		if v, _ := optionalString(body, key); v != nil && *v != "" {
			ErrorJSON(w, http.StatusBadRequest, "Calories and macros are computed from the components; leave them out.")
			return
		}
	}
	raw, err := json.Marshal(comps)
	var inputs []v1ComponentInput
	if err != nil || json.Unmarshal(raw, &inputs) != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid components")
		return
	}
	name := ""
	if entryName != nil {
		name = *entryName
	}

	user := middleware.GetCurrentUser(r)
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to save entry")
		return
	}
	defer tx.Rollback(ctx)

	entryID, prob := insertEntryWithComponents(ctx, tx, user.ID, inputs, entryDate, name, eatenAt)
	if prob != nil {
		writeProblemLegacy(w, prob)
		return
	}
	if err := recomputeEntryTotals(ctx, tx, entryID); err != nil {
		writeProblemLegacy(w, totalsProblem(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to save entry")
		return
	}
	h.Broker.BroadcastEntryChange(user.ID)
	JSON(w, http.StatusOK, map[string]any{"ok": true, "id": entryID})
}

func macroFieldNames() []string {
	out := make([]string, 0, len(service.MacroKeys))
	for _, key := range service.MacroKeys {
		out = append(out, key+"_g")
	}
	return out
}

// UpdateEntry handles POST /entries/:id/update
func (h *EntriesHandler) UpdateEntry(w http.ResponseWriter, r *http.Request) {
	entryID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid entry id")
		return
	}

	var body map[string]any
	if err := ReadJSON(r, &body); err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid request.")
		return
	}

	user := middleware.GetCurrentUser(r)
	mu := service.ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold)
	autoCalc := service.IsAutoCalcCalories(mu)
	userTz := getUserTimezone(r, user)

	updates, values, badRequest := buildEntryUpdates(body, autoCalc)
	if badRequest != "" {
		ErrorJSON(w, http.StatusBadRequest, badRequest)
		return
	}

	// eaten_at moves the entry to that moment, and to that day in the account's
	// time zone. An explicit null clears it: the entry falls back to its
	// creation time and keeps its day.
	if raw, present := body["eaten_at"]; present {
		if raw == nil {
			updates = append(updates, fmt.Sprintf("eaten_at = $%d", len(updates)+1))
			values = append(values, nil)
		} else {
			str, _ := raw.(string)
			t, day, prob := parseEatenAt(strings.TrimSpace(str), "", userTz)
			if prob != nil {
				writeProblemLegacy(w, prob)
				return
			}
			updates = append(updates, fmt.Sprintf("eaten_at = $%d", len(updates)+1))
			values = append(values, t)
			updates = append(updates, fmt.Sprintf("entry_date = $%d", len(updates)+1))
			values = append(values, day)
		}
	}

	if len(updates) == 0 {
		ErrorJSON(w, http.StatusBadRequest, "No updates provided")
		return
	}

	// buildEntryUpdates numbered its placeholders $1..$len(updates), so the id
	// and user_id predicates continue from there.
	idx := len(updates) + 1

	query := fmt.Sprintf(
		"UPDATE calorie_entries SET %s WHERE id = $%d AND user_id = $%d RETURNING id, entry_date, amount, entry_name, COALESCE(eaten_at, created_at), protein_g, carbs_g, fat_g, fiber_g, sugar_g",
		strings.Join(updates, ", "), idx, idx+1)
	values = append(values, entryID, user.ID)

	var id int
	var entryDate string
	var amount int
	var entryName *string
	var eatenAtTime time.Time
	var proteinG, carbsG, fatG, fiberG, sugarG *int

	// Run the field update and the auto-calc amount update in one
	// transaction so a rejected computed amount can't leave the entry
	// half-updated (name/macros committed, amount stale).
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update entry")
		return
	}
	defer tx.Rollback(r.Context())

	err = tx.QueryRow(r.Context(), query, values...).Scan(
		&id, &entryDate, &amount, &entryName, &eatenAtTime,
		&proteinG, &carbsG, &fatG, &fiberG, &sugarG)
	if err != nil {
		ErrorJSON(w, http.StatusNotFound, "Entry not found")
		return
	}

	// Auto-calc calories
	if autoCalc {
		p, c, f := intOrZero(proteinG), intOrZero(carbsG), intOrZero(fatG)
		if computed := service.ComputeCaloriesFromMacros(p, c, f); computed != nil {
			if !entryCaloriesInRange(*computed) {
				ErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Calories computed from macros exceed the maximum of %d", MaxEntryCalories))
				return
			}
			if _, err := tx.Exec(r.Context(), "UPDATE calorie_entries SET amount = $1 WHERE id = $2 AND user_id = $3", *computed, entryID, user.ID); err != nil {
				ErrorJSON(w, http.StatusInternalServerError, "Failed to update entry")
				return
			}
			amount = *computed
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to update entry")
		return
	}

	enabledMacros := service.GetEnabledMacros(mu)
	macros := buildMacroMap(enabledMacros, proteinG, carbsG, fatG, fiberG, sugarG)

	payload := map[string]any{
		"id": id, "date": entryDate, "amount": amount,
		"time": service.FormatTimeInTz(eatenAtTime, userTz),
		"name": entryName, "macros": macros,
	}

	h.Broker.BroadcastEntryChange(user.ID)
	JSON(w, http.StatusOK, map[string]any{"ok": true, "entry": payload})
}

// DeleteEntry handles POST /entries/:id/delete
func (h *EntriesHandler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	entryID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		ErrorJSON(w, http.StatusBadRequest, "Invalid entry id")
		return
	}
	user := middleware.GetCurrentUser(r)
	tag, err := h.Pool.Exec(r.Context(), "DELETE FROM calorie_entries WHERE id = $1 AND user_id = $2", entryID, user.ID)
	if err != nil {
		ErrorJSON(w, http.StatusInternalServerError, "Failed to delete entry")
		return
	}
	if tag.RowsAffected() == 0 {
		ErrorJSON(w, http.StatusNotFound, "Entry not found")
		return
	}
	h.Broker.BroadcastEntryChange(user.ID)
	OkJSON(w)
}
