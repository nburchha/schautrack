package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"schautrack/internal/apierr"
)

// Catalog foods carry values per 100 g with decimals, unlike entries and saved
// foods, which are whole numbers. NULL means unknown and is never read as 0.
const (
	maxFoodName     = 120
	maxCatalogKcal  = 1000.0
	maxCatalogMacro = 100.0
	maxSearchQuery  = 100
	minSearchQuery  = 2
	maxSearchLimit  = 50
)

type v1FoodMacros struct {
	ProteinG *float64 `json:"protein_g"`
	CarbsG   *float64 `json:"carbs_g"`
	FatG     *float64 `json:"fat_g"`
	FiberG   *float64 `json:"fiber_g"`
	SugarG   *float64 `json:"sugar_g"`
}

// v1Food is a catalog food: nutrients per 100 g.
type v1Food struct {
	ID                   int          `json:"id"`
	Source               string       `json:"source"`
	SourceCode           *string      `json:"source_code"`
	Name                 string       `json:"name"`
	SourceClassification *string      `json:"source_classification"`
	Calories             *float64     `json:"calories_per_100g"`
	Macros               v1FoodMacros `json:"macros_per_100g"`
	CreatedAt            time.Time    `json:"created_at"`
}

const foodSelect = `id, source, source_code, name, source_classification,
	kcal_100g::float8, protein_100g::float8, carbs_100g::float8, fat_100g::float8,
	fiber_100g::float8, sugar_100g::float8, created_at`

func scanFood(row pgx.Row) (*v1Food, error) {
	var f v1Food
	if err := row.Scan(&f.ID, &f.Source, &f.SourceCode, &f.Name, &f.SourceClassification,
		&f.Calories, &f.Macros.ProteinG, &f.Macros.CarbsG, &f.Macros.FatG,
		&f.Macros.FiberG, &f.Macros.SugarG, &f.CreatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// likeEscape neutralises LIKE wildcards in user input.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// SearchFoodsV1 handles GET /api/v1/foods/search.
//
// Matches global catalog foods and the caller's own. Substring matches and
// trigram-similar names both count, so a typo still finds the food. Exact
// names rank first, then prefixes, then similarity; ties go to the caller's
// own foods, then to shorter names.
func (h *V1Handler) SearchFoodsV1(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if strings.ContainsRune(q, 0) {
		// Postgres TEXT cannot hold NUL; refuse it here, as decodeV1 does for bodies.
		apierr.Write(w, r, apierr.BadRequest("Text fields cannot contain NUL characters."))
		return
	}
	if len([]rune(q)) < minSearchQuery {
		apierr.Write(w, r, apierr.Unprocessable(
			fmt.Sprintf(`"q" needs at least %d characters.`, minSearchQuery),
			apierr.InvalidParam{Name: "q", Reason: "too short"}))
		return
	}
	q = truncateUTF8(q, maxSearchQuery)

	limit, prob := queryLimit(r)
	if prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	user := v1User(r)
	rows, err := h.Pool.Query(r.Context(), `
		SELECT `+foodSelect+`
		FROM foods
		WHERE (user_id IS NULL OR user_id = $2)
		  AND (name ILIKE '%' || $3 || '%' ESCAPE '\' OR $1 <% name)
		ORDER BY lower(name) = lower($1) DESC,
		         name ILIKE $3 || '%' ESCAPE '\' DESC,
		         GREATEST(word_similarity($1, name), similarity(name, $1)) DESC,
		         (user_id IS NOT NULL) DESC,
		         length(name),
		         id
		LIMIT $4`,
		q, user.ID, likeEscape(q), limit)
	if err != nil {
		apierr.Write(w, r, dbFail("search foods", err))
		return
	}
	defer rows.Close()

	out := []v1Food{}
	for rows.Next() {
		f, err := scanFood(rows)
		if err != nil {
			apierr.Write(w, r, dbFail("scan food", err))
			return
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		apierr.Write(w, r, dbFail("iterate foods", err))
		return
	}
	writeV1(w, http.StatusOK, v1List[v1Food]{Data: out})
}

type v1FoodInput struct {
	Name     string   `json:"name"`
	Calories *float64 `json:"calories_per_100g"`
	ProteinG *float64 `json:"protein_g"`
	CarbsG   *float64 `json:"carbs_g"`
	FatG     *float64 `json:"fat_g"`
	FiberG   *float64 `json:"fiber_g"`
	SugarG   *float64 `json:"sugar_g"`
}

// CreateFoodV1 handles POST /api/v1/foods: a food of the caller's own, such as
// a baker's bread with values from the label. Own foods are created on purpose,
// so a name already in use is a 409 rather than a silent upsert.
func (h *V1Handler) CreateFoodV1(w http.ResponseWriter, r *http.Request) {
	var in v1FoodInput
	if prob := decodeV1(w, r, &in); prob != nil {
		apierr.Write(w, r, prob)
		return
	}

	name := truncateUTF8(strings.TrimSpace(in.Name), maxFoodName)
	if name == "" {
		apierr.Write(w, r, apierr.Unprocessable("A food needs a name.",
			apierr.InvalidParam{Name: "name", Reason: "required"}))
		return
	}

	var bad []apierr.InvalidParam
	check := func(field string, v *float64, max float64) {
		if v != nil && (*v < 0 || *v > max) {
			bad = append(bad, apierr.InvalidParam{
				Name: field, Reason: fmt.Sprintf("must be between 0 and %g", max)})
		}
	}
	check("calories_per_100g", in.Calories, maxCatalogKcal)
	check("protein_g", in.ProteinG, maxCatalogMacro)
	check("carbs_g", in.CarbsG, maxCatalogMacro)
	check("fat_g", in.FatG, maxCatalogMacro)
	check("fiber_g", in.FiberG, maxCatalogMacro)
	check("sugar_g", in.SugarG, maxCatalogMacro)
	if bad != nil {
		apierr.Write(w, r, apierr.Unprocessable("One or more values per 100 g are out of range.", bad...))
		return
	}
	if in.Calories == nil {
		// A food without kcal cannot be a component (architecture.md).
		apierr.Write(w, r, apierr.Unprocessable("A food needs a calorie value per 100 g.",
			apierr.InvalidParam{Name: "calories_per_100g", Reason: "required"}))
		return
	}

	user := v1User(r)
	f, err := scanFood(h.Pool.QueryRow(r.Context(), `
		INSERT INTO foods (user_id, source, name, kcal_100g, protein_100g, carbs_100g, fat_100g, fiber_100g, sugar_100g)
		VALUES ($1, 'own', $2, $3, $4, $5, $6, $7, $8) RETURNING `+foodSelect,
		user.ID, name, in.Calories, in.ProteinG, in.CarbsG, in.FatG, in.FiberG, in.SugarG))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			apierr.Write(w, r, apierr.Conflict("A food of yours with that name already exists."))
			return
		}
		apierr.Write(w, r, dbFail("create food", err))
		return
	}

	writeV1(w, http.StatusCreated, f)
}
