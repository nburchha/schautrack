// Package foodimport loads global catalog foods from a Source into the foods
// table. Each source (BLS, later others) is its own implementation; Import
// handles validation and the upsert.
package foodimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Food is one catalog row, values per 100 g. A nil value is unknown, never 0.
type Food struct {
	Code           string
	Name           string
	Classification string
	Kcal           *float64
	Protein        *float64
	Carbs          *float64
	Fat            *float64
	Fiber          *float64
	Sugar          *float64
}

// Source provides foods for one catalog source.
type Source interface {
	// Name is the value stored in foods.source ("bls", "off").
	Name() string
	Foods() ([]Food, error)
}

// Result counts what an import did.
type Result struct {
	Inserted int
	Updated  int
	// Skipped rows broke a foods range check (kcal 0-1000, macros 0-100) or
	// lacked a code or name.
	Skipped []string
	// NoKcal counts imported rows without kcal. They are searchable but cannot
	// be used as a component.
	NoKcal int
}

const (
	maxKcal  = 1000.0
	maxMacro = 100.0
)

func inRange(v *float64, max float64) bool {
	return v == nil || (*v >= 0 && *v <= max)
}

func valid(f Food) bool {
	return strings.TrimSpace(f.Code) != "" && strings.TrimSpace(f.Name) != "" &&
		inRange(f.Kcal, maxKcal) &&
		inRange(f.Protein, maxMacro) && inRange(f.Carbs, maxMacro) && inRange(f.Fat, maxMacro) &&
		inRange(f.Fiber, maxMacro) && inRange(f.Sugar, maxMacro)
}

const upsert = `
	INSERT INTO foods (source, source_code, name, source_classification,
		kcal_100g, protein_100g, carbs_100g, fat_100g, fiber_100g, sugar_100g)
	VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10)
	ON CONFLICT (source, source_code) WHERE user_id IS NULL DO UPDATE SET
		name = EXCLUDED.name,
		source_classification = EXCLUDED.source_classification,
		kcal_100g = EXCLUDED.kcal_100g,
		protein_100g = EXCLUDED.protein_100g,
		carbs_100g = EXCLUDED.carbs_100g,
		fat_100g = EXCLUDED.fat_100g,
		fiber_100g = EXCLUDED.fiber_100g,
		sugar_100g = EXCLUDED.sugar_100g,
		updated_at = NOW()
	RETURNING (xmax = 0)`

// Import upserts all foods of src in one transaction, keyed on
// (source, source_code). Running it again with the same data changes nothing
// but updated_at. Entries and components keep their own snapshots, so an
// update never touches history.
func Import(ctx context.Context, pool *pgxpool.Pool, src Source) (Result, error) {
	foods, err := src.Foods()
	if err != nil {
		return Result{}, fmt.Errorf("read %s foods: %w", src.Name(), err)
	}

	var res Result
	batch := &pgx.Batch{}
	for _, f := range foods {
		if !valid(f) {
			res.Skipped = append(res.Skipped, f.Code)
			continue
		}
		if f.Kcal == nil {
			res.NoKcal++
		}
		batch.Queue(upsert, src.Name(), f.Code, strings.TrimSpace(f.Name), f.Classification,
			f.Kcal, f.Protein, f.Carbs, f.Fat, f.Fiber, f.Sugar)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	br := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		var inserted bool
		if err := br.QueryRow().Scan(&inserted); err != nil {
			_ = br.Close()
			return Result{}, fmt.Errorf("upsert food %d: %w", i, err)
		}
		if inserted {
			res.Inserted++
		} else {
			res.Updated++
		}
	}
	if err := br.Close(); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return res, nil
}
