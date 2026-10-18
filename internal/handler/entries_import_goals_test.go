package handler

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"schautrack/internal/service"
)

// TestParseImportDataGoalAdjustments pins validation of the "goal_adjustments"
// array. Bad rows are skipped and counted rather than failing the import; each
// skip carries a documented reason code.
func TestParseImportDataGoalAdjustments(t *testing.T) {
	longReason := strings.Repeat("x", service.MaxGoalReasonLen+1)
	okReason := strings.Repeat("ü", service.MaxGoalReasonLen) // counted in runes, not bytes

	tests := []struct {
		name        string
		input       string
		wantKept    int
		wantSkipped int
		wantReasons []string // reason codes of the recorded skips, in order
		check       func(t *testing.T, a []importAdjustment)
	}{
		{
			name:     "valid full row",
			input:    `{"goal_adjustments":[{"date":"2026-05-01","calories":3100,"protein":150,"carbs":480,"fat":67,"fiber":35,"sugar":50,"reason":"Lauf 25 km"}]}`,
			wantKept: 1,
			check: func(t *testing.T, a []importAdjustment) {
				g := a[0]
				if g.date != "2026-05-01" {
					t.Errorf("date = %q", g.date)
				}
				for name, pair := range map[string][2]*int{
					"calories": {g.goal.Calories, intp(3100)}, "protein": {g.goal.Protein, intp(150)},
					"carbs": {g.goal.Carbs, intp(480)}, "fat": {g.goal.Fat, intp(67)},
					"fiber": {g.goal.Fiber, intp(35)}, "sugar": {g.goal.Sugar, intp(50)},
				} {
					if !eqIntPtr(pair[0], pair[1]) {
						t.Errorf("%s = %v, want %d", name, pair[0], *pair[1])
					}
				}
				if g.reason == nil || *g.reason != "Lauf 25 km" {
					t.Errorf("reason = %v", g.reason)
				}
			},
		},
		{
			name:     "absent and null values stay unset",
			input:    `{"goal_adjustments":[{"date":"2026-05-01","calories":2500,"protein":null}]}`,
			wantKept: 1,
			check: func(t *testing.T, a []importAdjustment) {
				g := a[0].goal
				if !eqIntPtr(g.Calories, intp(2500)) || g.Protein != nil || g.Carbs != nil || a[0].reason != nil {
					t.Errorf("unexpected row: %+v reason=%v", g, a[0].reason)
				}
			},
		},
		{
			name:     "zero macro is no goal",
			input:    `{"goal_adjustments":[{"date":"2026-05-01","calories":2500,"fiber":0}]}`,
			wantKept: 1,
			check: func(t *testing.T, a []importAdjustment) {
				if a[0].goal.Fiber != nil {
					t.Errorf("fiber = %v, want nil", *a[0].goal.Fiber)
				}
			},
		},
		{
			name:     "reason only is kept and trimmed",
			input:    `{"goal_adjustments":[{"date":"2026-05-01","reason":"  Ruhetag  "}]}`,
			wantKept: 1,
			check: func(t *testing.T, a []importAdjustment) {
				if a[0].reason == nil || *a[0].reason != "Ruhetag" {
					t.Errorf("reason = %v", a[0].reason)
				}
			},
		},
		{
			name:     "blank reason becomes none",
			input:    `{"goal_adjustments":[{"date":"2026-05-01","calories":2500,"reason":"   "}]}`,
			wantKept: 1,
			check: func(t *testing.T, a []importAdjustment) {
				if a[0].reason != nil {
					t.Errorf("reason = %q, want nil", *a[0].reason)
				}
			},
		},
		{
			name:     "bounds are inclusive",
			input:    fmt.Sprintf(`{"goal_adjustments":[{"date":"2026-05-01","calories":%d,"carbs":%d,"reason":%q}]}`, service.MaxGoalCalories, service.MaxGoalMacro, okReason),
			wantKept: 1,
		},
		{
			name:        "non-object row",
			input:       `{"goal_adjustments":["x",{"date":"2026-05-01","calories":2000}]}`,
			wantKept:    1,
			wantSkipped: 1,
			wantReasons: []string{"not_an_object"},
		},
		{
			name:        "invalid dates",
			input:       `{"goal_adjustments":[{"date":"nope","calories":2000},{"calories":2000},{"date":"2026-02-30","calories":2000}]}`,
			wantSkipped: 3,
			wantReasons: []string{"invalid_date", "invalid_date", "invalid_date"},
		},
		{
			name: "invalid values",
			input: `{"goal_adjustments":[` +
				`{"date":"2026-05-01","calories":0},` + // calories must be >= 1
				`{"date":"2026-05-02","calories":-5},` +
				`{"date":"2026-05-03","calories":10000},` +
				`{"date":"2026-05-04","carbs":10000},` +
				`{"date":"2026-05-05","protein":-1},` +
				`{"date":"2026-05-06","fat":12.5},` +
				`{"date":"2026-05-07","fiber":"30"},` +
				`{"date":"2026-05-08","calories":2000,"reason":5},` +
				fmt.Sprintf(`{"date":"2026-05-09","calories":2000,"reason":%q}`, longReason) +
				`]}`,
			wantSkipped: 9,
			wantReasons: []string{
				"invalid_value", "invalid_value", "invalid_value", "invalid_value", "invalid_value",
				"invalid_value", "invalid_value", "invalid_value", "invalid_value",
			},
		},
		{
			name:        "row without any value or reason",
			input:       `{"goal_adjustments":[{"date":"2026-05-01"},{"date":"2026-05-02","calories":null,"reason":""}]}`,
			wantSkipped: 2,
			wantReasons: []string{"no_values", "no_values"},
		},
		{
			name:        "duplicate date keeps the first",
			input:       `{"goal_adjustments":[{"date":"2026-05-01","calories":2000},{"date":"2026-05-01","calories":3000}]}`,
			wantKept:    1,
			wantSkipped: 1,
			wantReasons: []string{"duplicate_date"},
			check: func(t *testing.T, a []importAdjustment) {
				if !eqIntPtr(a[0].goal.Calories, intp(2000)) {
					t.Errorf("calories = %v, want the first row's 2000", a[0].goal.Calories)
				}
			},
		},
		{
			name:     "missing key and wrong type are ignored",
			input:    `{"goal_adjustments":"nope","entries":[{"date":"2026-05-01","amount":100}]}`,
			wantKept: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := parseImportData(mustParseImportJSON(t, tt.input))
			if len(d.adjustments) != tt.wantKept {
				t.Fatalf("kept = %d, want %d", len(d.adjustments), tt.wantKept)
			}
			if d.skippedAdjustments != tt.wantSkipped {
				t.Errorf("skippedAdjustments = %d, want %d", d.skippedAdjustments, tt.wantSkipped)
			}
			var reasons []string
			for _, s := range d.skipped {
				if s.Kind != "adjustment" {
					continue
				}
				reasons = append(reasons, s.Reason)
			}
			if strings.Join(reasons, ",") != strings.Join(tt.wantReasons, ",") {
				t.Errorf("skip reasons = %v, want %v", reasons, tt.wantReasons)
			}
			if tt.check != nil {
				tt.check(t, d.adjustments)
			}
		})
	}
}

func TestParseImportDataGoalAdjustmentsRowCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"goal_adjustments":[`)
	const total = 10000 + 3
	for i := 0; i < total; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		day := time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		fmt.Fprintf(&b, `{"date":%q,"calories":2000}`, day.Format("2006-01-02"))
	}
	b.WriteString(`]}`)

	d := parseImportData(mustParseImportJSON(t, b.String()))
	if len(d.adjustments) != 10000 {
		t.Fatalf("kept %d rows, want the cap of 10000", len(d.adjustments))
	}
	if d.skippedAdjustments != 3 {
		t.Errorf("skippedAdjustments = %d, want 3", d.skippedAdjustments)
	}
	hasLimit := false
	for _, s := range d.skipped {
		if s.Kind == "adjustment" && s.Reason == "row_limit" {
			hasLimit = true
		}
	}
	if !hasLimit {
		t.Errorf("rows past the cap were not reported as row_limit")
	}
}

func TestImportDataIsEmptyCountsAdjustments(t *testing.T) {
	only := parseImportData(mustParseImportJSON(t, `{"goal_adjustments":[{"date":"2026-05-01","calories":2000}]}`))
	if only.isEmpty() {
		t.Error("an adjustments-only file must not be empty")
	}
	bad := parseImportData(mustParseImportJSON(t, `{"goal_adjustments":[{"date":"nope","calories":2000}]}`))
	if !bad.isEmpty() {
		t.Error("a file whose only rows are invalid must stay empty")
	}
}
