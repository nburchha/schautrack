package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"schautrack/internal/apierr"
	"schautrack/internal/model"
)

const (
	maxComponentsPerEntry = 50
	maxComponentGrams     = 10000.0
	// minComponentGrams is the smallest value NUMERIC(8,2) can hold; anything
	// below rounds to 0 and would trip the grams > 0 CHECK.
	minComponentGrams = 0.01
)

// v1Component is one ingredient of an entry: a snapshot of a food, in grams.
// FoodID is null for an ad-hoc component, and after its catalog food is
// deleted. The per-100 g values never change after capture.
type v1Component struct {
	ID       int          `json:"id"`
	FoodID   *int         `json:"food_id"`
	Name     string       `json:"name"`
	Grams    float64      `json:"grams"`
	Calories float64      `json:"calories_per_100g"`
	Macros   v1FoodMacros `json:"macros_per_100g"`
}

const componentSelect = `id, food_id, name, grams::float8, kcal_100g::float8,
	protein_100g::float8, carbs_100g::float8, fat_100g::float8, fiber_100g::float8, sugar_100g::float8`

func scanComponent(row pgx.Row) (*v1Component, error) {
	var c v1Component
	if err := row.Scan(&c.ID, &c.FoodID, &c.Name, &c.Grams, &c.Calories,
		&c.Macros.ProteinG, &c.Macros.CarbsG, &c.Macros.FatG,
		&c.Macros.FiberG, &c.Macros.SugarG); err != nil {
		return nil, err
	}
	return &c, nil
}

// querier is what pgxpool.Pool and pgx.Tx have in common.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// attachComponents fills Components on every entry with one query.
func attachComponents(ctx context.Context, q querier, entries []*v1Entry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]int, len(entries))
	byID := make(map[int]*v1Entry, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
		byID[e.ID] = e
	}
	rows, err := q.Query(ctx,
		"SELECT entry_id, "+componentSelect+" FROM entry_components WHERE entry_id = ANY($1) ORDER BY id", ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entryID int
		var c v1Component
		if err := rows.Scan(&entryID, &c.ID, &c.FoodID, &c.Name, &c.Grams, &c.Calories,
			&c.Macros.ProteinG, &c.Macros.CarbsG, &c.Macros.FatG,
			&c.Macros.FiberG, &c.Macros.SugarG); err != nil {
			return err
		}
		byID[entryID].Components = append(byID[entryID].Components, c)
	}
	return rows.Err()
}

// v1ComponentInput is either a catalog reference (food_id + grams) or an
// ad-hoc component (name + grams + calories_per_100g, macros optional). For a
// catalog food the server copies the values, so a client never sends them.
type v1ComponentInput struct {
	FoodID   *int     `json:"food_id"`
	Name     *string  `json:"name"`
	Grams    *float64 `json:"grams"`
	Calories *float64 `json:"calories_per_100g"`
	ProteinG *float64 `json:"protein_g"`
	CarbsG   *float64 `json:"carbs_g"`
	FatG     *float64 `json:"fat_g"`
	FiberG   *float64 `json:"fiber_g"`
	SugarG   *float64 `json:"sugar_g"`
}

// resolvedComponent is a component ready to insert.
type resolvedComponent struct {
	foodID                     *int
	name                       string
	grams                      float64
	kcal                       float64
	protein, carbs, fat, fiber *float64
	sugar                      *float64
}

func invalid(prefix, field, reason string) *apierr.Problem {
	return apierr.Unprocessable("The component is not valid.",
		apierr.InvalidParam{Name: prefix + field, Reason: reason})
}

func validGrams(g *float64) bool {
	return g != nil && *g >= minComponentGrams && *g <= maxComponentGrams
}

// resolveComponent validates one input and, for a catalog food, snapshots the
// food's values. prefix names the field in errors, e.g. "components[2].".
func resolveComponent(ctx context.Context, q querier, userID int, in v1ComponentInput, prefix string) (*resolvedComponent, *apierr.Problem) {
	if !validGrams(in.Grams) {
		return nil, invalid(prefix, "grams", fmt.Sprintf("required, between %g and %g", minComponentGrams, maxComponentGrams))
	}
	rc := &resolvedComponent{grams: *in.Grams}

	if in.FoodID != nil {
		if in.Name != nil || in.Calories != nil || in.ProteinG != nil || in.CarbsG != nil ||
			in.FatG != nil || in.FiberG != nil || in.SugarG != nil {
			return nil, invalid(prefix, "food_id", "send either food_id or ad-hoc values, not both")
		}
		var kcal *float64
		err := q.QueryRow(ctx, `
			SELECT name, kcal_100g::float8, protein_100g::float8, carbs_100g::float8,
			       fat_100g::float8, fiber_100g::float8, sugar_100g::float8
			FROM foods WHERE id = $1 AND (user_id IS NULL OR user_id = $2)`,
			*in.FoodID, userID).Scan(&rc.name, &kcal, &rc.protein, &rc.carbs, &rc.fat, &rc.fiber, &rc.sugar)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, invalid(prefix, "food_id", "no such food")
			}
			return nil, dbFail("load food", err)
		}
		if kcal == nil {
			// A food without kcal cannot be a component (architecture.md).
			return nil, invalid(prefix, "food_id", "this food has no calorie value and cannot be used as a component")
		}
		rc.kcal, rc.foodID = *kcal, in.FoodID
		return rc, nil
	}

	name := ""
	if in.Name != nil {
		name = truncateUTF8(strings.TrimSpace(*in.Name), maxFoodName)
	}
	if name == "" {
		return nil, invalid(prefix, "name", "required without food_id")
	}
	if in.Calories == nil {
		return nil, invalid(prefix, "calories_per_100g", "required without food_id")
	}
	var bad []apierr.InvalidParam
	check := func(field string, v *float64, max float64) {
		if v != nil && (*v < 0 || *v > max) {
			bad = append(bad, apierr.InvalidParam{
				Name: prefix + field, Reason: fmt.Sprintf("must be between 0 and %g", max)})
		}
	}
	check("calories_per_100g", in.Calories, maxCatalogKcal)
	check("protein_g", in.ProteinG, maxCatalogMacro)
	check("carbs_g", in.CarbsG, maxCatalogMacro)
	check("fat_g", in.FatG, maxCatalogMacro)
	check("fiber_g", in.FiberG, maxCatalogMacro)
	check("sugar_g", in.SugarG, maxCatalogMacro)
	if bad != nil {
		return nil, apierr.Unprocessable("One or more values per 100 g are out of range.", bad...)
	}
	rc.name, rc.kcal = name, *in.Calories
	rc.protein, rc.carbs, rc.fat, rc.fiber, rc.sugar = in.ProteinG, in.CarbsG, in.FatG, in.FiberG, in.SugarG
	return rc, nil
}

func insertComponent(ctx context.Context, q querier, entryID int, c *resolvedComponent) error {
	_, err := q.Exec(ctx, `
		INSERT INTO entry_components
			(entry_id, food_id, name, grams, kcal_100g, protein_100g, carbs_100g, fat_100g, fiber_100g, sugar_100g)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entryID, c.foodID, c.name, c.grams, c.kcal, c.protein, c.carbs, c.fat, c.fiber, c.sugar)
	return err
}

// recomputeEntryTotals rewrites an entry's calories and macros from its
// components: the sum of grams/100 * value-per-100 g, rounded to whole units.
//
// Unknown values are skipped, not counted as 0. A macro no component knows
// stays NULL. With no components it does nothing, so removing the last
// component leaves the last computed totals in place as a direct entry.
func recomputeEntryTotals(ctx context.Context, q querier, entryID int) error {
	_, err := q.Exec(ctx, `
		UPDATE calorie_entries e SET
			amount    = ROUND(s.kcal)::int,
			protein_g = ROUND(s.protein)::int,
			carbs_g   = ROUND(s.carbs)::int,
			fat_g     = ROUND(s.fat)::int,
			fiber_g   = ROUND(s.fiber)::int,
			sugar_g   = ROUND(s.sugar)::int
		FROM (
			SELECT SUM(grams * kcal_100g / 100) AS kcal,
			       SUM(grams * protein_100g / 100) AS protein,
			       SUM(grams * carbs_100g / 100) AS carbs,
			       SUM(grams * fat_100g / 100) AS fat,
			       SUM(grams * fiber_100g / 100) AS fiber,
			       SUM(grams * sugar_100g / 100) AS sugar
			FROM entry_components WHERE entry_id = $1 HAVING COUNT(*) > 0
		) s
		WHERE e.id = $1`, entryID)
	return err
}

// totalsProblem maps a CHECK violation from recomputeEntryTotals to a 422: the
// components add up past the per-entry limits.
func totalsProblem(err error) *apierr.Problem {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" {
		return apierr.Unprocessable(
			fmt.Sprintf("The components add up past the per-entry limits (%d kcal, %d g per macro).",
				MaxEntryCalories, MaxEntryMacro),
			apierr.InvalidParam{Name: "grams", Reason: "total exceeds entry limits"})
	}
	return dbFail("recompute entry totals", err)
}

// finishEntryMutation recomputes totals, re-reads the entry with components
// and commits. Shared by every write that touches components.
func (h *V1Handler) finishEntryMutation(w http.ResponseWriter, r *http.Request, tx pgx.Tx, entryID, userID int, status int, location bool) {
	ctx := r.Context()
	if err := recomputeEntryTotals(ctx, tx, entryID); err != nil {
		apierr.Write(w, r, totalsProblem(err))
		return
	}
	e, err := scanEntry(tx.QueryRow(ctx,
		"SELECT "+entrySelect+" FROM calorie_entries WHERE id = $1 AND user_id = $2", entryID, userID), v1Tz(r))
	if err != nil {
		apierr.Write(w, r, dbFail("reload entry", err))
		return
	}
	if err := attachComponents(ctx, tx, []*v1Entry{e}); err != nil {
		apierr.Write(w, r, dbFail("load components", err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.Write(w, r, dbFail("commit entry", err))
		return
	}
	h.broadcastEntries(userID)
	if location {
		w.Header().Set("Location", fmt.Sprintf("/api/v1/entries/%d", e.ID))
	}
	writeV1(w, status, e)
}

// createEntryWithComponents is the components branch of POST /entries.
func (h *V1Handler) createEntryWithComponents(w http.ResponseWriter, r *http.Request,
	inputs []v1ComponentInput, date, name string, eatenAt *time.Time) {
	user := v1User(r)
	ctx := r.Context()

	if len(inputs) > maxComponentsPerEntry {
		apierr.Write(w, r, apierr.Unprocessable(
			fmt.Sprintf("An entry holds at most %d components.", maxComponentsPerEntry),
			apierr.InvalidParam{Name: "components", Reason: "too many"}))
		return
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		apierr.Write(w, r, dbFail("begin create entry", err))
		return
	}
	defer tx.Rollback(ctx)

	resolved := make([]*resolvedComponent, len(inputs))
	for i, in := range inputs {
		rc, prob := resolveComponent(ctx, tx, user.ID, in, fmt.Sprintf("components[%d].", i))
		if prob != nil {
			apierr.Write(w, r, prob)
			return
		}
		resolved[i] = rc
	}

	// amount is a placeholder until recomputeEntryTotals runs below.
	var entryID int
	if err := tx.QueryRow(ctx, `
		INSERT INTO calorie_entries (user_id, entry_date, amount, entry_name, eaten_at)
		VALUES ($1, $2, 0, $3, $4) RETURNING id`,
		user.ID, date, nilString(name), eatenAt).Scan(&entryID); err != nil {
		apierr.Write(w, r, dbFail("create entry", err))
		return
	}
	for _, rc := range resolved {
		if err := insertComponent(ctx, tx, entryID, rc); err != nil {
			apierr.Write(w, r, dbFail("create component", err))
			return
		}
	}
	h.finishEntryMutation(w, r, tx, entryID, user.ID, http.StatusCreated, true)
}

// lockEntry opens a transaction holding a row lock on the caller's entry, so
// concurrent component writes recompute one after the other.
func (h *V1Handler) lockEntry(w http.ResponseWriter, r *http.Request) (tx pgx.Tx, entryID int, ok bool) {
	entryID, prob := pathID(r)
	if prob != nil {
		apierr.Write(w, r, prob)
		return nil, 0, false
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		apierr.Write(w, r, dbFail("begin component write", err))
		return nil, 0, false
	}
	var locked int
	err = tx.QueryRow(r.Context(),
		"SELECT id FROM calorie_entries WHERE id = $1 AND user_id = $2 FOR UPDATE",
		entryID, v1User(r).ID).Scan(&locked)
	if err != nil {
		tx.Rollback(r.Context())
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.Write(w, r, apierr.NotFound("No entry with that id."))
		} else {
			apierr.Write(w, r, dbFail("lock entry", err))
		}
		return nil, 0, false
	}
	return tx, entryID, true
}

// AddComponentV1 handles POST /api/v1/entries/{id}/components. The entry's
// calories and macros become the sum of its components.
func (h *V1Handler) AddComponentV1(w http.ResponseWriter, r *http.Request) {
	var in v1ComponentInput
	if prob := decodeV1(w, r, &in); prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	tx, entryID, ok := h.lockEntry(w, r)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	ctx, user := r.Context(), v1User(r)

	rc, prob := resolveComponent(ctx, tx, user.ID, in, "")
	if prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	var count int
	if err := tx.QueryRow(ctx,
		"SELECT COUNT(*)::int FROM entry_components WHERE entry_id = $1", entryID).Scan(&count); err != nil {
		apierr.Write(w, r, dbFail("count components", err))
		return
	}
	if count >= maxComponentsPerEntry {
		apierr.Write(w, r, apierr.Conflict(
			fmt.Sprintf("An entry holds at most %d components.", maxComponentsPerEntry)))
		return
	}
	if err := insertComponent(ctx, tx, entryID, rc); err != nil {
		apierr.Write(w, r, dbFail("create component", err))
		return
	}
	h.finishEntryMutation(w, r, tx, entryID, user.ID, http.StatusCreated, false)
}

type v1ComponentPatch struct {
	Grams *float64 `json:"grams"`
}

// UpdateComponentV1 handles PATCH /api/v1/entries/{id}/components/{cid}. Only
// grams can change; the snapshot values stay as captured.
func (h *V1Handler) UpdateComponentV1(w http.ResponseWriter, r *http.Request) {
	cid, ok := model.ParseID(chi.URLParam(r, "cid"))
	if !ok {
		apierr.Write(w, r, apierr.BadRequest("The component id must be a positive integer."))
		return
	}
	var in v1ComponentPatch
	if prob := decodeV1(w, r, &in); prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	if !validGrams(in.Grams) {
		apierr.Write(w, r, invalid("", "grams", fmt.Sprintf("required, between %g and %g", minComponentGrams, maxComponentGrams)))
		return
	}
	tx, entryID, locked := h.lockEntry(w, r)
	if !locked {
		return
	}
	defer tx.Rollback(r.Context())

	tag, err := tx.Exec(r.Context(),
		"UPDATE entry_components SET grams = $1 WHERE id = $2 AND entry_id = $3", *in.Grams, cid, entryID)
	if err != nil {
		apierr.Write(w, r, dbFail("update component", err))
		return
	}
	if tag.RowsAffected() == 0 {
		apierr.Write(w, r, apierr.NotFound("No component with that id on this entry."))
		return
	}
	h.finishEntryMutation(w, r, tx, entryID, v1User(r).ID, http.StatusOK, false)
}

// DeleteComponentV1 handles DELETE /api/v1/entries/{id}/components/{cid} and
// returns the updated entry, since its totals change.
func (h *V1Handler) DeleteComponentV1(w http.ResponseWriter, r *http.Request) {
	cid, ok := model.ParseID(chi.URLParam(r, "cid"))
	if !ok {
		apierr.Write(w, r, apierr.BadRequest("The component id must be a positive integer."))
		return
	}
	tx, entryID, locked := h.lockEntry(w, r)
	if !locked {
		return
	}
	defer tx.Rollback(r.Context())

	tag, err := tx.Exec(r.Context(),
		"DELETE FROM entry_components WHERE id = $1 AND entry_id = $2", cid, entryID)
	if err != nil {
		apierr.Write(w, r, dbFail("delete component", err))
		return
	}
	if tag.RowsAffected() == 0 {
		apierr.Write(w, r, apierr.NotFound("No component with that id on this entry."))
		return
	}
	h.finishEntryMutation(w, r, tx, entryID, v1User(r).ID, http.StatusOK, false)
}
