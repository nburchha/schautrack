// Command import-foods loads a food source into the catalog.
//
//	import-foods --source bls
//
// It reads DATABASE_URL like the server, runs the migrations, and is safe to
// run repeatedly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"schautrack/internal/config"
	"schautrack/internal/database"
	"schautrack/internal/foodimport"
	"schautrack/internal/foodimport/bls"
)

var sources = map[string]foodimport.Source{
	"bls": bls.Source{},
}

func main() {
	source := flag.String("source", "", "source to import (bls)")
	flag.Parse()

	src, ok := sources[*source]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown --source %q, available: bls\n", *source)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := database.InitSchemaWithRetry(ctx, pool, 3); err != nil {
		slog.Error("schema init failed", "error", err)
		os.Exit(1)
	}

	res, err := foodimport.Import(ctx, pool, src)
	if err != nil {
		slog.Error("import failed", "source", src.Name(), "error", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d inserted, %d updated, %d without kcal, %d skipped %v\n",
		src.Name(), res.Inserted, res.Updated, res.NoKcal, len(res.Skipped), res.Skipped)
}
