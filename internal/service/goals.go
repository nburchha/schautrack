package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"schautrack/internal/model"
)

// Tagesziel resolution and Tagesziel-Anpassung writes (ADR 0007).
//
// Vocabulary (CONTEXT.md): the Basisziel is the permanent goal in
// users.macro_goals; a Tagesziel-Anpassung is a complete snapshot of the goal
// for one date in goal_adjustments; the Tagesziel of a date is its adjustment
// if one exists, otherwise the Basisziel.
//
// ResolveDayGoal is the single definition of "the goal on this date". Every
// consumer of a per-date goal must go through it (or a batch variant built on
// the same rules) so there is one place that decides.

// Bounds of a goal value. Calories mirror the Basisziel's calorie bound;
// macros allow 0, which means "no goal" exactly as in users.macro_goals. These
// are goal bounds, deliberately separate from the per-entry MaxEntryMacro: a
// day's carbohydrate target may exceed any single entry.
const (
	MaxGoalCalories  = 9999
	MaxGoalMacro     = 9999
	MaxGoalReasonLen = 120
)

// GoalValues is a goal as the API presents it: calories and the five macros,
// each nil when no goal is set. 0 is never presented; it is normalised to nil.
type GoalValues struct {
	Calories *int
	Protein  *int
	Carbs    *int
	Fat      *int
	Fiber    *int
	Sugar    *int
}

// DayGoal is the Tagesziel of one date.
type DayGoal struct {
	Date     string
	Adjusted bool
	// Reason is the adjustment's reason; nil when unadjusted or none given.
	Reason *string
	GoalValues
	// Base is the Basisziel the adjusted day differs from. Nil when the day is
	// not adjusted (the values above already are the Basisziel).
	Base *GoalValues
	// UpdatedAt is when the adjustment was last written; nil when unadjusted.
	UpdatedAt *time.Time
}

// GoalInput is the thin body of a write: nil means "not sent" and is filled
// from the current Basisziel. Reason is trimmed; empty becomes none.
type GoalInput struct {
	GoalValues
	Reason *string
}

// GoalValidationError names the offending field so a caller can report it
// (v1: invalid_params; session routes: error text). Field is empty when the
// problem concerns the request as a whole.
type GoalValidationError struct {
	Field  string
	Reason string
}

func (e *GoalValidationError) Error() string {
	if e.Field == "" {
		return e.Reason
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// goalKeys fixes the order of the six goal fields so loops over them stay in
// step with GoalValues.slots.
var goalKeys = append([]string{"calories"}, MacroKeys...)

func (v *GoalValues) slots() []**int {
	return []**int{&v.Calories, &v.Protein, &v.Carbs, &v.Fat, &v.Fiber, &v.Sugar}
}

func caloriesVisible(u MacroUser) bool {
	v, ok := u.MacrosEnabled["calories"]
	return !ok || v != false
}

// baseGoalValues reads the raw Basisziel: every key, enabled or not, with 0
// (and absent) as nil. Gap-fill copies these so a macro enabled later still has
// the value it had when the day was adjusted.
func baseGoalValues(u MacroUser) GoalValues {
	var v GoalValues
	if c := GetCalorieGoal(u); c != nil && *c > 0 {
		n := *c
		v.Calories = &n
	}
	for i, key := range MacroKeys {
		if n := getGoalValue(u.MacroGoals, key); n > 0 {
			*v.slots()[i+1] = &n
		}
	}
	return v
}

// BaseGoal is the raw Basisziel of user: every key, enabled or not. Unlike
// ResolveDayGoal it is not filtered by the macros the account tracks, because
// its consumers (GET /me) are the ones who write the Basisziel and must see
// what a later PATCH would merge into.
func BaseGoal(user *model.User) GoalValues {
	return baseGoalValues(userMacros(user))
}

// visibleTo hides what the account does not track. Applied at read time, never
// at write time, so toggling a macro in Settings does not destroy history.
func (v GoalValues) visibleTo(u MacroUser) GoalValues {
	if !caloriesVisible(u) {
		v.Calories = nil
	}
	enabled := map[string]bool{}
	for _, k := range GetEnabledMacros(u) {
		enabled[k] = true
	}
	for i, key := range MacroKeys {
		if !enabled[key] {
			*v.slots()[i+1] = nil
		}
	}
	return v
}

// normalised turns stored zeros into nil: 0 means "no goal", as in the Basisziel.
func (v GoalValues) normalised() GoalValues {
	for _, p := range v.slots() {
		if *p != nil && **p <= 0 {
			*p = nil
		}
	}
	return v
}

func userMacros(user *model.User) MacroUser {
	return ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold)
}

// MacroMap is the macro goals in the shape the status helpers take: only macros
// that have a goal. Visibility filtering has already happened, so for a
// resolved day this equals GetMacroGoals of the Basisziel.
func (v GoalValues) MacroMap() map[string]int {
	out := map[string]int{}
	for i, key := range MacroKeys {
		if p := *v.slots()[i+1]; p != nil {
			out[key] = *p
		}
	}
	return out
}

// storedAdjustment is one goal_adjustments row.
type storedAdjustment struct {
	GoalValues
	reason    *string
	updatedAt time.Time
}

// dayGoalFor is the single rule that turns an optional stored adjustment into a
// Tagesziel: the adjustment if present, otherwise the Basisziel. Both the
// single-date and the range resolver go through it.
func dayGoalFor(mu MacroUser, date string, adj *storedAdjustment) *DayGoal {
	base := baseGoalValues(mu).visibleTo(mu)
	if adj == nil {
		return &DayGoal{Date: date, GoalValues: base}
	}
	return &DayGoal{
		Date: date, Adjusted: true, Reason: adj.reason,
		GoalValues: adj.GoalValues.normalised().visibleTo(mu),
		Base:       &base,
		UpdatedAt:  &adj.updatedAt,
	}
}

const adjustmentColumns = `calories, protein, carbs, fat, fiber, sugar, reason, updated_at`

func (a *storedAdjustment) scan(row pgx.Row) error {
	return row.Scan(&a.Calories, &a.Protein, &a.Carbs, &a.Fat, &a.Fiber, &a.Sugar, &a.reason, &a.updatedAt)
}

// ResolveDayGoal returns the Tagesziel of user on date (YYYY-MM-DD): the
// stored adjustment if there is one, otherwise the Basisziel. Values are
// filtered by the macros enabled at read time. It never fails for want of a
// Basisziel; an account without one resolves to all-nil values.
func ResolveDayGoal(ctx context.Context, q Querier, user *model.User, date string) (*DayGoal, error) {
	mu := userMacros(user)
	var adj storedAdjustment
	err := adj.scan(q.QueryRow(ctx,
		`SELECT `+adjustmentColumns+` FROM goal_adjustments WHERE user_id = $1 AND goal_date = $2`,
		user.ID, date))
	if errors.Is(err, pgx.ErrNoRows) {
		return dayGoalFor(mu, date, nil), nil
	}
	if err != nil {
		return nil, err
	}
	return dayGoalFor(mu, date, &adj), nil
}

// RowsQuerier is the multi-row counterpart of Querier (pool or tx).
type RowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// DayGoals resolves the Tagesziel for every date of a range after one query.
// Build it with ResolveDayGoalRange.
type DayGoals struct {
	mu          MacroUser
	adjustments map[string]*storedAdjustment
}

// On returns the Tagesziel of date. Dates outside the loaded range resolve to
// the Basisziel, so callers must load the whole span they ask about.
func (g *DayGoals) On(date string) *DayGoal {
	return dayGoalFor(g.mu, date, g.adjustments[date])
}

// ValuesOn is On(date).GoalValues, the shape the day-status calculation takes.
func (g *DayGoals) ValuesOn(date string) GoalValues { return g.On(date).GoalValues }

// ResolveDayGoalRange loads the adjustments of userID within [oldest, newest]
// (inclusive, YYYY-MM-DD) in one query and returns a resolver for those dates.
// mu carries the Basisziel and the enabled macros; pass a linked user's
// MacroUser for a friend. Callers must only do this for data they may show:
// resolving a friend's goals is nutrition data.
func ResolveDayGoalRange(ctx context.Context, q RowsQuerier, userID int, mu MacroUser, oldest, newest string) (*DayGoals, error) {
	rows, err := q.Query(ctx, `
		SELECT goal_date, `+adjustmentColumns+`
		FROM goal_adjustments
		WHERE user_id = $1 AND goal_date BETWEEN $2 AND $3`, userID, oldest, newest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &DayGoals{mu: mu, adjustments: map[string]*storedAdjustment{}}
	for rows.Next() {
		var date string
		var a storedAdjustment
		if err := rows.Scan(&date, &a.Calories, &a.Protein, &a.Carbs, &a.Fat, &a.Fiber, &a.Sugar, &a.reason, &a.updatedAt); err != nil {
			return nil, err
		}
		out.adjustments[date] = &a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// validateGoalInput checks a write against the account's settings and returns
// the cleaned reason.
func validateGoalInput(mu MacroUser, in GoalInput) (*string, error) {
	enabled := map[string]bool{}
	for _, k := range GetEnabledMacros(mu) {
		enabled[k] = true
	}
	given := in.GoalValues
	sent := false
	for i, p := range given.slots() {
		if *p == nil {
			continue
		}
		sent = true
		key := goalKeys[i]
		lo, hi := 0, MaxGoalMacro
		if key == "calories" {
			lo, hi = 1, MaxGoalCalories
			if !caloriesVisible(mu) {
				return nil, &GoalValidationError{Field: key, Reason: "calories are hidden for this account"}
			}
		} else if !enabled[key] {
			return nil, &GoalValidationError{Field: key, Reason: "this macro is not enabled for the account"}
		}
		if n := **p; n < lo || n > hi {
			return nil, &GoalValidationError{Field: key, Reason: fmt.Sprintf("must be a whole number from %d to %d", lo, hi)}
		}
	}

	var reason *string
	if in.Reason != nil {
		r := strings.TrimSpace(*in.Reason)
		if utf8.RuneCountInString(r) > MaxGoalReasonLen {
			return nil, &GoalValidationError{Field: "reason", Reason: fmt.Sprintf("must be at most %d characters", MaxGoalReasonLen)}
		}
		if r != "" {
			reason = &r
		}
		sent = true
	}
	if !sent {
		return nil, &GoalValidationError{Reason: "send at least one of calories, protein, carbs, fat, fiber, sugar or reason"}
	}
	return reason, nil
}

// SetDayGoal replaces the adjustment of user on date and returns the resulting
// Tagesziel. created reports whether this was the date's first adjustment.
//
// Fields not sent in the input are filled from the user's current Basisziel,
// never from a previous adjustment, so the stored row is always a complete
// snapshot. A validation problem is returned as *GoalValidationError (use
// errors.As); anything else is a storage error.
//
// Repeating the same call yields the same state. Any date is allowed.
func SetDayGoal(ctx context.Context, pool *pgxpool.Pool, user *model.User, date string, in GoalInput) (goal *DayGoal, created bool, err error) {
	mu := userMacros(user)
	reason, err := validateGoalInput(mu, in)
	if err != nil {
		return nil, false, err
	}

	vals := baseGoalValues(mu)
	for i, p := range in.slots() {
		if *p != nil {
			*vals.slots()[i] = *p
		}
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO goal_adjustments (user_id, goal_date, calories, protein, carbs, fat, fiber, sugar, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, goal_date) DO UPDATE SET
			calories = EXCLUDED.calories, protein = EXCLUDED.protein, carbs = EXCLUDED.carbs,
			fat = EXCLUDED.fat, fiber = EXCLUDED.fiber, sugar = EXCLUDED.sugar,
			reason = EXCLUDED.reason, updated_at = NOW()
		RETURNING (xmax = 0)`,
		user.ID, date, vals.Calories, vals.Protein, vals.Carbs, vals.Fat, vals.Fiber, vals.Sugar, reason).Scan(&created)
	if err != nil {
		return nil, false, err
	}
	goal, err = ResolveDayGoal(ctx, pool, user, date)
	return goal, created, err
}

// DeleteDayGoal removes the adjustment of userID on date, restoring the
// Basisziel for that day. Deleting a day that has none is not an error.
func DeleteDayGoal(ctx context.Context, pool *pgxpool.Pool, userID int, date string) error {
	_, err := pool.Exec(ctx, `DELETE FROM goal_adjustments WHERE user_id = $1 AND goal_date = $2`, userID, date)
	return err
}
