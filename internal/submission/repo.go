// Package submission holds the staff-proposal aggregate: its persistence and its
// write side.
//
// A submission is something a staff user is offering to the warehouse. They
// photograph the thing, give it a name, optionally say what they hope to receive
// for it, and send it. It lands in an administrator's inbox as a message; the
// administrator rings them, agrees a price, and converts the submission into a
// real offer attached to a warehouse location.
//
// A submission is deliberately NOT an offer. A staff member has a photograph, a
// title and an idea of a price, and nothing else an export would need. Making
// them fill in an offer instead would either block the submission behind fields
// they cannot answer, or admit half-built offers into the catalogue where a
// marketplace export could pick one up. Nothing in this package can be exported:
// a proposal becomes sellable stock only when [Service.Accept] says so.
//
// The submitter is the item's OWNER, which is why core.Submission.Asking seeds
// the converted offer's OWNER price and never its shop price — what the person
// handing the thing over wants for it is exactly what core.Offer.Owner means.
//
// The package follows the project's CQRS split. [Repo] is the SQLite adapter
// implementing core.SubmissionStore; [Service] is the only thing that mutates a
// submission. A read model is handed a core.SubmissionReader and therefore
// cannot write, and the reader database handle carries query_only(1) so the
// driver refuses a write even if the compiler were somehow persuaded to allow
// one.
package submission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// defaultLimit bounds an inbox listing whose filter asked for no limit. An
// unbounded query is fine on the day it is written and loads every submission
// ever sent a year later, so the repository refuses to have no ceiling at all.
const defaultLimit = 100

// timeLayout is the on-disk timestamp format. Every stored time is RFC3339 in
// UTC, so the text sorts in the same order as the instants it names — which is
// what lets "newest first" be an ORDER BY on a TEXT column.
const timeLayout = time.RFC3339

// submissionColumns is the select list for a submission row.
//
// It is aliased to s and carries the submitter's name columns from the joined
// users row, because the inbox shows who sent what on every line and a second
// lookup per row is the difference between one query and a page of them.
const submissionColumns = `s.id, s.submitted_by, s.title, s.note,
	s.asking_minor, s.asking_currency, s.status, s.decline_reason, s.offer_id,
	s.created_at, s.reviewed_at, s.reviewed_by, u.display_name, u.email`

// submitterJoin resolves the submitter's display name.
//
// It is a LEFT JOIN even though submitted_by is NOT NULL and references users:
// an INNER JOIN would make a submission whose user row somehow went missing
// vanish from the inbox entirely, which is the one failure mode nobody would
// notice. A missing name renders as empty; a missing submission does not render
// at all.
const submitterJoin = ` LEFT JOIN users u ON u.id = s.submitted_by`

// photoColumns is the select list for a submission photo row.
const photoColumns = `id, submission_id, position, filename, content_type,
	byte_size, sha256, created_at`

// Repo is the SQLite-backed submission store.
//
// It holds two handles onto the same database file: reads go through read,
// which is a concurrent pool, and every mutation goes through write, which is
// the single writer. Nothing in this type writes through read.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// Repo must satisfy the whole port; this fails the build rather than a request
// if a method signature drifts from core.
var _ core.SubmissionStore = (*Repo)(nil)

// NewRepo returns a Repo reading through read and writing through write.
//
// The two handles are expected to address the same database file with different
// pragmas, as internal/store.Open produces them.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// Submission returns the submission with this id, photos included in position
// order, or core.ErrNotFound.
func (r *Repo) Submission(ctx context.Context, id string) (core.Submission, error) {
	q := "SELECT " + submissionColumns + " FROM submissions s" + submitterJoin +
		" WHERE s.id = ?"
	s, err := scanSubmission(r.read.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return core.Submission{}, fmt.Errorf("submission %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return core.Submission{}, fmt.Errorf("submission %s: %w", id, err)
	}
	// Photos are loaded here rather than left to the caller because the read
	// model is a function of the id alone. A caller that had to remember a
	// second call would remember it on one code path and forget it on the other,
	// and the one that forgets shows an administrator a decision about a thing
	// they cannot see.
	photos, err := r.photosBySubmission(ctx, []string{s.ID})
	if err != nil {
		return core.Submission{}, err
	}
	s.Photos = photos[s.ID]
	return s, nil
}

// Submissions lists the submissions matching f, newest first, each with its
// photos in position order.
//
// Every field of f narrows the result and a zero f means everything, bounded by
// [defaultLimit].
func (r *Repo) Submissions(ctx context.Context, f core.SubmissionFilter) ([]core.Submission, error) {
	q, args := listQuery(f)

	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("submission: list: %w", err)
	}
	defer rows.Close()

	var (
		out []core.Submission
		ids []string
	)
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, fmt.Errorf("submission: list: %w", err)
		}
		out = append(out, s)
		ids = append(ids, s.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: list: %w", err)
	}

	// One photo query for the whole page, never one per row. An N+1 here is
	// invisible on the handful of rows a developer tests with and becomes
	// several hundred round trips per inbox render once the warehouse is real.
	bySubmission, err := r.photosBySubmission(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Photos = bySubmission[out[i].ID]
	}
	return out, nil
}

// listQuery builds the SELECT and its arguments for a filter.
//
// It is separate from [Repo.Submissions] so the SQL can be reasoned about — and
// tested — without a database round trip in the way.
func listQuery(f core.SubmissionFilter) (string, []any) {
	var (
		where []string
		args  []any
	)

	if len(f.Status) > 0 {
		clause, a := statusIn(f.Status)
		where = append(where, clause)
		args = append(args, a...)
	}

	if by := strings.TrimSpace(f.SubmittedBy); by != "" {
		where = append(where, "s.submitted_by = ?")
		args = append(args, by)
	}

	if f.OpenOnly {
		clause, a := statusIn(openStatuses())
		where = append(where, clause)
		args = append(args, a...)
	}

	q := "SELECT " + submissionColumns + " FROM submissions s" + submitterJoin
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}

	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	// id breaks the tie so that two submissions sent during the same second come
	// back in a stable order; without it a page boundary could show one row
	// twice and skip another.
	q += " ORDER BY s.created_at DESC, s.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	return q, args
}

// CountOpen returns how many submissions still await a decision.
//
// It is a single COUNT rather than a listing the caller measures, because the
// live inbox banner polls it on every page: fetching every open submission and
// its photographs to learn a number would make the cheapest thing on the page
// the most expensive query behind it.
func (r *Repo) CountOpen(ctx context.Context) (int, error) {
	clause, args := statusIn(openStatuses())
	var n int
	err := r.read.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM submissions s WHERE "+clause, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("submission: count open: %w", err)
	}
	return n, nil
}

// CreateSubmission inserts a new submission.
//
// SubmitterName is not written: it is denormalised for display only and is
// resolved from the users table on every read, so there is no copy here to go
// stale when somebody changes their display name.
func (r *Repo) CreateSubmission(ctx context.Context, s core.Submission) error {
	const q = `INSERT INTO submissions (id, submitted_by, title, note, asking_minor,
		asking_currency, status, decline_reason, offer_id, created_at, reviewed_at,
		reviewed_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.write.ExecContext(ctx, q,
		s.ID, s.SubmittedBy, s.Title, s.Note, s.Asking.Minor, s.Asking.Currency,
		string(s.Status), s.DeclineReason, nullString(s.OfferID),
		formatTime(s.CreatedAt), nullTime(s.ReviewedAt), nullString(s.ReviewedBy))
	if err != nil {
		return fmt.Errorf("submission: create: %w", err)
	}
	return nil
}

// UpdateSubmission rewrites every mutable column of an existing submission and
// returns core.ErrNotFound when there is no such row.
//
// created_at and submitted_by are deliberately not in the SET list: when it was
// sent and who sent it are not things a triage decision may change, and leaving
// them out also means a caller passing a partially-filled struct cannot erase
// them.
func (r *Repo) UpdateSubmission(ctx context.Context, s core.Submission) error {
	const q = `UPDATE submissions SET title = ?, note = ?, asking_minor = ?,
		asking_currency = ?, status = ?, decline_reason = ?, offer_id = ?,
		reviewed_at = ?, reviewed_by = ? WHERE id = ?`
	res, err := r.write.ExecContext(ctx, q,
		s.Title, s.Note, s.Asking.Minor, s.Asking.Currency, string(s.Status),
		s.DeclineReason, nullString(s.OfferID), nullTime(s.ReviewedAt),
		nullString(s.ReviewedBy), s.ID)
	if err != nil {
		return fmt.Errorf("submission: update %s: %w", s.ID, err)
	}
	return checkAffected(res, "update", s.ID)
}

// AddSubmissionPhoto inserts a photo row.
//
// ⚠ The photo's owning submission travels in core.Photo.OfferID. core.Photo has
// one parent field and it is named for the aggregate it usually belongs to; a
// submission photo is the same row shape with a different parent, and it becomes
// a genuine offer photo — id unchanged — at [Repo.MovePhotosToOffer].
//
// The bytes are written separately and FIRST; see [Service.AddPhoto] for why
// that order is not negotiable.
func (r *Repo) AddSubmissionPhoto(ctx context.Context, p core.Photo) error {
	const q = `INSERT INTO submission_photos (` + photoColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.write.ExecContext(ctx, q,
		p.ID, p.OfferID, p.Position, p.Filename, p.ContentType, p.ByteSize,
		p.SHA256, formatTime(p.CreatedAt))
	if err != nil {
		return fmt.Errorf("submission: add photo: %w", err)
	}
	return nil
}

// DeleteSubmissionPhoto removes a photo row, or returns core.ErrNotFound.
func (r *Repo) DeleteSubmissionPhoto(ctx context.Context, photoID string) error {
	res, err := r.write.ExecContext(ctx,
		`DELETE FROM submission_photos WHERE id = ?`, photoID)
	if err != nil {
		return fmt.Errorf("submission: delete photo %s: %w", photoID, err)
	}
	return checkAffected(res, "delete photo", photoID)
}

// MovePhotosToOffer re-parents a submission's photographs onto an offer,
// KEEPING EACH PHOTO'S ID, in one transaction.
//
// ★ The id is the photograph's public URL segment — the address a marketplace
// fetches the image from, and the address of the blob on disk. Minting new ids
// would break any link that has already gone out AND orphan every blob, because
// nothing in this system moves the bytes: the rows move, the files stay exactly
// where [Service.AddPhoto] put them. The INSERT ... SELECT carries id, position,
// filename, content type, size, hash and creation time across unchanged, so the
// only thing that differs after the move is which parent the row names.
//
// A submission with no photographs moves nothing and is not an error: sending a
// description of an item you are about to bring in is a legitimate submission,
// and refusing to convert it would be refusing the admin's decision over a
// missing picture.
func (r *Repo) MovePhotosToOffer(ctx context.Context, submissionID, offerID string) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("submission: move photos: %w", err)
	}
	// Rollback after a successful Commit returns sql.ErrTxDone, which is the
	// normal path and not a failure worth reporting.
	defer func() { _ = tx.Rollback() }()

	const insert = `INSERT INTO offer_photos
		(id, offer_id, position, filename, content_type, byte_size, sha256, created_at)
		SELECT id, ?, position, filename, content_type, byte_size, sha256, created_at
		FROM submission_photos WHERE submission_id = ?`
	if _, err := tx.ExecContext(ctx, insert, offerID, submissionID); err != nil {
		return fmt.Errorf("submission: move photos to offer %s: %w", offerID, err)
	}

	const del = `DELETE FROM submission_photos WHERE submission_id = ?`
	if _, err := tx.ExecContext(ctx, del, submissionID); err != nil {
		return fmt.Errorf("submission: move photos from %s: %w", submissionID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("submission: move photos: commit: %w", err)
	}
	return nil
}

// photosBySubmission loads the photos of every listed submission in ONE query
// and groups them in Go, keyed by submission id and ordered by position.
//
// The IN list carries one placeholder per submission on the page, so the
// caller's page size is what keeps it inside SQLite's variable limit.
func (r *Repo) photosBySubmission(ctx context.Context, submissionIDs []string) (map[string][]core.Photo, error) {
	out := make(map[string][]core.Photo, len(submissionIDs))
	if len(submissionIDs) == 0 {
		return out, nil
	}

	ph := make([]string, len(submissionIDs))
	args := make([]any, len(submissionIDs))
	for i, id := range submissionIDs {
		ph[i] = "?"
		args[i] = id
	}
	q := `SELECT ` + photoColumns + ` FROM submission_photos WHERE submission_id IN (` +
		strings.Join(ph, ", ") + `) ORDER BY submission_id, position, id`

	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("submission: load photos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			p       core.Photo
			created string
		)
		// OfferID receives submission_id: see [Repo.AddSubmissionPhoto].
		if err := rows.Scan(&p.ID, &p.OfferID, &p.Position, &p.Filename,
			&p.ContentType, &p.ByteSize, &p.SHA256, &created); err != nil {
			return nil, fmt.Errorf("submission: load photos: %w", err)
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out[p.OfferID] = append(out[p.OfferID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("submission: load photos: %w", err)
	}
	return out, nil
}

// rowScanner is the one method *sql.Row and *sql.Rows have in common, so that a
// single-row read and a page read share one scan.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSubmission reads one submission row, including the joined submitter name.
// Photos are not its business; the caller populates them.
func scanSubmission(sc rowScanner) (core.Submission, error) {
	var (
		s           core.Submission
		status      string
		offerID     sql.NullString
		created     string
		reviewedAt  sql.NullString
		reviewedBy  sql.NullString
		displayName sql.NullString
		email       sql.NullString
	)
	err := sc.Scan(&s.ID, &s.SubmittedBy, &s.Title, &s.Note, &s.Asking.Minor,
		&s.Asking.Currency, &status, &s.DeclineReason, &offerID, &created,
		&reviewedAt, &reviewedBy, &displayName, &email)
	if err != nil {
		return core.Submission{}, err
	}
	s.Status = core.SubmissionStatus(status)
	s.OfferID = offerID.String
	s.ReviewedBy = reviewedBy.String
	// The display fallback is core.User.Name's rule, applied by calling it rather
	// than by rewriting it as a SQL COALESCE — two copies of "which name do we
	// show" would drift, and the one in SQL is the copy nobody tests.
	u := core.User{DisplayName: displayName.String, Email: email.String}
	s.SubmitterName = u.Name()
	if s.CreatedAt, err = parseTime(created); err != nil {
		return core.Submission{}, err
	}
	if reviewedAt.Valid {
		t, err := parseTime(reviewedAt.String)
		if err != nil {
			return core.Submission{}, err
		}
		s.ReviewedAt = &t
	}
	return s, nil
}

// openStatuses returns the triage states that still need a decision.
//
// It is derived from the domain rather than written out as SQL literals, so that
// a status added to core.SubmissionStatus cannot leave the inbox count behind
// counting the wrong thing while still returning a plausible number.
func openStatuses() []core.SubmissionStatus {
	var out []core.SubmissionStatus
	for _, s := range core.SubmissionStatuses() {
		if s.Open() {
			out = append(out, s)
		}
	}
	return out
}

// statusIn builds an "s.status IN (…)" clause and its arguments.
func statusIn(sts []core.SubmissionStatus) (string, []any) {
	ph := make([]string, len(sts))
	args := make([]any, len(sts))
	for i, s := range sts {
		ph[i] = "?"
		args[i] = string(s)
	}
	return "s.status IN (" + strings.Join(ph, ", ") + ")", args
}

// formatTime renders t for storage: RFC3339, in UTC, to the second.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp back as UTC.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("submission: parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// nullString maps Go's empty string to SQL NULL.
//
// offer_id and reviewed_by are foreign keys, so an empty string would be a
// reference resolving to nothing rather than the "not yet" the domain means.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullTime maps a nil review date to SQL NULL, which is how "nobody has looked
// at it" is stored.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// checkAffected turns a statement that matched no row into core.ErrNotFound, so
// that a caller cannot mistake "there was nothing to change" for success.
func checkAffected(res sql.Result, op, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("submission: %s %s: %w", op, id, err)
	}
	if n == 0 {
		return fmt.Errorf("submission: %s %s: %w", op, id, core.ErrNotFound)
	}
	return nil
}
