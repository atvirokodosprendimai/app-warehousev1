package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ErrNotPermitted reports that the acting account may not perform the request.
var ErrNotPermitted = errors.New("auth: not permitted")

// ErrBadCredentials reports a failed sign-in.
//
// It is deliberately the ONE answer to "no such account", "wrong password" and
// "account disabled". Anything that distinguished them would turn the sign-in
// form into an oracle for which addresses hold accounts here.
var ErrBadCredentials = errors.New("auth: invalid email or password")

// ErrLastAdmin reports a refusal to disable the only remaining active
// administrator.
//
// There is no recovery path — no password reset, no console — so an
// installation that disables its last admin is locked out of its own user
// management for good.
var ErrLastAdmin = errors.New("auth: cannot disable the last active administrator")

// dummyPasswordHash is a real bcrypt hash of a password nobody holds. It is
// compared against when no account matched the address.
//
// Without it, "no such email" returns in microseconds while "wrong password"
// spends the tens of milliseconds bcrypt deliberately costs, and that difference
// is a timing oracle that reveals which addresses are registered — undoing the
// indistinguishable [ErrBadCredentials] above. It is built once, lazily, at
// bcrypt.DefaultCost so that it stays exactly as expensive as a real hash.
var dummyPasswordHash = sync.OnceValue(func() []byte {
	h, err := bcrypt.GenerateFromPassword(
		[]byte("no account holds this password"), bcrypt.DefaultCost)
	if err != nil {
		panic("auth: cannot hash the dummy password: " + err.Error())
	}
	return h
})

// Service is the write side of accounts. It holds a [core.UserStore] and is the
// only thing in the application allowed to mint, re-credential or disable one.
type Service struct {
	users core.UserStore
}

// NewService returns a Service backed by users.
func NewService(users core.UserStore) *Service {
	return &Service{users: users}
}

// Register mints the first administrator and is the zero-user bootstrap's only
// entry point.
//
// It goes through [core.UserWriter.CreateFirstAdmin], never a plain insert, so
// that the "is the installation still empty?" test and the insert are one atomic
// statement. Once somebody has won that race every later call returns an error
// wrapping [ErrBootstrapClosed], which is what keeps the route from becoming
// public registration by accident.
func (s *Service) Register(ctx context.Context, email, password, displayName string) (core.User, error) {
	u, err := newUser(email, password, displayName)
	if err != nil {
		return core.User{}, err
	}
	// CreateFirstAdmin writes is_admin = 1 itself; mirror that on the value
	// returned so the caller's copy matches the stored row.
	u.IsAdmin = true
	if err := s.users.CreateFirstAdmin(ctx, u); err != nil {
		return core.User{}, err
	}
	return u, nil
}

// CreateUser creates an account on behalf of actor, who must be an active
// administrator, and returns an error wrapping [ErrNotPermitted] otherwise.
//
// This is the only way an account is made after the first one: the product has
// no public registration.
func (s *Service) CreateUser(ctx context.Context, actor core.User, email, password, displayName string, admin bool) (core.User, error) {
	if err := requireAdmin(actor); err != nil {
		return core.User{}, err
	}
	u, err := newUser(email, password, displayName)
	if err != nil {
		return core.User{}, err
	}
	u.IsAdmin = admin
	if err := s.users.CreateUser(ctx, u); err != nil {
		return core.User{}, err
	}
	return u, nil
}

// Authenticate resolves an email and password to an active account.
//
// Every failure — unparseable address, no such account, wrong password, disabled
// account — returns the bare [ErrBadCredentials] value, so the four paths are
// indistinguishable in both the sentinel and the message. Only a genuine store
// failure returns something else, because reporting an outage as a bad password
// would send the operator hunting for the wrong problem.
func (s *Service) Authenticate(ctx context.Context, email, password string) (core.User, error) {
	normalized, err := core.NormalizeEmail(email)
	if err != nil {
		// An address that does not parse cannot name an account, so it gets the
		// same answer, and the same amount of work, as one that names none.
		equaliseTiming(password)
		return core.User{}, ErrBadCredentials
	}

	u, err := s.users.UserByEmail(ctx, normalized)
	if errors.Is(err, core.ErrNotFound) {
		equaliseTiming(password)
		return core.User{}, ErrBadCredentials
	}
	if err != nil {
		return core.User{}, fmt.Errorf("auth: authenticate: %w", err)
	}

	// The password is checked before the disabled flag on purpose: doing it the
	// other way round would answer for a disabled account without paying the
	// bcrypt cost, which is the same timing oracle in a different place.
	if err := bcrypt.CompareHashAndPassword(
		[]byte(u.PasswordHash), []byte(password)); err != nil {
		return core.User{}, ErrBadCredentials
	}
	if !u.Active() {
		return core.User{}, ErrBadCredentials
	}
	return u, nil
}

// SetPassword replaces an account's password.
//
// An active administrator may set anyone's; anybody may set their own. Every
// other combination returns an error wrapping [ErrNotPermitted].
func (s *Service) SetPassword(ctx context.Context, actor core.User, userID, newPassword string) error {
	// An empty actor id is a zero-valued caller, not a signed-in one, and must
	// never satisfy the self-service branch by matching an empty userID.
	self := actor.ID != "" && actor.ID == userID
	if !self {
		if err := requireAdmin(actor); err != nil {
			return err
		}
	}

	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	u, err := s.users.User(ctx, userID)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	if err := s.users.UpdateUser(ctx, u); err != nil {
		return err
	}
	return nil
}

// SetDisabled blocks or restores sign-in for an account. Only an active
// administrator may call it.
//
// It refuses to disable the last active administrator with an error wrapping
// [ErrLastAdmin]. That check is the difference between a reversible mistake and
// an installation that can no longer manage its own users.
func (s *Service) SetDisabled(ctx context.Context, actor core.User, userID string, disabled bool) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	u, err := s.users.User(ctx, userID)
	if err != nil {
		return err
	}

	if disabled && u.IsAdmin && u.Active() {
		admins, err := s.countActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("%w: %s", ErrLastAdmin, u.Email)
		}
	}

	switch {
	case disabled && u.Active():
		now := time.Now().UTC()
		u.DisabledAt = &now
	case !disabled:
		u.DisabledAt = nil
	}
	// Disabling an already-disabled account falls through both cases and leaves
	// the original timestamp, so the audit answer to "since when" is not lost to
	// a repeated click.

	if err := s.users.UpdateUser(ctx, u); err != nil {
		return err
	}
	return nil
}

// BootstrapOpen reports whether the one-time admin bootstrap is still available,
// which is true only while no account exists.
//
// The router asks this before exposing the registration route at all, so that
// the route does not merely refuse after the first admin — it stops existing.
func (s *Service) BootstrapOpen(ctx context.Context) (bool, error) {
	n, err := s.users.CountUsers(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// countActiveAdmins reports how many accounts can still administer the
// installation.
func (s *Service) countActiveAdmins(ctx context.Context) (int, error) {
	users, err := s.users.Users(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		if u.IsAdmin && u.Active() {
			n++
		}
	}
	return n, nil
}

// requireAdmin reports whether actor may exercise administrative authority.
//
// Being disabled revokes it: a session minted before the account was disabled
// would otherwise keep full access until it expired, which is precisely the
// window [Service.SetDisabled] exists to close.
func requireAdmin(actor core.User) error {
	if !actor.IsAdmin || !actor.Active() {
		return fmt.Errorf("%w: administrator access required", ErrNotPermitted)
	}
	return nil
}

// newUser validates the inputs for a fresh account and assembles it. Both
// creation paths go through here so that "what a new account is made of" —
// normalised address, checked password, UUID id, UTC clock — is stated once.
func newUser(email, password, displayName string) (core.User, error) {
	normalized, err := core.NormalizeEmail(email)
	if err != nil {
		return core.User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return core.User{}, err
	}
	return core.User{
		ID:           uuid.NewString(),
		Email:        normalized,
		PasswordHash: hash,
		DisplayName:  strings.TrimSpace(displayName),
		CreatedAt:    time.Now().UTC(),
	}, nil
}

// hashPassword checks a proposed password against the domain rule and hashes it.
func hashPassword(password string) (string, error) {
	if err := core.ValidatePassword(password); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(h), nil
}

// equaliseTiming spends the same bcrypt cost a real comparison would, so that a
// miss and a wrong password take the same time. The result is discarded by
// design: there is nothing to learn from comparing against a hash nobody holds.
func equaliseTiming(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(password))
}
