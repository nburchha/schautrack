// Command mint-token provisions an API token without a browser session.
//
//	mint-token --user-email me@example.com --name scale --scopes weight:write --out /token/token
//
// It reads DATABASE_URL like the server, runs the migrations, and is safe to run
// repeatedly: if the file already holds a token that is valid for this user, name
// and scopes it does nothing, otherwise it revokes the old token of that name and
// writes a new one. The secret goes to --out only, never to stdout or the logs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"schautrack/internal/config"
	"schautrack/internal/database"
	"schautrack/internal/service"
)

func main() {
	email := flag.String("user-email", "", "email of the account the token belongs to")
	name := flag.String("name", "", "token name; tokens with this name are replaced")
	scopes := flag.String("scopes", "", "comma-separated scopes, e.g. weight:write")
	out := flag.String("out", "", "file the token is written to")
	flag.Parse()

	if *email == "" || *name == "" || *scopes == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "--user-email, --name, --scopes and --out are required")
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

	var userID int
	err = pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, *email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Fresh install: the account is created in the app, then the job is run again.
		slog.Error("no account with this email yet, register it in the app first", "email", *email)
		os.Exit(1)
	}
	if err != nil {
		slog.Error("user lookup failed", "error", err)
		os.Exit(1)
	}

	current := ""
	if b, err := os.ReadFile(*out); err == nil {
		current = strings.TrimSpace(string(b))
	}

	raw, created, err := service.EnsureAPIToken(ctx, pool, userID, *name, strings.Split(*scopes, ","), current)
	if err != nil {
		slog.Error("ensure token failed", "error", err)
		os.Exit(1)
	}
	if !created {
		fmt.Printf("token %q is valid, nothing to do\n", *name)
		return
	}
	if err := writeSecret(*out, raw); err != nil {
		slog.Error("write token failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("token %q written to %s\n", *name, *out)
}

// writeSecret replaces the file atomically, so a reader never sees half a token.
// The mode is world-readable on purpose: the consumer runs as another user in
// another container, and the directory is a volume shared only between the two.
func writeSecret(path, secret string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
