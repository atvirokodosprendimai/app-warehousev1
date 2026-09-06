package submission

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// --- a driver that counts what the read handle actually executes ------------
//
// The N+1 rule is a claim about HOW MANY queries a page load issues, and no
// assertion about the returned data can distinguish one photo query from one per
// row — both produce the same submissions. Counting the statements is the only
// check that fails when [Repo.photosBySubmission] is replaced by a loop.

// countingDriver wraps another driver and records every statement executed
// through it.
type countingDriver struct {
	base  driver.Driver
	mu    sync.Mutex
	stmts []string
}

// counting is the wrapper registered as the "sqlite-counting" driver.
var counting = &countingDriver{}

func init() {
	// sql.Open does not connect, so this only resolves the registered driver.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic("resolve sqlite driver: " + err.Error())
	}
	counting.base = db.Driver()
	_ = db.Close()
	sql.Register("sqlite-counting", counting)
}

// Open wraps a connection from the base driver.
func (d *countingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	_, direct := c.(driver.QueryerContext)
	return &countingConn{Conn: c, d: d, direct: direct}, nil
}

// record notes one executed statement.
func (d *countingDriver) record(q string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stmts = append(d.stmts, q)
}

// reset forgets every recorded statement.
func (d *countingDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stmts = nil
}

// matching returns how many recorded statements contain sub.
func (d *countingDriver) matching(sub string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, q := range d.stmts {
		if strings.Contains(q, sub) {
			n++
		}
	}
	return n
}

// countingConn records the statements issued on one connection.
//
// direct says whether the wrapped connection answers QueryContext itself. When
// it does, recording in PrepareContext as well would count each query twice,
// because database/sql only falls back to preparing when QueryContext skips.
type countingConn struct {
	driver.Conn
	d      *countingDriver
	direct bool
}

// QueryContext records the query and forwards it.
func (c *countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.d.record(q)
	return qc.QueryContext(ctx, q, args)
}

// PrepareContext records the query when it is the path database/sql took.
func (c *countingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if !c.direct {
		c.d.record(q)
	}
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, q)
	}
	return c.Conn.Prepare(q)
}

// --- reads -------------------------------------------------------------------

func TestSubmissionReturnsPhotosInPositionOrder(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	u := insertUser(t, r, "u1", "staff@example.com", "Staff Person", false)

	sub := newSubmission("s1", u.ID, "Old radio", baseTime)
	if err := r.CreateSubmission(ctx, sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Inserted out of order on purpose: the ORDER BY is what must sort them.
	insertPhoto(t, r, "p-second", "s1", 1)
	insertPhoto(t, r, "p-first", "s1", 0)

	got, err := r.Submission(ctx, "s1")
	if err != nil {
		t.Fatalf("submission: %v", err)
	}
	want := []string{"p-first", "p-second"}
	if !equalStrings(photoIDs(got.Photos), want) {
		t.Fatalf("photo order = %v, want %v", photoIDs(got.Photos), want)
	}
	if got.Photos[0].Position != 0 || got.Photos[1].Position != 1 {
		t.Fatalf("positions = %d,%d, want 0,1",
			got.Photos[0].Position, got.Photos[1].Position)
	}
	if got.PrimaryPhoto().ID != "p-first" {
		t.Fatalf("primary photo = %q, want %q", got.PrimaryPhoto().ID, "p-first")
	}
}

func TestSubmissionWrapsErrNotFound(t *testing.T) {
	r := newTestRepo(t)
	_, err := r.Submission(context.Background(), "nope")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestSubmissionResolvesSubmitterNameByJoin(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	named := insertUser(t, r, "u1", "staff@example.com", "Rasa Kaunietė", false)
	unnamed := insertUser(t, r, "u2", "jonas@example.com", "", false)

	for _, tc := range []struct {
		id, subID, want string
		user            core.User
	}{
		{id: "named", subID: "s1", want: "Rasa Kaunietė", user: named},
		// core.User.Name falls back to the email's local part; the read model
		// must show the same name the rest of the app does.
		{id: "unnamed", subID: "s2", want: "jonas", user: unnamed},
	} {
		t.Run(tc.id, func(t *testing.T) {
			sub := newSubmission(tc.subID, tc.user.ID, "Thing", baseTime)
			if err := r.CreateSubmission(ctx, sub); err != nil {
				t.Fatalf("create: %v", err)
			}
			got, err := r.Submission(ctx, tc.subID)
			if err != nil {
				t.Fatalf("submission: %v", err)
			}
			if got.SubmitterName != tc.want {
				t.Fatalf("SubmitterName = %q, want %q", got.SubmitterName, tc.want)
			}
		})
	}
}

func TestSubmissionsLoadsEveryRowsPhotosInOneQuery(t *testing.T) {
	r := newTestRepoReadingThrough(t, "sqlite-counting")
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)

	// Three submissions, two photos each: enough that a per-row loader and a
	// batched one differ by three queries, and that a mis-grouping shows up as a
	// submission holding another's pictures.
	for i, id := range []string{"s1", "s2", "s3"} {
		sub := newSubmission(id, "u1", "Item "+id, baseTime.Add(time.Duration(i)*time.Minute))
		if err := r.CreateSubmission(ctx, sub); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		insertPhoto(t, r, id+"-b", id, 1)
		insertPhoto(t, r, id+"-a", id, 0)
	}

	counting.reset()
	got, err := r.Submissions(ctx, core.SubmissionFilter{})
	if err != nil {
		t.Fatalf("submissions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d submissions, want 3", len(got))
	}
	if n := counting.matching("FROM submission_photos"); n != 1 {
		t.Fatalf("photo queries = %d, want exactly 1 (an N+1 would issue 3)", n)
	}
	// And each row got ITS OWN photos, in order: one query is worthless if the
	// grouping in Go hands them to the wrong submission.
	for _, s := range got {
		want := []string{s.ID + "-a", s.ID + "-b"}
		if !equalStrings(photoIDs(s.Photos), want) {
			t.Fatalf("submission %s photos = %v, want %v", s.ID, photoIDs(s.Photos), want)
		}
	}
}

func TestSubmissionsNewestFirst(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)
	for i, id := range []string{"oldest", "middle", "newest"} {
		sub := newSubmission(id, "u1", id, baseTime.Add(time.Duration(i)*time.Hour))
		if err := r.CreateSubmission(ctx, sub); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	got, err := r.Submissions(ctx, core.SubmissionFilter{})
	if err != nil {
		t.Fatalf("submissions: %v", err)
	}
	want := []string{"newest", "middle", "oldest"}
	if !equalStrings(subIDs(got), want) {
		t.Fatalf("order = %v, want %v", subIDs(got), want)
	}
}

// seedInbox fills the repo with one submission per status plus a second
// submitter, which is the shape every filter test asserts against.
func seedInbox(t *testing.T, r *Repo) {
	t.Helper()
	ctx := context.Background()
	insertUser(t, r, "u1", "one@example.com", "One", false)
	insertUser(t, r, "u2", "two@example.com", "Two", false)
	insertOffer(t, r, "o1", "WH-EXISTING")

	rows := []struct {
		id, by string
		status core.SubmissionStatus
	}{
		{"s-new", "u1", core.SubmissionNew},
		{"s-reviewing", "u1", core.SubmissionReviewing},
		{"s-accepted", "u2", core.SubmissionAccepted},
		{"s-declined", "u2", core.SubmissionDeclined},
	}
	for i, row := range rows {
		sub := newSubmission(row.id, row.by, row.id, baseTime.Add(time.Duration(i)*time.Minute))
		sub.Status = row.status
		if row.status == core.SubmissionAccepted {
			sub.OfferID = "o1"
		}
		if row.status == core.SubmissionDeclined {
			sub.DeclineReason = "not something we can sell"
		}
		if err := r.CreateSubmission(ctx, sub); err != nil {
			t.Fatalf("create %s: %v", row.id, err)
		}
	}
}

func TestSubmissionsHonoursEveryFilterField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter core.SubmissionFilter
		want   []string
	}{
		{
			name:   "no filter returns everything, newest first",
			filter: core.SubmissionFilter{},
			want:   []string{"s-declined", "s-accepted", "s-reviewing", "s-new"},
		},
		{
			name:   "status is an IN list",
			filter: core.SubmissionFilter{Status: []core.SubmissionStatus{core.SubmissionNew, core.SubmissionDeclined}},
			want:   []string{"s-declined", "s-new"},
		},
		{
			name:   "submitted by one person",
			filter: core.SubmissionFilter{SubmittedBy: "u2"},
			want:   []string{"s-declined", "s-accepted"},
		},
		{
			name:   "open only is new plus reviewing",
			filter: core.SubmissionFilter{OpenOnly: true},
			want:   []string{"s-reviewing", "s-new"},
		},
		{
			name:   "open only combines with the submitter",
			filter: core.SubmissionFilter{OpenOnly: true, SubmittedBy: "u1"},
			want:   []string{"s-reviewing", "s-new"},
		},
		{
			name:   "limit takes the newest",
			filter: core.SubmissionFilter{Limit: 2},
			want:   []string{"s-declined", "s-accepted"},
		},
		{
			name:   "offset pages past them",
			filter: core.SubmissionFilter{Limit: 2, Offset: 2},
			want:   []string{"s-reviewing", "s-new"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t)
			seedInbox(t, r)
			got, err := r.Submissions(context.Background(), tc.filter)
			if err != nil {
				t.Fatalf("submissions: %v", err)
			}
			if !equalStrings(subIDs(got), tc.want) {
				t.Fatalf("got %v, want %v", subIDs(got), tc.want)
			}
		})
	}
}

func TestListQueryAppliesADefaultLimit(t *testing.T) {
	q, args := listQuery(core.SubmissionFilter{})
	if !strings.Contains(q, "LIMIT ? OFFSET ?") {
		t.Fatalf("query has no limit clause: %s", q)
	}
	if len(args) != 2 || args[0] != defaultLimit || args[1] != 0 {
		t.Fatalf("args = %v, want [%d 0]", args, defaultLimit)
	}
}

func TestCountOpenCountsNewAndReviewingOnly(t *testing.T) {
	r := newTestRepo(t)
	seedInbox(t, r)
	n, err := r.CountOpen(context.Background())
	if err != nil {
		t.Fatalf("count open: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountOpen = %d, want 2 (new + reviewing, not accepted or declined)", n)
	}
}

func TestCountOpenOnAnEmptyInboxIsZero(t *testing.T) {
	r := newTestRepo(t)
	n, err := r.CountOpen(context.Background())
	if err != nil {
		t.Fatalf("count open: %v", err)
	}
	if n != 0 {
		t.Fatalf("CountOpen = %d, want 0", n)
	}
}

// --- writes ------------------------------------------------------------------

func TestSubmissionRoundTripsEveryField(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)
	insertUser(t, r, "admin", "admin@example.com", "Admin", true)

	reviewed := baseTime.Add(2 * time.Hour)
	sub := newSubmission("s1", "u1", "Marantz amplifier", baseTime)
	sub.Note = "works, one scratch"
	sub.Asking = core.Money{Minor: 12050, Currency: "EUR"}
	sub.Status = core.SubmissionDeclined
	sub.DeclineReason = "we have three already"
	sub.ReviewedAt = &reviewed
	sub.ReviewedBy = "admin"
	if err := r.CreateSubmission(ctx, sub); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.Submission(ctx, "s1")
	if err != nil {
		t.Fatalf("submission: %v", err)
	}
	if got.Note != sub.Note {
		t.Fatalf("Note = %q, want %q", got.Note, sub.Note)
	}
	if got.Asking != sub.Asking {
		t.Fatalf("Asking = %v, want %v", got.Asking, sub.Asking)
	}
	if got.DeclineReason != sub.DeclineReason {
		t.Fatalf("DeclineReason = %q, want %q", got.DeclineReason, sub.DeclineReason)
	}
	if got.ReviewedBy != "admin" {
		t.Fatalf("ReviewedBy = %q, want %q", got.ReviewedBy, "admin")
	}
	if got.ReviewedAt == nil || !got.ReviewedAt.Equal(reviewed) {
		t.Fatalf("ReviewedAt = %v, want %v", got.ReviewedAt, reviewed)
	}
	if got.ReviewedAt.Location() != time.UTC {
		t.Fatalf("ReviewedAt location = %v, want UTC", got.ReviewedAt.Location())
	}
	if !got.CreatedAt.Equal(baseTime) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, baseTime)
	}
}

func TestSubmissionWithNoReviewHasANilReviewedAt(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)
	if err := r.CreateSubmission(ctx, newSubmission("s1", "u1", "Thing", baseTime)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.Submission(ctx, "s1")
	if err != nil {
		t.Fatalf("submission: %v", err)
	}
	if got.ReviewedAt != nil {
		t.Fatalf("ReviewedAt = %v, want nil", got.ReviewedAt)
	}
	if got.ReviewedBy != "" || got.OfferID != "" {
		t.Fatalf("ReviewedBy = %q, OfferID = %q, want both empty",
			got.ReviewedBy, got.OfferID)
	}
}

func TestUpdateSubmissionLeavesCreatedAtAndSubmitterAlone(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "one@example.com", "One", false)
	insertUser(t, r, "u2", "two@example.com", "Two", false)
	if err := r.CreateSubmission(ctx, newSubmission("s1", "u1", "Thing", baseTime)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A caller passing a form-shaped struct must not be able to rewrite when it
	// was sent or who sent it.
	edit := newSubmission("s1", "u2", "Thing renamed", baseTime.Add(99*time.Hour))
	if err := r.UpdateSubmission(ctx, edit); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := r.Submission(ctx, "s1")
	if err != nil {
		t.Fatalf("submission: %v", err)
	}
	if got.Title != "Thing renamed" {
		t.Fatalf("Title = %q, want the edit to have applied", got.Title)
	}
	if got.SubmittedBy != "u1" {
		t.Fatalf("SubmittedBy = %q, want %q", got.SubmittedBy, "u1")
	}
	if !got.CreatedAt.Equal(baseTime) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, baseTime)
	}
}

func TestUpdateSubmissionOnAMissingRowIsErrNotFound(t *testing.T) {
	r := newTestRepo(t)
	err := r.UpdateSubmission(context.Background(), newSubmission("ghost", "u1", "x", baseTime))
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteSubmissionPhotoOnAMissingRowIsErrNotFound(t *testing.T) {
	r := newTestRepo(t)
	err := r.DeleteSubmissionPhoto(context.Background(), "ghost")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestTheReadHandleRefusesToWrite(t *testing.T) {
	r := newTestRepo(t)
	// query_only(1) is what makes "reads go to read, writes go to write" a
	// property the driver enforces rather than a convention review has to catch.
	_, err := r.read.Exec(`INSERT INTO users (id, email, password_hash, created_at)
		VALUES ('x', 'x@example.com', 'x', '2026-09-06T10:00:00Z')`)
	if err == nil {
		t.Fatal("the read handle accepted a write; query_only(1) is not in effect")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("err = %v, want a readonly-database refusal", err)
	}
}

// --- the photo move ----------------------------------------------------------

func TestMovePhotosToOfferKeepsTheSamePhotoIDs(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)
	if err := r.CreateSubmission(ctx, newSubmission("s1", "u1", "Camera", baseTime)); err != nil {
		t.Fatalf("create: %v", err)
	}
	insertOffer(t, r, "o1", "WH-TARGET")
	// A second submission whose photos must not move.
	if err := r.CreateSubmission(ctx, newSubmission("s2", "u1", "Other", baseTime)); err != nil {
		t.Fatalf("create s2: %v", err)
	}
	insertPhoto(t, r, "keep-me-1", "s1", 0)
	insertPhoto(t, r, "keep-me-2", "s1", 1)
	insertPhoto(t, r, "stay-put", "s2", 0)

	before, err := r.Submission(ctx, "s1")
	if err != nil {
		t.Fatalf("submission before: %v", err)
	}
	beforeIDs := photoIDs(before.Photos)
	if !equalStrings(beforeIDs, []string{"keep-me-1", "keep-me-2"}) {
		t.Fatalf("setup: photo ids = %v", beforeIDs)
	}

	if err := r.MovePhotosToOffer(ctx, "s1", "o1"); err != nil {
		t.Fatalf("move: %v", err)
	}

	// ★ The ids must be IDENTICAL, not merely the same in number: each id is the
	// public URL a marketplace fetches and the name of the blob on disk, and
	// nothing moves the bytes.
	rows, err := r.read.QueryContext(ctx,
		`SELECT id, offer_id, position, filename, content_type, byte_size, sha256
		 FROM offer_photos WHERE offer_id = ? ORDER BY position`, "o1")
	if err != nil {
		t.Fatalf("read offer photos: %v", err)
	}
	defer rows.Close()
	var (
		gotIDs  []string
		gotPos  []int
		gotHash []string
	)
	for rows.Next() {
		var (
			id, offerID, filename, contentType, sha string
			position                                int
			size                                    int64
		)
		if err := rows.Scan(&id, &offerID, &position, &filename, &contentType, &size, &sha); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if offerID != "o1" {
			t.Fatalf("photo %s has offer_id %q, want %q", id, offerID, "o1")
		}
		if filename != id+".jpg" || contentType != "image/jpeg" {
			t.Fatalf("photo %s lost its metadata: filename=%q type=%q", id, filename, contentType)
		}
		gotIDs = append(gotIDs, id)
		gotPos = append(gotPos, position)
		gotHash = append(gotHash, sha)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if !equalStrings(gotIDs, beforeIDs) {
		t.Fatalf("offer photo ids = %v, want the SAME ids as before the move, %v",
			gotIDs, beforeIDs)
	}
	if len(gotPos) != 2 || gotPos[0] != 0 || gotPos[1] != 1 {
		t.Fatalf("positions = %v, want [0 1]", gotPos)
	}
	if !equalStrings(gotHash, []string{"hash-keep-me-1", "hash-keep-me-2"}) {
		t.Fatalf("hashes = %v, want them carried across unchanged", gotHash)
	}

	// The submission no longer holds them, and its neighbour is untouched.
	after, err := r.Submission(ctx, "s1")
	if err != nil {
		t.Fatalf("submission after: %v", err)
	}
	if len(after.Photos) != 0 {
		t.Fatalf("submission still holds %d photo(s) after the move", len(after.Photos))
	}
	other, err := r.Submission(ctx, "s2")
	if err != nil {
		t.Fatalf("submission s2: %v", err)
	}
	if !equalStrings(photoIDs(other.Photos), []string{"stay-put"}) {
		t.Fatalf("s2 photos = %v, want [stay-put]", photoIDs(other.Photos))
	}
}

func TestMovePhotosToOfferWithNoPhotosIsNotAnError(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)
	if err := r.CreateSubmission(ctx, newSubmission("s1", "u1", "Described only", baseTime)); err != nil {
		t.Fatalf("create: %v", err)
	}
	insertOffer(t, r, "o1", "WH-TARGET")

	if err := r.MovePhotosToOffer(ctx, "s1", "o1"); err != nil {
		t.Fatalf("move: %v, want nil — a submission with no picture is legitimate", err)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Fatalf("offer_photos = %d, want 0", n)
	}
}

func TestMovePhotosToOfferKeepsTheRowsWhenTheInsertFails(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	insertUser(t, r, "u1", "staff@example.com", "Staff", false)
	if err := r.CreateSubmission(ctx, newSubmission("s1", "u1", "Camera", baseTime)); err != nil {
		t.Fatalf("create: %v", err)
	}
	insertPhoto(t, r, "p1", "s1", 0)

	// No such offer: offer_photos.offer_id is a real foreign key, so the INSERT
	// fails and the DELETE must never happen. Without one transaction the photos
	// would be deleted from a submission that never reached an offer — which is
	// the only way to lose them for good.
	err := r.MovePhotosToOffer(ctx, "s1", "no-such-offer")
	if err == nil {
		t.Fatal("move onto a missing offer succeeded, want a foreign key failure")
	}
	if n := countRows(t, r, "submission_photos"); n != 1 {
		t.Fatalf("submission_photos = %d, want 1 — the failed move ate the row", n)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Fatalf("offer_photos = %d, want 0", n)
	}
}
