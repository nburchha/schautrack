package service

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureAPIToken makes sure a user has a working token with exactly the given
// name and scopes, and returns the raw secret to use.
//
// current is a secret issued earlier (for example read back from a file), or "".
// If it still authenticates for this user, carries this name, and holds exactly
// these scopes, it is returned unchanged and created is false. Otherwise every
// live token of this user with this name is revoked and a fresh one is minted,
// so repeated runs never pile up tokens and a lost secret is replaced.
//
// This exists for provisioning (cmd/mint-token), where no browser session is
// available to use the token UI. It is not reachable over HTTP: tokens still
// cannot mint tokens.
func EnsureAPIToken(ctx context.Context, pool *pgxpool.Pool, userID int, name string, scopes []string, current string) (raw string, created bool, err error) {
	want, err := ValidateScopes(scopes)
	if err != nil {
		return "", false, err
	}

	if current != "" {
		t, err := AuthenticateAPIToken(ctx, pool, current)
		switch {
		case err == nil:
			if t.UserID == userID && t.Name == name && slices.Equal(t.Scopes, want) {
				return current, false, nil
			}
		case !errors.Is(err, ErrTokenInvalid):
			return "", false, err
		}
	}

	if _, err := pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = NOW()
		 WHERE user_id = $1 AND name = $2 AND revoked_at IS NULL`, userID, name); err != nil {
		return "", false, err
	}
	_, raw, err = CreateAPIToken(ctx, pool, userID, name, want, nil)
	if err != nil {
		return "", false, err
	}
	return raw, true, nil
}
