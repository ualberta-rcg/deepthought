package server

// Database wiring: the server opens the shared MySQL pool (schema ensured at
// boot) and exposes per-user store views. When no DSN is configured the DB
// features answer 503 — the health/UI/auth surface keeps working, exactly
// like the no-password auth state.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"deepthought-cli/internal/server/store"
)

// errDBNotConfigured is returned by DB-backed endpoints when no DSN was given.
var errDBNotConfigured = errors.New("server database not configured (set DEEPTHOUGHT_MYSQL_DSN)")

// sessionKey is the context key for the authenticated session.
type sessionKey struct{}

// SessionUser returns the authenticated user from the request context.
func SessionUser(r *http.Request) string {
	if sess, ok := r.Context().Value(sessionKey{}).(Session); ok {
		return sess.User
	}
	return ""
}

// sanitizeUserID reduces a login username to a safe DB key (the trust anchor
// is the shared password; the user field is an identity label).
var userIDSafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeUserID(user string) string {
	id := userIDSafe.ReplaceAllString(strings.TrimSpace(user), "-")
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// OpenDB opens the MySQL pool from a DSN and ensures the schema.
func OpenDB(dsn string) (*sql.DB, *store.UserStore, error) {
	db, err := store.OpenMySQL(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("server db: %w", err)
	}
	return db, store.NewUserStore(db), nil
}

// userStore returns a per-user store view for the request's session.
func (a *API) userStore(r *http.Request) (*store.MySQLStore, error) {
	if a.DB == nil {
		return nil, errDBNotConfigured
	}
	user := SessionUser(r)
	if user == "" {
		return nil, fmt.Errorf("no session user")
	}
	return store.ForUser(a.DB, sanitizeUserID(user)), nil
}
