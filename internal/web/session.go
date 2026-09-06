package web

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// sessionUserKey is the session field holding the signed-in user's id.
//
// The session carries the ID ONLY, never a copy of the user record. A cached
// copy would keep an account signed in with stale rights after an admin disabled
// it or removed its admin flag — the change would not take effect until the
// cookie expired, which is the opposite of what disabling an account means.
const sessionUserKey = "uid"

// NewSessions configures the session manager.
//
// Sessions are stored in SQLite rather than in the cookie, so that signing
// someone out actually revokes their session instead of relying on the browser
// to discard a token it still holds.
func NewSessions(write *sql.DB, secure bool) *scs.SessionManager {
	m := scs.New()
	m.Store = sqlite3store.New(write)
	m.Lifetime = 12 * time.Hour
	m.IdleTimeout = 2 * time.Hour
	m.Cookie.Name = "warehouse_session"
	m.Cookie.HttpOnly = true
	m.Cookie.Path = "/"
	// Lax rather than Strict: Strict drops the cookie on a top-level navigation
	// from another site, so following a link to an offer would land on the sign-in
	// page even while signed in. Lax still blocks the cross-site POST that
	// SameSite exists to stop.
	m.Cookie.SameSite = http.SameSiteLaxMode
	// Secure is driven by configuration rather than hardcoded, because a cookie
	// marked Secure is simply not sent over plain HTTP and the whole application
	// would appear to reject every login on a local development machine.
	m.Cookie.Secure = secure
	return m
}

// ctxUserKey types the request-context key for the resolved user, so it cannot
// collide with a key any other package puts in the same context.
type ctxUserKey struct{}

// WithUser returns a context carrying the signed-in user.
func WithUser(ctx context.Context, u core.User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, u)
}

// UserFrom returns the signed-in user from a context.
//
// It is how a templ component reaches the current user without every render
// function taking one as a parameter, which is the pattern templ's own guidance
// suggests for exactly this sort of ambient value.
func UserFrom(ctx context.Context) (core.User, bool) {
	u, ok := ctx.Value(ctxUserKey{}).(core.User)
	return u, ok
}
