package web

import (
	"net/http"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// authSignals is what the sign-in screen sends.
type authSignals struct {
	Email    string `json:"authEmail"`
	Password string `json:"authPassword"`
	Name     string `json:"authName"`
}

// GetLogin renders the sign-in screen, or the first-administrator bootstrap when
// the installation has no accounts at all.
func (a *App) GetLogin(w http.ResponseWriter, r *http.Request) {
	open, err := a.Auth.BootstrapOpen(r.Context())
	if err != nil {
		a.Log.Error("bootstrap check failed", "err", err)
		http.Error(w, "The database is not reachable.", http.StatusInternalServerError)
		return
	}
	if open {
		http.Redirect(w, r, "/bootstrap", http.StatusSeeOther)
		return
	}
	_ = view.AuthPage(view.Auth{}).Render(r.Context(), w)
}

// GetBootstrap renders the one-time first-administrator screen.
//
// It re-checks that the installation is still empty and refuses otherwise, so
// the route cannot be reached by anyone who bookmarked it before the first
// account existed. The check is repeated in the service inside a single
// statement, so this one is a courtesy rather than the guarantee.
func (a *App) GetBootstrap(w http.ResponseWriter, r *http.Request) {
	open, err := a.Auth.BootstrapOpen(r.Context())
	if err != nil {
		a.Log.Error("bootstrap check failed", "err", err)
		http.Error(w, "The database is not reachable.", http.StatusInternalServerError)
		return
	}
	if !open {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = view.AuthPage(view.Auth{Bootstrap: true}).Render(r.Context(), w)
}

// PostLogin authenticates and starts a session.
func (a *App) PostLogin(w http.ResponseWriter, r *http.Request) {
	// Signals are read BEFORE the SSE stream is opened: opening it primes and
	// flushes the response, after which the request body can no longer be read.
	var in authSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.authError(w, r, "Could not read the sign-in form. Reload the page and try again.")
		return
	}

	u, err := a.Auth.Authenticate(r.Context(), in.Email, in.Password)
	if err != nil {
		// One message for every failure. Distinguishing "no such account" from
		// "wrong password" would turn this form into a way to discover which
		// email addresses are registered.
		a.authError(w, r, "That email and password do not match an active account.")
		return
	}
	a.signIn(w, r, u.ID)
}

// PostBootstrap creates the first administrator.
func (a *App) PostBootstrap(w http.ResponseWriter, r *http.Request) {
	var in authSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.authError(w, r, "Could not read the form. Reload the page and try again.")
		return
	}

	u, err := a.Auth.Register(r.Context(), in.Email, in.Password, in.Name)
	if err != nil {
		a.authError(w, r, a.userMessage(err))
		return
	}
	a.Log.Info("first administrator created", "email", u.Email)
	a.signIn(w, r, u.ID)
}

// signIn renews the session token, records the user, and sends the browser on.
func (a *App) signIn(w http.ResponseWriter, r *http.Request, userID string) {
	// Renewing the token before recording who is signed in prevents session
	// fixation: a token an attacker planted before sign-in is discarded rather
	// than promoted to an authenticated one.
	if err := a.Sessions.RenewToken(r.Context()); err != nil {
		a.Log.Error("session renew failed", "err", err)
		a.authError(w, r, "Could not start a session.")
		return
	}
	a.Sessions.Put(r.Context(), sessionUserKey, userID)

	sse := render.NewSSE(w, r)
	// A datastar action expects an SSE body, so navigation is instructed rather
	// than answered with a 303 the client would not follow.
	_ = sse.ExecuteScript("window.location.href = '/'")
}

// authError shows a failure on the sign-in screen without leaving it.
func (a *App) authError(w http.ResponseWriter, r *http.Request, msg string) {
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.AuthMessage(msg))
}

// GetLogout ends the session.
//
// It is a normal link rather than a datastar action, so it stays a plain
// navigation that works with the keyboard, middle-click and the back button.
func (a *App) GetLogout(w http.ResponseWriter, r *http.Request) {
	if err := a.Sessions.Destroy(r.Context()); err != nil {
		a.Log.Warn("session destroy failed", "err", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// userSignals is what the account-management screen sends.
type userSignals struct {
	Email    string `json:"userEmail"`
	Name     string `json:"userName"`
	Password string `json:"userPassword"`
	Admin    bool   `json:"userAdmin"`
}

// GetUsers renders account management.
func (a *App) GetUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.Users.Users(r.Context())
	if err != nil {
		a.Log.Error("list users", "err", err)
		http.Error(w, "Could not list accounts.", http.StatusInternalServerError)
		return
	}
	u := view.Users{Page: a.page(r, "Users", "users"), Users: users}
	_ = view.PageShell(u.Page, nil, view.UsersScreen(u)).Render(r.Context(), w)
}

// PostUsers creates an account. This is the only way an account is made after
// the first one; there is no public registration anywhere in this product.
func (a *App) PostUsers(w http.ResponseWriter, r *http.Request) {
	var in userSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "users-msg", "error", "Could not read the form.")
		return
	}
	actor, _ := UserFrom(r.Context())

	created, err := a.Auth.CreateUser(r.Context(), actor, in.Email, in.Password, in.Name, in.Admin)
	if err != nil {
		a.flash(w, r, "users-msg", "error", a.userMessage(err))
		return
	}

	users, err := a.Users.Users(r.Context())
	if err != nil {
		a.flash(w, r, "users-msg", "error", a.userMessage(err))
		return
	}

	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.Flash("users-msg", "ok",
		"Created "+created.Email+". Give them the password directly — there is no reset email."))
	_ = sse.PatchElementTempl(view.UserTable(users))
}
