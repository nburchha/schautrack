package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"schautrack/internal/model"
)

// RecentGoalLimit is how many quick picks the adjustment sheet shows.
const RecentGoalLimit = 8

// recentGoalScan bounds how many distinct stored snapshots are read before the
// account's enabled macros are applied (hiding a macro can make two distinct
// snapshots identical, so the cap has to be applied after, not in SQL).
const recentGoalScan = 100

// RecentGoal is a previously used Tagesziel-Anpassung offered as a quick pick:
// the values and reason, plus when it was last written.
type RecentGoal struct {
	GoalValues
	Reason *string
	UsedAt time.Time
}

// RecentGoals returns the user's latest distinct adjustments, most recently
// used first, at most limit. "Distinct" means the same reason and the same
// values (after hiding macros the account does not track); the date an
// adjustment was for does not matter, so reusing "Lauf 25 km" every week is one
// pick. Values are filtered by the macros enabled at read time, like
// ResolveDayGoal, because a pick pre-fills a sheet that only has those fields.
func RecentGoals(ctx context.Context, pool *pgxpool.Pool, user *model.User, limit int) ([]RecentGoal, error) {
	rows, err := pool.Query(ctx, `
		SELECT calories, protein, carbs, fat, fiber, sugar, reason, MAX(updated_at) AS used_at
		FROM goal_adjustments
		WHERE user_id = $1
		GROUP BY calories, protein, carbs, fat, fiber, sugar, reason
		ORDER BY used_at DESC
		LIMIT $2`, user.ID, recentGoalScan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mu := userMacros(user)
	type key struct {
		v      [6]int
		reason string
		has    bool
	}
	seen := map[key]bool{}
	out := []RecentGoal{}
	for rows.Next() {
		var g RecentGoal
		if err := rows.Scan(&g.Calories, &g.Protein, &g.Carbs, &g.Fat, &g.Fiber, &g.Sugar, &g.Reason, &g.UsedAt); err != nil {
			return nil, err
		}
		g.GoalValues = g.GoalValues.normalised().visibleTo(mu)

		k := key{}
		for i, slot := range g.Slots() {
			if *slot.Dst != nil {
				k.v[i] = **slot.Dst
			}
		}
		if g.Reason != nil {
			k.reason, k.has = *g.Reason, true
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, g)
		if len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}
