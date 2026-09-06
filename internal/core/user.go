package core

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// User is someone who can sign in.
//
// There is no public registration: the only self-service path is the zero-user
// bootstrap that mints the first admin. Every later account is created by an
// admin, so this type has no "pending approval" state to model.
type User struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// Email is the sign-in identifier, stored lowercased.
	Email string
	// PasswordHash is a bcrypt hash. The plaintext never leaves the handler.
	PasswordHash string
	// DisplayName is shown in the UI; falls back to the email's local part.
	DisplayName string
	// IsAdmin grants access to user management and destructive actions.
	IsAdmin bool
	// CreatedAt is a UTC timestamp.
	CreatedAt time.Time
	// DisabledAt, when set, blocks sign-in while keeping the row so that the
	// audit trail on offers this user touched still resolves to a name.
	DisabledAt *time.Time
}

// Active reports whether the user may sign in.
func (u *User) Active() bool { return u != nil && u.DisabledAt == nil }

// Name returns the best available display name.
func (u *User) Name() string {
	if u == nil {
		return ""
	}
	if strings.TrimSpace(u.DisplayName) != "" {
		return u.DisplayName
	}
	local, _, _ := strings.Cut(u.Email, "@")
	return local
}

// NormalizeEmail lowercases and trims an address and checks it parses.
//
// Case folding happens here rather than in the database's collation so that the
// value stored, the value compared and the value displayed are the same string —
// a NOCASE column would let two rows differ only in case if the collation were
// ever dropped in a migration.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", fmt.Errorf("%w: email is required", ErrInvalid)
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return "", fmt.Errorf("%w: %q is not a valid email address", ErrInvalid, s)
	}
	return s, nil
}

// MinPasswordLength is the shortest password accepted.
//
// Length is the only rule. Composition rules (a digit, a symbol) measurably push
// people towards predictable substitutions and a sticky note, and this app has
// no password reset flow to rescue them with.
const MinPasswordLength = 10

// ValidatePassword checks a proposed password.
func ValidatePassword(p string) error {
	if len([]rune(p)) < MinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters",
			ErrInvalid, MinPasswordLength)
	}
	return nil
}
