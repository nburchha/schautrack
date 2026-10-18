package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"schautrack/internal/middleware"
	"schautrack/internal/model"
	"schautrack/internal/sse"
)

// Export and Import driven end to end through the real handlers against a real
// database. Skipped unless TEST_DATABASE_URL is set (see v1TestPool).

// adjRow is one goal_adjustments row as the tests compare it. Timestamps are
// deliberately absent: they are bookkeeping, not part of the backup format.
type adjRow struct {
	Date                                        string
	Calories, Protein, Carbs, Fat, Fiber, Sugar *int
	Reason                                      *string
}

func (e *v1Env) exportImportHandler() *EntriesHandler {
	return &EntriesHandler{Pool: e.Pool, Broker: sse.NewBroker(e.Pool)}
}

func (e *v1Env) testUser() *model.User {
	return &model.User{
		ID: e.UserID, Email: e.Email,
		MacrosEnabled: json.RawMessage(`{}`), MacroGoals: json.RawMessage(`{}`),
	}
}

func (e *v1Env) insertAdjustments(rows ...adjRow) {
	e.t.Helper()
	for _, r := range rows {
		if _, err := e.Pool.Exec(e.Ctx, `
			INSERT INTO goal_adjustments (user_id, goal_date, calories, protein, carbs, fat, fiber, sugar, reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			e.UserID, r.Date, r.Calories, r.Protein, r.Carbs, r.Fat, r.Fiber, r.Sugar, r.Reason); err != nil {
			e.t.Fatalf("seeding adjustment %s: %v", r.Date, err)
		}
	}
}

func (e *v1Env) adjustments() []adjRow {
	e.t.Helper()
	rows, err := e.Pool.Query(e.Ctx, `
		SELECT goal_date, calories, protein, carbs, fat, fiber, sugar, reason
		FROM goal_adjustments WHERE user_id = $1 ORDER BY goal_date`, e.UserID)
	if err != nil {
		e.t.Fatalf("reading adjustments: %v", err)
	}
	defer rows.Close()
	var out []adjRow
	for rows.Next() {
		var r adjRow
		if err := rows.Scan(&r.Date, &r.Calories, &r.Protein, &r.Carbs, &r.Fat, &r.Fiber, &r.Sugar, &r.Reason); err != nil {
			e.t.Fatalf("scanning adjustment: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func (e *v1Env) runExport() []byte {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings/export", nil)
	req = req.WithContext(middleware.WithTestUser(req.Context(), e.testUser()))
	rec := httptest.NewRecorder()
	e.exportImportHandler().Export(rec, req)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("export status = %d (%s)", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

type importResp struct {
	OK      bool   `json:"ok"`
	DryRun  bool   `json:"dry_run"`
	Message string `json:"message"`
	Skipped struct {
		Total int `json:"total"`
		Rows  []struct {
			Kind, Date, Reason string
			Index              int
		} `json:"rows"`
	} `json:"skipped"`
}

func (e *v1Env) runImport(file string, dryRun bool) (int, importResp) {
	e.t.Helper()
	fields := map[string]string{}
	if dryRun {
		fields["dry_run"] = "true"
	}
	req := buildImportRequestWithFields(e.t, file, fields)
	req = req.WithContext(middleware.WithTestUser(req.Context(), e.testUser()))
	rec := httptest.NewRecorder()
	e.exportImportHandler().Import(rec, req)
	var resp importResp
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func requireAdjustments(t *testing.T, got, want []adjRow) {
	t.Helper()
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if string(gj) != string(wj) {
		t.Errorf("adjustments differ\n got: %s\nwant: %s", gj, wj)
	}
}

var seedAdjustments = []adjRow{
	{Date: "2026-05-01", Calories: ptr(3100), Protein: ptr(150), Carbs: ptr(480), Fat: ptr(67), Fiber: ptr(35), Sugar: ptr(50), Reason: ptr("Lauf 25 km")},
	{Date: "2026-05-02", Calories: ptr(2400), Carbs: ptr(300)},                           // partial: other goals unset
	{Date: "2026-05-03", Calories: ptr(2000), Reason: ptr("Ärger mit \"Quotes\" und ü")}, // unicode and quoting
	{Date: "2026-05-04", Reason: ptr("nur Grund")},                                       // reason only
}

func TestExportImportRoundTripsGoalAdjustments(t *testing.T) {
	e := newV1Env(t)
	e.insertAdjustments(seedAdjustments...)

	exported := e.runExport()

	var doc struct {
		Adjustments []map[string]any `json:"goal_adjustments"`
	}
	if err := json.Unmarshal(exported, &doc); err != nil {
		t.Fatalf("export is not valid JSON: %v\n%s", err, exported)
	}
	if len(doc.Adjustments) != len(seedAdjustments) {
		t.Fatalf("exported %d adjustments, want %d", len(doc.Adjustments), len(seedAdjustments))
	}
	// Unset values are omitted, like a weight without body fat.
	for _, row := range doc.Adjustments {
		if row["date"] == "2026-05-02" {
			if _, has := row["protein"]; has {
				t.Errorf("unset protein exported: %v", row)
			}
			if _, has := row["reason"]; has {
				t.Errorf("absent reason exported: %v", row)
			}
		}
	}

	// Lose the data, then restore it from the file.
	if _, err := e.Pool.Exec(e.Ctx, `DELETE FROM goal_adjustments WHERE user_id = $1`, e.UserID); err != nil {
		t.Fatal(err)
	}
	code, resp := e.runImport(string(exported), false)
	if code != http.StatusOK || !resp.OK {
		t.Fatalf("import = %d %+v", code, resp)
	}
	requireAdjustments(t, e.adjustments(), seedAdjustments)
	if !strings.Contains(resp.Message, "4 goal adjustments") {
		t.Errorf("message %q does not report the adjustments", resp.Message)
	}
}

// Adjustment rows are absolute snapshots: they come back as written, with no
// gap-fill from the importing account's Basisziel.
func TestImportStoresAdjustmentsVerbatim(t *testing.T) {
	e := newV1Env(t)
	if _, err := e.Pool.Exec(e.Ctx, `UPDATE users SET macro_goals = '{"calories":2200,"protein":120}' WHERE id = $1`, e.UserID); err != nil {
		t.Fatal(err)
	}
	code, _ := e.runImport(`{"goal_adjustments":[{"date":"2026-06-01","carbs":400}]}`, false)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireAdjustments(t, e.adjustments(), []adjRow{{Date: "2026-06-01", Carbs: ptr(400)}})
}

func TestImportAdjustmentsOnlyFileSucceedsAndKeepsEntries(t *testing.T) {
	e := newV1Env(t)
	if _, err := e.Pool.Exec(e.Ctx, `INSERT INTO calorie_entries (user_id, entry_date, amount, entry_name) VALUES ($1, '2026-05-01', 500, 'keep me')`, e.UserID); err != nil {
		t.Fatal(err)
	}
	code, resp := e.runImport(`{"goal_adjustments":[{"date":"2026-05-01","calories":3000,"reason":"x"}]}`, false)
	if code != http.StatusOK || !resp.OK {
		t.Fatalf("adjustments-only file = %d %+v, want 200", code, resp)
	}
	if n := e.entryCount(); n != 1 {
		t.Errorf("entries = %d, want the existing 1 untouched", n)
	}
	if got := e.adjustments(); len(got) != 1 {
		t.Errorf("adjustments = %+v", got)
	}
}

func TestImportReplacesAdjustmentsOnlyWhenFileCarriesAny(t *testing.T) {
	e := newV1Env(t)
	e.insertAdjustments(seedAdjustments...)

	// Old-format file: no goal_adjustments key. Existing adjustments survive.
	code, _ := e.runImport(`{"entries":[{"date":"2026-05-01","amount":100}]}`, false)
	if code != http.StatusOK {
		t.Fatalf("old-format import = %d", code)
	}
	requireAdjustments(t, e.adjustments(), seedAdjustments)

	// An empty array is not "carries rows" either.
	code, _ = e.runImport(`{"goal_adjustments":[],"entries":[{"date":"2026-05-01","amount":100}]}`, false)
	if code != http.StatusOK {
		t.Fatalf("empty-array import = %d", code)
	}
	requireAdjustments(t, e.adjustments(), seedAdjustments)

	// A file with rows replaces the set: the stale 05-01..05-04 rows disappear.
	code, _ = e.runImport(`{"goal_adjustments":[{"date":"2026-07-01","calories":2800}]}`, false)
	if code != http.StatusOK {
		t.Fatalf("replacing import = %d", code)
	}
	requireAdjustments(t, e.adjustments(), []adjRow{{Date: "2026-07-01", Calories: ptr(2800)}})
}

func TestImportDoesNotTouchAnotherUsersAdjustments(t *testing.T) {
	e := newV1Env(t)
	otherID, _ := e.seedUser("-other")
	if _, err := e.Pool.Exec(e.Ctx, `INSERT INTO goal_adjustments (user_id, goal_date, calories) VALUES ($1, '2026-05-01', 1234)`, otherID); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.runImport(`{"goal_adjustments":[{"date":"2026-05-01","calories":3000}]}`, false); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var cal int
	if err := e.Pool.QueryRow(e.Ctx, `SELECT calories FROM goal_adjustments WHERE user_id = $1`, otherID).Scan(&cal); err != nil || cal != 1234 {
		t.Errorf("other user's adjustment = %d (%v), want 1234", cal, err)
	}
}

func TestImportDryRunMatchesRealImportForAdjustments(t *testing.T) {
	e := newV1Env(t)
	e.insertAdjustments(adjRow{Date: "2026-01-01", Calories: ptr(1111)})
	file := `{"goal_adjustments":[
		{"date":"2026-05-01","calories":3000,"reason":"ok"},
		{"date":"2026-05-02","calories":-1},
		{"date":"2026-05-01","calories":2000}
	],"entries":[{"date":"2026-05-01","amount":100}]}`

	code, dry := e.runImport(file, true)
	if code != http.StatusOK || !dry.DryRun {
		t.Fatalf("dry run = %d %+v", code, dry)
	}
	requireAdjustments(t, e.adjustments(), []adjRow{{Date: "2026-01-01", Calories: ptr(1111)}}) // nothing written

	code, real := e.runImport(file, false)
	if code != http.StatusOK || real.DryRun {
		t.Fatalf("real import = %d %+v", code, real)
	}
	if want := real.Message + " Nothing was written."; dry.Message != want {
		t.Errorf("dry message %q != real message + suffix %q", dry.Message, want)
	}
	if dry.Skipped.Total != 2 || real.Skipped.Total != 2 {
		t.Errorf("skipped totals dry=%d real=%d, want 2/2", dry.Skipped.Total, real.Skipped.Total)
	}
	var reasons []string
	for _, r := range real.Skipped.Rows {
		if r.Kind == "adjustment" {
			reasons = append(reasons, r.Reason)
		}
	}
	if strings.Join(reasons, ",") != "invalid_value,duplicate_date" {
		t.Errorf("adjustment skip reasons = %v", reasons)
	}
	requireAdjustments(t, e.adjustments(), []adjRow{{Date: "2026-05-01", Calories: ptr(3000), Reason: ptr("ok")}})
}

func TestExportWithoutAdjustmentsEmitsAnEmptyArray(t *testing.T) {
	e := newV1Env(t)
	exported := e.runExport()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(exported, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(doc["goal_adjustments"]) != "[]" {
		t.Errorf("goal_adjustments = %s, want []", doc["goal_adjustments"])
	}
}
