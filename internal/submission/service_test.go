package submission

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// errBoom is the injected failure the partial-failure tests use.
var errBoom = errors.New("boom")

// newTestService wires a Service over a real repository, an offer writer backed
// by the same database, and an in-memory blob store. It returns all three,
// because most assertions are about what landed where.
func newTestService(t *testing.T) (*Service, *Repo, *fakeOffers, *fakeBlobs) {
	t.Helper()
	r := newTestRepo(t)
	offers := newDBOffers(r)
	blobs := newFakeBlobs()
	return NewService(r, offers, blobs), r, offers, blobs
}

// staffAndAdmin seeds the two accounts every service test needs.
func staffAndAdmin(t *testing.T, r *Repo) (staff, admin core.User) {
	t.Helper()
	staff = insertUser(t, r, "u-staff", "staff@example.com", "Staff Person", false)
	admin = insertUser(t, r, "u-admin", "admin@example.com", "Admin Person", true)
	return staff, admin
}

// --- submitting --------------------------------------------------------------

func TestSubmitOpensANewSubmissionInTheInbox(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, _ := staffAndAdmin(t, r)

	got, err := svc.Submit(ctx, staff, "  Marantz amplifier  ", "  one scratch  ",
		core.Money{Minor: 15000, Currency: "EUR"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got.Status != core.SubmissionNew {
		t.Fatalf("Status = %q, want %q", got.Status, core.SubmissionNew)
	}
	if got.ID == "" {
		t.Fatal("Submit returned an empty id")
	}
	if got.Title != "Marantz amplifier" || got.Note != "one scratch" {
		t.Fatalf("Title = %q, Note = %q, want them trimmed", got.Title, got.Note)
	}
	if got.SubmitterName != "Staff Person" {
		t.Fatalf("SubmitterName = %q, want %q", got.SubmitterName, "Staff Person")
	}

	stored, err := r.Submission(ctx, got.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.SubmittedBy != staff.ID {
		t.Fatalf("SubmittedBy = %q, want %q", stored.SubmittedBy, staff.ID)
	}
	if stored.Asking != (core.Money{Minor: 15000, Currency: "EUR"}) {
		t.Fatalf("Asking = %v, want 15000 EUR", stored.Asking)
	}
	if n, err := r.CountOpen(ctx); err != nil || n != 1 {
		t.Fatalf("CountOpen = %d (err %v), want 1", n, err)
	}
}

func TestSubmitRefusesASubmissionWithNoTitle(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	staff, _ := staffAndAdmin(t, r)

	_, err := svc.Submit(context.Background(), staff, "   ", "", core.Money{})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err = %v, want core.ErrInvalid", err)
	}
	if n := countRows(t, r, "submissions"); n != 0 {
		t.Fatalf("submissions = %d, want 0", n)
	}
}

func TestSubmitAcceptsNoAskingPrice(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	staff, _ := staffAndAdmin(t, r)

	// "What will you give me for this" is a legitimate submission.
	got, err := svc.Submit(context.Background(), staff, "Box of cables", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !got.Asking.IsZero() {
		t.Fatalf("Asking = %v, want zero", got.Asking)
	}
}

// --- photographs -------------------------------------------------------------

func TestAddPhotoWritesTheBlobBeforeTheRow(t *testing.T) {
	svc, r, _, blobs := newTestService(t)
	ctx := context.Background()
	staff, _ := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// The blob store refuses. If the row were written first, the submission
	// would now carry a photograph whose URL 404s — for the administrator
	// deciding on it, and then for a marketplace once it is accepted.
	blobs.putErr = errBoom
	_, err = svc.AddPhoto(ctx, sub.ID, "front.jpg", "image/jpeg", []byte("bytes"))
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the blob failure", err)
	}
	if n := countRows(t, r, "submission_photos"); n != 0 {
		t.Fatalf("submission_photos = %d, want 0 — the row was written before the blob", n)
	}

	stored, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(stored.Photos) != 0 {
		t.Fatalf("submission holds %d photo(s) after a failed blob write", len(stored.Photos))
	}
}

func TestAddPhotoAppendsAfterThePhotosAlreadyThere(t *testing.T) {
	svc, r, _, blobs := newTestService(t)
	ctx := context.Background()
	staff, _ := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	first, err := svc.AddPhoto(ctx, sub.ID, "front.jpg", "image/jpeg", []byte("aaa"))
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	second, err := svc.AddPhoto(ctx, sub.ID, "back.png", "IMAGE/PNG", []byte("bbbb"))
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if first.Position != 0 || second.Position != 1 {
		t.Fatalf("positions = %d,%d, want 0,1", first.Position, second.Position)
	}
	if second.ContentType != "image/png" {
		t.Fatalf("ContentType = %q, want it lowercased", second.ContentType)
	}
	if second.ByteSize != 4 {
		t.Fatalf("ByteSize = %d, want 4", second.ByteSize)
	}
	// sha256 of "aaa", so a re-upload of the same bytes is detectable.
	const wantSum = "9834876dcfb05cb167a5c24953eba58c4ac89b1adf57f28f2f9d09af107ee8f0"
	if first.SHA256 != wantSum {
		t.Fatalf("SHA256 = %q, want %q", first.SHA256, wantSum)
	}
	if blobs.count() != 2 {
		t.Fatalf("stored blobs = %d, want 2", blobs.count())
	}

	stored, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !equalStrings(photoIDs(stored.Photos), []string{first.ID, second.ID}) {
		t.Fatalf("photos = %v, want [%s %s]", photoIDs(stored.Photos), first.ID, second.ID)
	}
}

func TestAddPhotoRefusesWhatAMarketplaceCannotRender(t *testing.T) {
	svc, r, _, blobs := newTestService(t)
	ctx := context.Background()
	staff, _ := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	for _, tc := range []struct {
		name        string
		contentType string
		data        []byte
	}{
		{"a pdf is not an image", "application/pdf", []byte("%PDF")},
		{"a tiff no marketplace fetches", "image/tiff", []byte("II*")},
		{"no content type at all", "", []byte("x")},
		{"an empty body", "image/jpeg", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.AddPhoto(ctx, sub.ID, "f", tc.contentType, tc.data)
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("err = %v, want core.ErrInvalid", err)
			}
		})
	}
	if n := countRows(t, r, "submission_photos"); n != 0 {
		t.Fatalf("submission_photos = %d, want 0", n)
	}
	if blobs.count() != 0 {
		t.Fatalf("stored blobs = %d, want 0 — a refused photo must not reach the store",
			blobs.count())
	}
}

func TestAddPhotoToAMissingSubmissionIsErrNotFound(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.AddPhoto(context.Background(), "ghost", "f.jpg", "image/jpeg", []byte("x"))
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

// --- triage ------------------------------------------------------------------

func TestStartReviewMovesNewToReviewingAndStampsTheReviewer(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := svc.StartReview(ctx, admin, sub.ID); err != nil {
		t.Fatalf("start review: %v", err)
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionReviewing {
		t.Fatalf("Status = %q, want %q", got.Status, core.SubmissionReviewing)
	}
	if got.ReviewedBy != admin.ID {
		t.Fatalf("ReviewedBy = %q, want %q", got.ReviewedBy, admin.ID)
	}
	if got.ReviewedAt == nil {
		t.Fatal("ReviewedAt is nil, want the pick-up stamped")
	}
	// Still open: somebody is on the phone about it, nobody has decided.
	if n, err := r.CountOpen(ctx); err != nil || n != 1 {
		t.Fatalf("CountOpen = %d (err %v), want 1", n, err)
	}
}

func TestStartReviewOnASubmissionAlreadyUnderReviewKeepsTheFirstReviewer(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	second := insertUser(t, r, "u-admin2", "admin2@example.com", "Second Admin", true)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := svc.StartReview(ctx, admin, sub.ID); err != nil {
		t.Fatalf("first start review: %v", err)
	}

	// The inbox is shared and the button sits on a page that may be stale.
	if err := svc.StartReview(ctx, second, sub.ID); err != nil {
		t.Fatalf("second start review: %v, want a no-op", err)
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ReviewedBy != admin.ID {
		t.Fatalf("ReviewedBy = %q, want the first reviewer %q", got.ReviewedBy, admin.ID)
	}
}

func TestStartReviewRefusesASubmissionAlreadyDecided(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := svc.Decline(ctx, admin, sub.ID, "we have three already"); err != nil {
		t.Fatalf("decline: %v", err)
	}

	err = svc.StartReview(ctx, admin, sub.ID)
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err = %v, want core.ErrInvalid", err)
	}
}

func TestDeclineClosesTheSubmissionWithItsReason(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := svc.Decline(ctx, admin, sub.ID, "  we have three already  "); err != nil {
		t.Fatalf("decline: %v", err)
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionDeclined {
		t.Fatalf("Status = %q, want %q", got.Status, core.SubmissionDeclined)
	}
	if got.ReviewNote != "we have three already" {
		t.Fatalf("ReviewNote = %q, want it trimmed", got.ReviewNote)
	}
	if got.ReviewedBy != admin.ID {
		t.Fatalf("ReviewedBy = %q, want %q", got.ReviewedBy, admin.ID)
	}
	if n, err := r.CountOpen(ctx); err != nil || n != 0 {
		t.Fatalf("CountOpen = %d (err %v), want 0", n, err)
	}
}

// TestAnEmptyReviewNoteIsRefusedAllTheWayDown checks the same rule at each of
// the three layers that hold it. A bare "no" produces the same submission again
// next week, and a rule enforced in one place is a rule the next write path
// skips.
func TestAnEmptyReviewNoteIsRefusedAllTheWayDown(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	t.Run("the service refuses it", func(t *testing.T) {
		err := svc.Decline(ctx, admin, sub.ID, "   ")
		if !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("err = %v, want core.ErrInvalid", err)
		}
		got, err := r.Submission(ctx, sub.ID)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if got.Status != core.SubmissionNew {
			t.Fatalf("Status = %q, want it untouched at %q", got.Status, core.SubmissionNew)
		}
	})

	t.Run("the domain refuses it", func(t *testing.T) {
		bad := core.Submission{
			ID: "x", SubmittedBy: "u-staff", Title: "Camera",
			Status: core.SubmissionDeclined,
		}
		if err := bad.Validate(); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("Validate = %v, want core.ErrInvalid", err)
		}
	})

	t.Run("the schema refuses it", func(t *testing.T) {
		// Straight past the domain, into the table's CHECK constraint.
		bad := newSubmission("s-bad", "u-staff", "Camera", baseTime)
		bad.Status = core.SubmissionDeclined
		if err := r.CreateSubmission(ctx, bad); err == nil {
			t.Fatal("the database accepted a declined submission with no reason")
		}
	})
}

func TestDeclineRefusesASubmissionAlreadyAccepted(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	insertLocation(t, r, "loc1", "KAUNAS", "KAUNAS")
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := svc.Accept(ctx, admin, sub.ID, conversion()); err != nil {
		t.Fatalf("accept: %v", err)
	}

	err = svc.Decline(ctx, admin, sub.ID, "changed my mind")
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err = %v, want core.ErrInvalid", err)
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionAccepted || got.OfferID != offers.only(t).ID {
		t.Fatalf("Status = %q, OfferID = %q, want it still accepted on its offer",
			got.Status, got.OfferID)
	}
}

func TestEveryAdminOnlyMethodRefusesANonAdmin(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, _ := staffAndAdmin(t, r)
	insertLocation(t, r, "loc1", "KAUNAS", "KAUNAS")
	sub, err := svc.Submit(ctx, staff, "Camera", "", core.Money{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"StartReview", func() error { return svc.StartReview(ctx, staff, sub.ID) }},
		{"Decline", func() error { return svc.Decline(ctx, staff, sub.ID, "no") }},
		{"Accept", func() error {
			_, err := svc.Accept(ctx, staff, sub.ID, conversion())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, ErrNotPermitted) {
				t.Fatalf("err = %v, want ErrNotPermitted", err)
			}
		})
	}

	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionNew {
		t.Fatalf("Status = %q, want it untouched at %q", got.Status, core.SubmissionNew)
	}
	if offers.count() != 0 {
		t.Fatalf("offers created = %d, want 0", offers.count())
	}
}

// --- conversion --------------------------------------------------------------

// conversion returns terms an administrator might have agreed on the phone.
func conversion() core.Conversion {
	return core.Conversion{
		Shop:        core.Money{Minor: 20000, Currency: "EUR"},
		Owner:       core.Money{Minor: 12000, Currency: "EUR"},
		LocationID:  "loc1",
		Condition:   "used - good",
		Description: "Serviced last year.",
	}
}

// acceptable seeds a submission with two photographs, ready to convert.
func acceptable(t *testing.T, svc *Service, r *Repo, staff core.User) core.Submission {
	t.Helper()
	ctx := context.Background()
	insertLocation(t, r, "loc1", "KAUNAS", "KAUNAS")
	sub, err := svc.Submit(ctx, staff, "Marantz amplifier", "one scratch",
		core.Money{Minor: 15000, Currency: "EUR"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, b := range [][]byte{[]byte("front"), []byte("back")} {
		if _, err := svc.AddPhoto(ctx, sub.ID, "p.jpg", "image/jpeg", b); err != nil {
			t.Fatalf("add photo: %v", err)
		}
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return got
}

func TestAcceptConvertsTheSubmissionIntoADraftOfferCarryingItsPhotos(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)
	wantPhotoIDs := photoIDs(sub.Photos)

	o, err := svc.Accept(ctx, admin, sub.ID, conversion())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	if o.Status != core.StatusDraft {
		t.Fatalf("Status = %q, want %q — a conversion settles the price, not the "+
			"decision to publish", o.Status, core.StatusDraft)
	}
	if o.Status.Exportable() {
		t.Fatal("the converted offer is exportable; a submission must never reach a marketplace by conversion alone")
	}
	if o.Title != "Marantz amplifier" {
		t.Fatalf("Title = %q, want the submission's title", o.Title)
	}
	if o.Description != "Serviced last year." || o.Condition != "used - good" {
		t.Fatalf("Description = %q, Condition = %q, want the conversion's",
			o.Description, o.Condition)
	}
	if o.Shop != (core.Money{Minor: 20000, Currency: "EUR"}) {
		t.Fatalf("Shop = %v, want 20000 EUR", o.Shop)
	}
	if o.Owner != (core.Money{Minor: 12000, Currency: "EUR"}) {
		t.Fatalf("Owner = %v, want the agreed 12000 EUR, not the 15000 EUR asked", o.Owner)
	}
	if o.LocationID != "loc1" {
		t.Fatalf("LocationID = %q, want %q", o.LocationID, "loc1")
	}
	if o.Quantity != 1 {
		t.Fatalf("Quantity = %d, want 1", o.Quantity)
	}

	// ★ The photographs are the SAME rows, keeping the ids that are their public
	// URLs and the names of their blobs.
	if !equalStrings(photoIDs(o.Photos), wantPhotoIDs) {
		t.Fatalf("offer photo ids = %v, want the submission's own %v",
			photoIDs(o.Photos), wantPhotoIDs)
	}
	if n := countRows(t, r, "submission_photos"); n != 0 {
		t.Fatalf("submission_photos = %d, want 0 — the rows moved, they were not copied", n)
	}

	stored, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Status != core.SubmissionAccepted {
		t.Fatalf("Status = %q, want %q", stored.Status, core.SubmissionAccepted)
	}
	if stored.OfferID != o.ID {
		t.Fatalf("OfferID = %q, want %q", stored.OfferID, o.ID)
	}
	if stored.ReviewedBy != admin.ID || stored.ReviewedAt == nil {
		t.Fatalf("ReviewedBy = %q, ReviewedAt = %v, want the admin stamped",
			stored.ReviewedBy, stored.ReviewedAt)
	}
	if n, err := r.CountOpen(ctx); err != nil || n != 0 {
		t.Fatalf("CountOpen = %d (err %v), want 0 — it has left the inbox", n, err)
	}
	if offers.count() != 1 {
		t.Fatalf("offers created = %d, want exactly 1", offers.count())
	}
}

func TestAcceptSeedsTheOwnerPriceFromWhatTheSubmitterAsked(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)

	// No owner price agreed: the submitter IS the item's owner, so what they
	// hoped to receive is what the owner is to be paid. It must not touch Shop.
	c := conversion()
	c.Owner = core.Money{}
	o, err := svc.Accept(ctx, admin, sub.ID, c)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if o.Owner != (core.Money{Minor: 15000, Currency: "EUR"}) {
		t.Fatalf("Owner = %v, want the asking price 15000 EUR", o.Owner)
	}
	if o.Shop != (core.Money{Minor: 20000, Currency: "EUR"}) {
		t.Fatalf("Shop = %v, want the agreed 20000 EUR — the asking price seeds the "+
			"OWNER price, never the shop price", o.Shop)
	}
	margin, err := o.Margin()
	if err != nil {
		t.Fatalf("margin: %v", err)
	}
	if margin != (core.Money{Minor: 5000, Currency: "EUR"}) {
		t.Fatalf("Margin = %v, want 5000 EUR", margin)
	}
}

func TestAcceptRefusesToSeedAnOwnerPriceAboveTheShopPrice(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	insertLocation(t, r, "loc1", "KAUNAS", "KAUNAS")
	sub, err := svc.Submit(ctx, staff, "Marantz amplifier", "",
		core.Money{Minor: 25000, Currency: "EUR"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// The admin agreed a 200.00 shop price and named no owner price. Falling
	// back to the 250.00 asked loses money on every sale, so the fallback is
	// validated like any other figure rather than waved through.
	c := conversion()
	c.Owner = core.Money{}
	_, err = svc.Accept(ctx, admin, sub.ID, c)
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err = %v, want core.ErrInvalid", err)
	}
	if offers.count() != 0 {
		t.Fatalf("offers created = %d, want 0", offers.count())
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionNew {
		t.Fatalf("Status = %q, want it still open at %q", got.Status, core.SubmissionNew)
	}
}

func TestAcceptGeneratesASKUWhenTheAdminGivesNone(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)

	o, err := svc.Accept(ctx, admin, sub.ID, conversion())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	wantPrefix := "WH-" + time.Now().UTC().Format("20060102") + "-"
	if !strings.HasPrefix(o.SKU, wantPrefix) {
		t.Fatalf("SKU = %q, want the prefix %q", o.SKU, wantPrefix)
	}
	suffix := strings.TrimPrefix(o.SKU, wantPrefix)
	if len(suffix) != 8 {
		t.Fatalf("SKU suffix = %q, want 8 hex digits", suffix)
	}
	if strings.ToUpper(suffix) != suffix {
		t.Fatalf("SKU suffix = %q, want it uppercase — it is typed back off a label", suffix)
	}
	for _, c := range suffix {
		if !strings.ContainsRune("0123456789ABCDEF", c) {
			t.Fatalf("SKU suffix = %q, want hex only", suffix)
		}
	}
}

func TestAcceptUsesTheSKUTheAdminSupplied(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)

	c := conversion()
	c.SKU = "  MARANTZ-2226  "
	o, err := svc.Accept(ctx, admin, sub.ID, c)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if o.SKU != "MARANTZ-2226" {
		t.Fatalf("SKU = %q, want %q", o.SKU, "MARANTZ-2226")
	}
}

func TestAcceptRefusesTermsThatAreNotAgreed(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)

	for _, tc := range []struct {
		name string
		mut  func(*core.Conversion)
	}{
		{"no location: an accepted item that is nowhere cannot be found again",
			func(c *core.Conversion) { c.LocationID = " " }},
		{"no shop price: it goes straight back into the pricing queue",
			func(c *core.Conversion) { c.Shop = core.Money{} }},
		{"owner in another currency: the margin cannot be shown",
			func(c *core.Conversion) { c.Owner = core.Money{Minor: 12000, Currency: "USD"} }},
		{"owner above shop: it loses money on every sale",
			func(c *core.Conversion) { c.Owner = core.Money{Minor: 30000, Currency: "EUR"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := conversion()
			tc.mut(&c)
			if _, err := svc.Accept(ctx, admin, sub.ID, c); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("err = %v, want core.ErrInvalid", err)
			}
		})
	}
	if offers.count() != 0 {
		t.Fatalf("offers created = %d, want 0", offers.count())
	}
}

func TestAcceptRefusesASubmissionAlreadyAccepted(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)

	first, err := svc.Accept(ctx, admin, sub.ID, conversion())
	if err != nil {
		t.Fatalf("first accept: %v", err)
	}

	// One item, one offer. A second conversion would put the same thing on a
	// marketplace twice, and the second copy would have no photographs.
	c := conversion()
	c.SKU = "SECOND-SKU"
	_, err = svc.Accept(ctx, admin, sub.ID, c)
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err = %v, want core.ErrInvalid", err)
	}
	if offers.count() != 1 {
		t.Fatalf("offers created = %d, want 1", offers.count())
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.OfferID != first.ID {
		t.Fatalf("OfferID = %q, want the first offer %q", got.OfferID, first.ID)
	}
}

// TestAcceptLeavesTheSubmissionOpenWhenThePhotoMoveFails is the partial-failure
// case the ordering exists for. Three writes across two aggregates cannot share
// a transaction, so the guarantee is not atomicity but this: a failure never
// takes a submission out of the inbox while its conversion is incomplete.
func TestAcceptLeavesTheSubmissionOpenWhenThePhotoMoveFails(t *testing.T) {
	r := newTestRepo(t)
	offers := newDBOffers(r)
	svc := NewService(failingMove{SubmissionStore: r, err: errBoom}, offers, newFakeBlobs())
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)
	wantPhotoIDs := photoIDs(sub.Photos)

	_, err := svc.Accept(ctx, admin, sub.ID, conversion())
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the photo move's failure", err)
	}

	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionAccepted {
		// Anything but accepted is the correct outcome; name what happened.
		t.Logf("submission left at %q, which is correctly still open", got.Status)
	} else {
		t.Fatal("the submission was marked accepted although its photographs never " +
			"moved: it has left the inbox holding pictures the new offer does not have")
	}
	if got.OfferID != "" {
		t.Fatalf("OfferID = %q, want empty", got.OfferID)
	}
	if !equalStrings(photoIDs(got.Photos), wantPhotoIDs) {
		t.Fatalf("photos = %v, want them still on the submission %v",
			photoIDs(got.Photos), wantPhotoIDs)
	}
	if n, err := r.CountOpen(ctx); err != nil || n != 1 {
		t.Fatalf("CountOpen = %d (err %v), want 1 — a human has to see this", n, err)
	}
	// The documented cost of having no cross-aggregate transaction: a stray
	// draft offer exists. It is a draft, so no export can pick it up.
	if offers.count() != 1 {
		t.Fatalf("offers created = %d, want the 1 stray draft the recovery note describes",
			offers.count())
	}
}

func TestAcceptLeavesTheSubmissionOpenWhenTheOfferCannotBeCreated(t *testing.T) {
	svc, r, offers, _ := newTestService(t)
	ctx := context.Background()
	staff, admin := staffAndAdmin(t, r)
	sub := acceptable(t, svc, r, staff)
	offers.createErr = errBoom

	if _, err := svc.Accept(ctx, admin, sub.ID, conversion()); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the create failure", err)
	}
	got, err := r.Submission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != core.SubmissionNew {
		t.Fatalf("Status = %q, want it untouched at %q", got.Status, core.SubmissionNew)
	}
	if n := countRows(t, r, "submission_photos"); n != 2 {
		t.Fatalf("submission_photos = %d, want the 2 still on the submission", n)
	}
}

func TestAcceptOnAMissingSubmissionIsErrNotFound(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	_, admin := staffAndAdmin(t, r)
	insertLocation(t, r, "loc1", "KAUNAS", "KAUNAS")

	_, err := svc.Accept(context.Background(), admin, "ghost", conversion())
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}
