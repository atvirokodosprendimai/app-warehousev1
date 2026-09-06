package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/google/uuid"

	// modernc.org/sqlite is the CGO-free driver; it registers itself as "sqlite".
	_ "modernc.org/sqlite"
)

// usersDDL is the users table exactly as migrations/00001_init.sql declares it.
//
// It is copied rather than obtained by running the migrations, because this
// package must not depend on internal/store to be testable: a repository test
// that pulled in the whole application's schema would start failing for reasons
// that have nothing to do with the code under test.
const usersDDL = `
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    TEXT NOT NULL,
    disabled_at   TEXT
) STRICT;`

// testRepo returns a Repo over a fresh database, wired the way production is:
// the read handle carries query_only(1) and therefore CANNOT write.
//
// Every test in this package consequently proves the read/write routing as a
// side effect — a method that reached for the read handle to mutate something
// fails here rather than in production.
func testRepo(t *testing.T) *Repo {
	t.Helper()

	path := filepath.Join(t.TempDir(), "t.db")

	write, err := sql.Open("sqlite",
		"file:"+path+"?_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		t.Fatalf("open write handle: %v", err)
	}
	t.Cleanup(func() { write.Close() })
	// One connection, as internal/store caps the production writer: SQLite admits
	// a single writer anyway, and queueing in database/sql gives better
	// diagnostics than a SQLITE_BUSY from the driver.
	write.SetMaxOpenConns(1)

	if _, err := write.Exec(usersDDL); err != nil {
		t.Fatalf("create users table: %v", err)
	}

	// Opened after the schema exists: a query_only connection cannot create the
	// database file itself.
	read, err := sql.Open("sqlite",
		"file:"+path+"?_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatalf("open read handle: %v", err)
	}
	t.Cleanup(func() { read.Close() })

	return NewRepo(read, write)
}

// testUser builds a plausible account. The timestamp is truncated to the second
// because that is the resolution RFC3339 storage keeps, so a round trip compares
// exactly.
func testUser(email string) core.User {
	return core.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: "$2a$10$notarealhashbutastringallthesame",
		DisplayName:  "Test Person",
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
	}
}

// TestTheReadHandleRefusesWrites pins the premise every other test relies on.
//
// Without it, "reads go through the read handle" would be asserted by a fixture
// that cannot fail: if query_only were silently not in effect, a repository that
// wrote through r.read would pass every test in this file.
func TestTheReadHandleRefusesWrites(t *testing.T) {
	repo := testRepo(t)

	_, err := repo.read.ExecContext(context.Background(),
		"INSERT INTO users (id, email, password_hash, created_at) VALUES (?, ?, ?, ?)",
		"id", "someone@example.test", "hash", "2026-01-01T00:00:00Z")
	if err == nil {
		t.Fatal("the read handle accepted an INSERT; query_only(1) is not in effect " +
			"and the read/write split is unenforced")
	}
}

func TestCreateUserRoundTripsEveryColumn(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	disabled := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	want := testUser("someone@example.test")
	want.IsAdmin = true
	want.DisabledAt = &disabled

	if err := repo.CreateUser(ctx, want); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := repo.User(ctx, want.ID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	assertSameUser(t, "User", got, want)

	// The address is stored folded, so a login typed with capitals must resolve.
	got, err = repo.UserByEmail(ctx, "  SomeOne@Example.Test  ")
	if err != nil {
		t.Fatalf("UserByEmail with mixed case: %v", err)
	}
	assertSameUser(t, "UserByEmail", got, want)
}

func TestUserLookupsReportNotFound(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	if _, err := repo.User(ctx, "no-such-id"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("User on a missing id = %v, want an error wrapping core.ErrNotFound", err)
	}
	if _, err := repo.UserByEmail(ctx, "nobody@example.test"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UserByEmail on a missing address = %v, want an error wrapping core.ErrNotFound", err)
	}
}

func TestCreateUserRefusesADuplicateEmailRatherThanOverwriting(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	first := testUser("shared@example.test")
	if err := repo.CreateUser(ctx, first); err != nil {
		t.Fatalf("CreateUser first: %v", err)
	}

	second := testUser("shared@example.test")
	second.PasswordHash = "$2a$10$adifferenthashentirely00000000000"
	second.DisplayName = "Impostor"

	err := repo.CreateUser(ctx, second)
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("CreateUser with a taken address = %v, want an error wrapping ErrEmailTaken", err)
	}

	// "Fail rather than overwrite" is the whole point, so check the row too.
	kept, err := repo.UserByEmail(ctx, "shared@example.test")
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if kept.ID != first.ID {
		t.Errorf("id after the refused insert = %q, want the original %q", kept.ID, first.ID)
	}
	if kept.PasswordHash != first.PasswordHash {
		t.Errorf("password hash after the refused insert = %q, want the original %q",
			kept.PasswordHash, first.PasswordHash)
	}
	if n, err := repo.CountUsers(ctx); err != nil || n != 1 {
		t.Errorf("CountUsers = %d, %v; want 1, nil", n, err)
	}
}

func TestUsersListsOldestFirstAndCountUsersAgrees(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	emails := []string{"a@example.test", "b@example.test", "c@example.test"}
	for i, email := range emails {
		u := testUser(email)
		// Deliberately inserted newest first, so an unordered query would return
		// them the other way round.
		u.CreatedAt = base.Add(time.Duration(len(emails)-i) * time.Hour)
		if err := repo.CreateUser(ctx, u); err != nil {
			t.Fatalf("CreateUser %s: %v", email, err)
		}
	}

	users, err := repo.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	want := []string{"c@example.test", "b@example.test", "a@example.test"}
	if len(users) != len(want) {
		t.Fatalf("Users returned %d rows, want %d", len(users), len(want))
	}
	for i, email := range want {
		if users[i].Email != email {
			t.Errorf("Users[%d].Email = %q, want %q", i, users[i].Email, email)
		}
	}

	n, err := repo.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != len(want) {
		t.Errorf("CountUsers = %d, want %d", n, len(want))
	}
}

func TestUsersIsEmptyOnAFreshDatabase(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	users, err := repo.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("Users on a fresh database returned %d rows, want 0", len(users))
	}
	n, err := repo.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("CountUsers on a fresh database = %d, want 0", n)
	}
}

func TestUpdateUserRewritesTheMutableColumnsAndKeepsCreatedAt(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	disabled := time.Now().UTC().Truncate(time.Second)
	original := testUser("mutable@example.test")
	original.DisabledAt = &disabled
	if err := repo.CreateUser(ctx, original); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	changed := original
	changed.DisplayName = "Renamed"
	changed.PasswordHash = "$2a$10$arotatedhashvalue000000000000000"
	changed.IsAdmin = true
	changed.DisabledAt = nil
	changed.CreatedAt = original.CreatedAt.Add(100 * time.Hour) // must be ignored

	if err := repo.UpdateUser(ctx, changed); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	got, err := repo.User(ctx, original.ID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if got.DisplayName != "Renamed" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Renamed")
	}
	if got.PasswordHash != changed.PasswordHash {
		t.Errorf("PasswordHash = %q, want %q", got.PasswordHash, changed.PasswordHash)
	}
	if !got.IsAdmin {
		t.Error("IsAdmin = false, want true")
	}
	if got.DisabledAt != nil {
		t.Errorf("DisabledAt = %v, want nil", got.DisabledAt)
	}
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt = %v, want the original %v — created_at must not be rewritable",
			got.CreatedAt, original.CreatedAt)
	}
}

func TestUpdateUserReportsNotFoundForAnUnknownID(t *testing.T) {
	repo := testRepo(t)

	u := testUser("ghost@example.test")
	if err := repo.UpdateUser(context.Background(), u); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateUser on a missing id = %v, want an error wrapping core.ErrNotFound", err)
	}
}

func TestUpdateUserRefusesToTakeAnotherAccountsEmail(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	first := testUser("first@example.test")
	second := testUser("second@example.test")
	for _, u := range []core.User{first, second} {
		if err := repo.CreateUser(ctx, u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.Email, err)
		}
	}

	second.Email = "first@example.test"
	if err := repo.UpdateUser(ctx, second); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("UpdateUser onto a taken address = %v, want an error wrapping ErrEmailTaken", err)
	}
}

func TestCreateFirstAdminOnlyFillsAnEmptyTable(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	first := testUser("founder@example.test")
	first.IsAdmin = false // the statement must set the flag itself
	if err := repo.CreateFirstAdmin(ctx, first); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}

	stored, err := repo.User(ctx, first.ID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if !stored.IsAdmin {
		t.Error("the bootstrapped account is not an administrator")
	}
	if stored.DisabledAt != nil {
		t.Errorf("DisabledAt = %v, want nil", stored.DisabledAt)
	}

	err = repo.CreateFirstAdmin(ctx, testUser("latecomer@example.test"))
	if !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("second CreateFirstAdmin = %v, want an error wrapping ErrBootstrapClosed", err)
	}
	if n, err := repo.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("CountUsers = %d, %v; want 1, nil", n, err)
	}
}

// TestCreateFirstAdminIsAtomicUnderConcurrentCallers is the reason
// CreateFirstAdmin is one statement.
//
// A "SELECT count(*)" followed by a separate INSERT passes every sequential test
// above and still hands administrator rights to a stranger, because two requests
// arriving together both read zero. The barrier makes the callers genuinely
// overlap; run under -race.
func TestCreateFirstAdminIsAtomicUnderConcurrentCallers(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	const callers = 8
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  = make([]error, callers)
	)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := testUser(fmt.Sprintf("racer%d@example.test", i))
			<-start // release them all at once
			errs[i] = repo.CreateFirstAdmin(ctx, u)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrBootstrapClosed):
		default:
			t.Errorf("caller %d = %v, want nil or an error wrapping ErrBootstrapClosed", i, err)
		}
	}
	if winners != 1 {
		t.Errorf("%d of %d callers succeeded, want exactly 1", winners, callers)
	}

	n, err := repo.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 1 {
		t.Fatalf("users table holds %d rows after %d concurrent bootstraps, want exactly 1", n, callers)
	}

	users, err := repo.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if !users[0].IsAdmin {
		t.Error("the surviving account is not an administrator")
	}
}

// assertSameUser compares every field of a round-tripped account.
func assertSameUser(t *testing.T, what string, got, want core.User) {
	t.Helper()

	if got.ID != want.ID {
		t.Errorf("%s: ID = %q, want %q", what, got.ID, want.ID)
	}
	if got.Email != want.Email {
		t.Errorf("%s: Email = %q, want %q", what, got.Email, want.Email)
	}
	if got.PasswordHash != want.PasswordHash {
		t.Errorf("%s: PasswordHash = %q, want %q", what, got.PasswordHash, want.PasswordHash)
	}
	if got.DisplayName != want.DisplayName {
		t.Errorf("%s: DisplayName = %q, want %q", what, got.DisplayName, want.DisplayName)
	}
	if got.IsAdmin != want.IsAdmin {
		t.Errorf("%s: IsAdmin = %t, want %t", what, got.IsAdmin, want.IsAdmin)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("%s: CreatedAt = %v, want %v", what, got.CreatedAt, want.CreatedAt)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("%s: CreatedAt zone = %v, want UTC", what, got.CreatedAt.Location())
	}
	switch {
	case got.DisabledAt == nil && want.DisabledAt != nil:
		t.Errorf("%s: DisabledAt = nil, want %v", what, *want.DisabledAt)
	case got.DisabledAt != nil && want.DisabledAt == nil:
		t.Errorf("%s: DisabledAt = %v, want nil", what, *got.DisabledAt)
	case got.DisabledAt != nil && !got.DisabledAt.Equal(*want.DisabledAt):
		t.Errorf("%s: DisabledAt = %v, want %v", what, *got.DisabledAt, *want.DisabledAt)
	}
}
