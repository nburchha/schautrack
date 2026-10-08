package foodimport

import (
	"context"
	"fmt"
	"testing"
	"time"

	"schautrack/internal/database"
	"schautrack/internal/dbtest"
)

type fakeSource []Food

func (fakeSource) Name() string             { return "bls" }
func (s fakeSource) Foods() ([]Food, error) { return s, nil }

func f(v float64) *float64 { return &v }

func TestImport(t *testing.T) {
	ctx := context.Background()
	pool, err := database.NewPool(ctx, dbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.InitSchemaWithRetry(ctx, pool, 1); err != nil {
		t.Fatal(err)
	}

	// The test database outlives a run, so codes carry a unique tag.
	tag := fmt.Sprintf("T%d", time.Now().UnixNano()%1_000_000_000)
	src := fakeSource{
		{Code: tag + "1", Name: "Testbrot", Classification: "B", Kcal: f(250), Protein: f(8), Carbs: f(45.5)},
		{Code: tag + "2", Name: "Testwasser", Kcal: nil},
		{Code: tag + "3", Name: "Zu viel", Kcal: f(5000)},
		{Code: tag + "4", Name: "", Kcal: f(1)},
	}
	like := tag + "%"

	res, err := Import(ctx, pool, src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 2 || res.Updated != 0 || res.NoKcal != 1 || len(res.Skipped) != 2 {
		t.Fatalf("first import = %+v", res)
	}

	// Second run with a changed value: update, no duplicates.
	src[0].Kcal = f(260)
	res, err = Import(ctx, pool, src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 0 || res.Updated != 2 {
		t.Fatalf("second import = %+v", res)
	}

	var n int
	var kcal float64
	var protein, fat *float64
	var class *string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM foods WHERE source = 'bls' AND source_code LIKE $1`, like).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows = %d, err %v, want 2", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT kcal_100g::float8, protein_100g::float8, fat_100g::float8, source_classification
		FROM foods WHERE source_code = $1`, tag+"1").Scan(&kcal, &protein, &fat, &class); err != nil {
		t.Fatal(err)
	}
	if kcal != 260 || protein == nil || *protein != 8 || fat != nil || class == nil || *class != "B" {
		t.Errorf("row = kcal %v protein %v fat %v class %v; unknown fat must stay NULL", kcal, protein, fat, class)
	}
	var nullClass bool
	if err := pool.QueryRow(ctx, `SELECT source_classification IS NULL AND kcal_100g IS NULL
		FROM foods WHERE source_code = $1`, tag+"2").Scan(&nullClass); err != nil || !nullClass {
		t.Errorf("unknown kcal / empty classification must be NULL (err %v)", err)
	}
}
