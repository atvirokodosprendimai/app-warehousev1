package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"golang.org/x/crypto/bcrypt"
)

// goodPassword is long enough for core.ValidatePassword.
const goodPassword = "correct horse battery"

// testService returns a Service over a fresh database, plus the repository
// underneath it for assertions that need to look at the stored row.
func testService(t *testing.T) (*Service, *Repo) {
	t.Helper()
	repo := testRepo(t)
	return NewService(repo), repo
}

// bootstrapAdmin registers the first administrator and returns it.
func bootstrapAdmin(t *testing.T, s *Service) core.User {
	t.Helper()
	admin, err := s.Register(context.Background(), "founder@example.test", goodPassword, "Founder")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return admin
}

func TestRegisterMintsAnAdminAndThenClosesTheBootstrap(t *testing.T) {
	svc, repo := testService(t)
	ctx := context.Background()

	admin, err := svc.Register(ctx, "  Founder@Example.Test  ", goodPassword, "  Founder  ")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !admin.IsAdmin {
		t.Error("the bootstrapped account is not an administrator")
	}
	if admin.Email != "founder@example.test" {
		t.Errorf("Email = %q, want the normalised %q", admin.Email, "founder@example.test")
	}
	if admin.DisplayName != "Founder" {
		t.Errorf("DisplayName = %q, want the trimmed %q", admin.DisplayName, "Founder")
	}
	if admin.ID == "" {
		t.Error("ID is empty, want a generated UUID")
	}
	if admin.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want the creation time")
	}
	if err := bcrypt.CompareHashAndPassword(
		[]byte(admin.PasswordHash), []byte(goodPassword)); err != nil {
		t.Errorf("the stored hash does not verify the password: %v", err)
	}

	// The stored row must agree, including the admin flag the SQL sets itself.
	stored, err := repo.User(ctx, admin.ID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if !stored.IsAdmin {
		t.Error("the stored account is not an administrator")
	}

	// Second call: bootstrap is closed for good.
	_, err = svc.Register(ctx, "latecomer@example.test", goodPassword, "Latecomer")
	if !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("second Register = %v, want an error wrapping ErrBootstrapClosed", err)
	}
	if n, err := repo.CountUsers(ctx); err != nil || n != 1 {
		t.Errorf("CountUsers = %d, %v; want 1, nil", n, err)
	}
}

func TestRegisterValidatesItsInput(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		password string
	}{
		{"unparseable email", "not-an-address", goodPassword},
		{"empty email", "   ", goodPassword},
		{"short password", "founder@example.test", "short"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := testService(t)
			ctx := context.Background()

			if _, err := svc.Register(ctx, tc.email, tc.password, "X"); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("Register = %v, want an error wrapping core.ErrInvalid", err)
			}
			if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
				t.Errorf("CountUsers = %d, %v; want 0, nil — a rejected registration must "+
					"not consume the bootstrap", n, err)
			}
		})
	}
}

func TestBootstrapOpenIsTrueOnlyWhileNoAccountExists(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	open, err := svc.BootstrapOpen(ctx)
	if err != nil {
		t.Fatalf("BootstrapOpen: %v", err)
	}
	if !open {
		t.Fatal("BootstrapOpen = false on an empty installation, want true")
	}

	bootstrapAdmin(t, svc)

	open, err = svc.BootstrapOpen(ctx)
	if err != nil {
		t.Fatalf("BootstrapOpen: %v", err)
	}
	if open {
		t.Error("BootstrapOpen = true after the first account, want false")
	}
}

func TestCreateUserRequiresAnActiveAdministrator(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)

	disabledAdmin := admin
	disabledAt := admin.CreatedAt
	disabledAdmin.DisabledAt = &disabledAt

	refused := []struct {
		name  string
		actor core.User
	}{
		{"zero actor", core.User{}},
		{"non-admin", core.User{ID: "u1", Email: "staff@example.test"}},
		{"disabled admin", disabledAdmin},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateUser(ctx, tc.actor, "new@example.test", goodPassword, "New", false)
			if !errors.Is(err, ErrNotPermitted) {
				t.Fatalf("CreateUser = %v, want an error wrapping ErrNotPermitted", err)
			}
			if _, err := svc.Authenticate(ctx, "new@example.test", goodPassword); !errors.Is(err, ErrBadCredentials) {
				t.Errorf("the refused account exists anyway: Authenticate = %v", err)
			}
		})
	}

	t.Run("active admin", func(t *testing.T) {
		created, err := svc.CreateUser(ctx, admin, "  Staff@Example.Test ", goodPassword, "Staff", true)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if created.Email != "staff@example.test" {
			t.Errorf("Email = %q, want the normalised %q", created.Email, "staff@example.test")
		}
		if !created.IsAdmin {
			t.Error("IsAdmin = false, want true — the admin argument was not honoured")
		}
		got, err := svc.Authenticate(ctx, "staff@example.test", goodPassword)
		if err != nil {
			t.Fatalf("Authenticate the new account: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("Authenticate returned id %q, want %q", got.ID, created.ID)
		}
	})
}

func TestCreateUserRefusesADuplicateAddress(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)

	_, err := svc.CreateUser(ctx, admin, "founder@example.test", goodPassword, "Clone", false)
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("CreateUser onto a taken address = %v, want an error wrapping ErrEmailTaken", err)
	}
}

// TestAuthenticateGivesOneIndistinguishableAnswerToEveryFailure is the
// enumeration guard: if any of these paths returned a different sentinel or a
// different message, the sign-in form would report which addresses exist here.
func TestAuthenticateGivesOneIndistinguishableAnswerToEveryFailure(t *testing.T) {
	svc, repo := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)

	disabled, err := svc.CreateUser(ctx, admin, "disabled@example.test", goodPassword, "Gone", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := svc.SetDisabled(ctx, admin, disabled.ID, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	if stored, err := repo.User(ctx, disabled.ID); err != nil || stored.Active() {
		t.Fatalf("the account was not actually disabled (err = %v)", err)
	}

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{"no such account", "nobody@example.test", goodPassword},
		{"wrong password", "founder@example.test", "a completely different one"},
		{"disabled account", "disabled@example.test", goodPassword},
		{"unparseable address", "not-an-address", goodPassword},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := svc.Authenticate(ctx, tc.email, tc.password)
			// Compared by identity, not by errors.Is: the three paths must return
			// the very same value, so no wrapping text can differ between them.
			if err != ErrBadCredentials { //nolint:errorlint // identity is the assertion
				t.Fatalf("Authenticate = %v (%T), want exactly ErrBadCredentials", err, err)
			}
			if u.ID != "" {
				t.Errorf("Authenticate returned a user (%q) alongside the error", u.ID)
			}
		})
	}
}

func TestAuthenticateAcceptsAnActiveAccount(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)

	// Mixed case and surrounding space, as typed into a login box.
	got, err := svc.Authenticate(ctx, " Founder@Example.Test ", goodPassword)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != admin.ID {
		t.Errorf("id = %q, want %q", got.ID, admin.ID)
	}
	if !got.IsAdmin {
		t.Error("IsAdmin = false, want true")
	}
}

// TestAuthenticateSurfacesAStoreFailure keeps the indistinguishable answer from
// swallowing an outage: a database that is down must not look like a wrong
// password, or the first symptom of a broken deployment is a support ticket
// about forgotten passwords.
func TestAuthenticateSurfacesAStoreFailure(t *testing.T) {
	boom := errors.New("database on fire")
	svc := NewService(brokenStore{err: boom})

	_, err := svc.Authenticate(context.Background(), "founder@example.test", goodPassword)
	if !errors.Is(err, boom) {
		t.Fatalf("Authenticate over a broken store = %v, want the store's error", err)
	}
	if errors.Is(err, ErrBadCredentials) {
		t.Error("a store failure was reported as bad credentials")
	}
}

// TestTheDummyHashIsRealAndAsExpensiveAsARealOne checks the timing-equalising
// comparison actually costs what a real one costs. A dummy hash at a lower cost,
// or a malformed one that bcrypt rejects before doing any work, would leave the
// enumeration timing side channel wide open while every other test still passed.
func TestTheDummyHashIsRealAndAsExpensiveAsARealOne(t *testing.T) {
	cost, err := bcrypt.Cost(dummyPasswordHash())
	if err != nil {
		t.Fatalf("the dummy hash is not a valid bcrypt hash: %v", err)
	}
	if cost != bcrypt.DefaultCost {
		t.Errorf("dummy hash cost = %d, want bcrypt.DefaultCost (%d)", cost, bcrypt.DefaultCost)
	}
	if err := bcrypt.CompareHashAndPassword(
		dummyPasswordHash(), []byte("whatever was typed")); err == nil {
		t.Error("the dummy hash matched an arbitrary password")
	}
}

func TestSetPasswordPermissions(t *testing.T) {
	newPassword := "an entirely new secret"

	t.Run("admin sets another account's", func(t *testing.T) {
		svc, _ := testService(t)
		ctx := context.Background()
		admin := bootstrapAdmin(t, svc)
		staff, err := svc.CreateUser(ctx, admin, "staff@example.test", goodPassword, "Staff", false)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		if err := svc.SetPassword(ctx, admin, staff.ID, newPassword); err != nil {
			t.Fatalf("SetPassword: %v", err)
		}
		if _, err := svc.Authenticate(ctx, staff.Email, newPassword); err != nil {
			t.Errorf("the new password does not sign in: %v", err)
		}
		if _, err := svc.Authenticate(ctx, staff.Email, goodPassword); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("the old password still signs in: %v", err)
		}
	})

	t.Run("a user sets their own", func(t *testing.T) {
		svc, _ := testService(t)
		ctx := context.Background()
		admin := bootstrapAdmin(t, svc)
		staff, err := svc.CreateUser(ctx, admin, "staff@example.test", goodPassword, "Staff", false)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		if err := svc.SetPassword(ctx, staff, staff.ID, newPassword); err != nil {
			t.Fatalf("SetPassword: %v", err)
		}
		if _, err := svc.Authenticate(ctx, staff.Email, newPassword); err != nil {
			t.Errorf("the new password does not sign in: %v", err)
		}
	})

	t.Run("a user may not set someone else's", func(t *testing.T) {
		svc, _ := testService(t)
		ctx := context.Background()
		admin := bootstrapAdmin(t, svc)
		staff, err := svc.CreateUser(ctx, admin, "staff@example.test", goodPassword, "Staff", false)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		other, err := svc.CreateUser(ctx, admin, "other@example.test", goodPassword, "Other", false)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		err = svc.SetPassword(ctx, staff, other.ID, newPassword)
		if !errors.Is(err, ErrNotPermitted) {
			t.Fatalf("SetPassword = %v, want an error wrapping ErrNotPermitted", err)
		}
		// The refusal must also not have changed anything.
		if _, err := svc.Authenticate(ctx, other.Email, goodPassword); err != nil {
			t.Errorf("the victim's password changed anyway: %v", err)
		}
	})

	t.Run("a zero actor is nobody, not the account with an empty id", func(t *testing.T) {
		svc, _ := testService(t)
		ctx := context.Background()

		err := svc.SetPassword(ctx, core.User{}, "", newPassword)
		if !errors.Is(err, ErrNotPermitted) {
			t.Fatalf("SetPassword = %v, want an error wrapping ErrNotPermitted", err)
		}
	})

	t.Run("a weak password is refused", func(t *testing.T) {
		svc, _ := testService(t)
		ctx := context.Background()
		admin := bootstrapAdmin(t, svc)

		err := svc.SetPassword(ctx, admin, admin.ID, "short")
		if !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("SetPassword = %v, want an error wrapping core.ErrInvalid", err)
		}
		if _, err := svc.Authenticate(ctx, admin.Email, goodPassword); err != nil {
			t.Errorf("the original password stopped working: %v", err)
		}
	})
}

// TestSetDisabledRefusesToRemoveTheLastActiveAdministrator guards the one
// mistake with no recovery path: there is no password reset and no console, so
// an installation that disables its last admin can never manage users again.
func TestSetDisabledRefusesToRemoveTheLastActiveAdministrator(t *testing.T) {
	svc, repo := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)

	err := svc.SetDisabled(ctx, admin, admin.ID, true)
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disabling the only admin = %v, want an error wrapping ErrLastAdmin", err)
	}
	if stored, err := repo.User(ctx, admin.ID); err != nil || !stored.Active() {
		t.Fatalf("the only admin was disabled anyway (err = %v)", err)
	}

	// A staff account is not an administrator, so it does not unlock the refusal.
	if _, err := svc.CreateUser(ctx, admin, "staff@example.test", goodPassword, "Staff", false); err != nil {
		t.Fatalf("CreateUser staff: %v", err)
	}
	if err := svc.SetDisabled(ctx, admin, admin.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("a non-admin account satisfied the last-admin check: %v", err)
	}

	// A second administrator does.
	second, err := svc.CreateUser(ctx, admin, "second@example.test", goodPassword, "Second", true)
	if err != nil {
		t.Fatalf("CreateUser second admin: %v", err)
	}
	if err := svc.SetDisabled(ctx, admin, admin.ID, true); err != nil {
		t.Fatalf("SetDisabled with two admins: %v", err)
	}

	// And now the survivor is the last one again.
	if err := svc.SetDisabled(ctx, second, second.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disabling the surviving admin = %v, want an error wrapping ErrLastAdmin", err)
	}
}

func TestSetDisabledBlocksAndRestoresSignIn(t *testing.T) {
	svc, repo := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)
	staff, err := svc.CreateUser(ctx, admin, "staff@example.test", goodPassword, "Staff", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := svc.SetDisabled(ctx, admin, staff.ID, true); err != nil {
		t.Fatalf("SetDisabled true: %v", err)
	}
	if _, err := svc.Authenticate(ctx, staff.Email, goodPassword); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("a disabled account still signs in: %v", err)
	}

	disabledAt, err := repo.User(ctx, staff.ID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if disabledAt.DisabledAt == nil {
		t.Fatal("DisabledAt = nil after disabling")
	}

	// Disabling twice must not move the timestamp: "since when" is an audit answer
	// and a repeated click must not overwrite it.
	if err := svc.SetDisabled(ctx, admin, staff.ID, true); err != nil {
		t.Fatalf("SetDisabled true again: %v", err)
	}
	again, err := repo.User(ctx, staff.ID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if !again.DisabledAt.Equal(*disabledAt.DisabledAt) {
		t.Errorf("DisabledAt moved from %v to %v on a repeated disable",
			*disabledAt.DisabledAt, *again.DisabledAt)
	}

	if err := svc.SetDisabled(ctx, admin, staff.ID, false); err != nil {
		t.Fatalf("SetDisabled false: %v", err)
	}
	restored, err := svc.Authenticate(ctx, staff.Email, goodPassword)
	if err != nil {
		t.Fatalf("a restored account cannot sign in: %v", err)
	}
	if restored.DisabledAt != nil {
		t.Errorf("DisabledAt = %v after restoring, want nil", restored.DisabledAt)
	}
}

func TestSetDisabledRequiresAnActiveAdministrator(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)
	staff, err := svc.CreateUser(ctx, admin, "staff@example.test", goodPassword, "Staff", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// A user cannot even disable themselves; user management is admin-only.
	if err := svc.SetDisabled(ctx, staff, staff.ID, true); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("SetDisabled by a non-admin = %v, want an error wrapping ErrNotPermitted", err)
	}
	if _, err := svc.Authenticate(ctx, staff.Email, goodPassword); err != nil {
		t.Errorf("the account was disabled anyway: %v", err)
	}
}

func TestSetDisabledAndSetPasswordReportAnUnknownAccount(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, svc)

	if err := svc.SetDisabled(ctx, admin, "no-such-id", true); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("SetDisabled on a missing id = %v, want an error wrapping core.ErrNotFound", err)
	}
	if err := svc.SetPassword(ctx, admin, "no-such-id", "a perfectly fine password"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("SetPassword on a missing id = %v, want an error wrapping core.ErrNotFound", err)
	}
}

// brokenStore is a core.UserStore whose lookups fail. The embedded interface is
// nil: any method this test does not exercise panics rather than quietly
// returning a zero value.
type brokenStore struct {
	core.UserStore
	err error
}

func (b brokenStore) UserByEmail(context.Context, string) (core.User, error) {
	return core.User{}, b.err
}
