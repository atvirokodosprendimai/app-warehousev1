package submission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrNotPermitted reports an action refused because the actor is not an
// administrator.
//
// Triage is an administrator's job: accepting a submission commits the business
// to paying for an item and puts stock on a shelf, and declining one closes
// somebody's proposal. It is a sentinel rather than a plain error so a handler
// can render 403 for this and 500 for a genuine fault.
var ErrNotPermitted = errors.New("submission: only an administrator may do this")

// allowedPhotoTypes are the image types a marketplace fetcher will actually
// render.
//
// A submission's photographs become an offer's photographs unchanged at
// conversion, so the type has to be acceptable to a marketplace at intake — the
// alternative is discovering it after the item is listed, when the picture is
// the only thing a buyer looks at.
var allowedPhotoTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// OfferWriter is the slice of the offer aggregate that converting a submission
// needs: create an offer, and read one back.
//
// It is declared HERE, at the consumer, rather than imported from the offer
// package on purpose. Conversion is the single point where these two aggregates
// touch, and importing the offer package for it would make every submission
// depend on the whole of offer — its SKU rules, its listing guards, its blob
// handling — for two method calls. Stating the two calls as an interface keeps
// the dependency at exactly the width of the collaboration, and lets a test
// stand in for the offer side without a database. core.OfferStore satisfies it,
// so the real *offer.Repo can be passed straight in.
type OfferWriter interface {
	// CreateOffer inserts a new offer.
	CreateOffer(ctx context.Context, o core.Offer) error
	// Offer returns one offer with its photos, or core.ErrNotFound.
	Offer(ctx context.Context, id string) (core.Offer, error)
}

// Service is the submission aggregate's write side: the only place a submission
// changes, and the only place one becomes an offer.
//
// It holds a core.SubmissionStore because every mutation here is a
// read-modify-validate-write over a whole submission, so the domain rules in
// core.Submission.Validate see the complete state rather than one changed field.
type Service struct {
	store  core.SubmissionStore
	offers OfferWriter
	blobs  core.BlobStore
	seq    core.Sequencer
}

// NewService returns a Service writing submissions through store, converted
// offers through offers, and photo bytes through blobs.
func NewService(store core.SubmissionStore, offers OfferWriter, blobs core.BlobStore, seq core.Sequencer) *Service {
	return &Service{store: store, offers: offers, blobs: blobs, seq: seq}
}

// Submit records a staff user's proposal and puts it in the administrator's
// inbox as status new.
//
// Only a title is required. asking is what the submitter hopes to receive and
// may be zero: "what will you give me for this" is a legitimate submission, and
// demanding a number would make people invent one.
func (s *Service) Submit(ctx context.Context, user core.User, title, note string, asking core.Money) (core.Submission, error) {
	sub := core.Submission{
		ID:          uuid.NewString(),
		SubmittedBy: user.ID,
		// Carried on the returned value for the caller to render immediately.
		// It is not persisted: the name is resolved from the users table on
		// every read, so there is no copy to go stale.
		SubmitterName: user.Name(),
		Title:         strings.TrimSpace(title),
		Note:          strings.TrimSpace(note),
		Asking:        asking,
		Status:        core.SubmissionNew,
		CreatedAt:     now(),
	}
	if err := sub.Validate(); err != nil {
		return core.Submission{}, err
	}
	if err := s.store.CreateSubmission(ctx, sub); err != nil {
		return core.Submission{}, err
	}
	return sub, nil
}

// AddPhoto stores an image for a submission and appends it after the ones
// already there.
//
// ⚠ THE BLOB IS WRITTEN BEFORE THE ROW, and the order is the whole point. The
// photo's id is also its public URL segment, and that id survives conversion
// unchanged — so a row written first whose blob write then failed would put a
// broken picture in front of the administrator deciding on the item, and would
// carry that broken picture onto a marketplace listing the moment the submission
// is accepted. A blob written first whose row write fails is an unreferenced
// file that nothing serves and nobody sees.
//
// No permission check: submitting is what a staff account is for, and the photos
// belong to the submission the caller already resolved.
func (s *Service) AddPhoto(ctx context.Context, submissionID, filename, contentType string, data []byte) (core.Photo, error) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !allowedPhotoTypes[contentType] {
		return core.Photo{}, fmt.Errorf("%w: %q is not an image type a marketplace will "+
			"fetch", core.ErrInvalid, contentType)
	}
	if len(data) == 0 {
		return core.Photo{}, fmt.Errorf("%w: empty image", core.ErrInvalid)
	}

	sub, err := s.store.Submission(ctx, submissionID)
	if err != nil {
		return core.Photo{}, err
	}

	sum := sha256.Sum256(data)
	p := core.Photo{
		ID: uuid.NewString(),
		// The parent submission travels in ParentID; see
		// [Repo.AddSubmissionPhoto].
		ParentID:    sub.ID,
		Position:    nextPosition(sub.Photos),
		Filename:    filename,
		ContentType: contentType,
		ByteSize:    int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		CreatedAt:   now(),
	}
	if err := s.blobs.Put(ctx, p.ID, data); err != nil {
		return core.Photo{}, fmt.Errorf("submission: store photo bytes: %w", err)
	}
	if err := s.store.AddSubmissionPhoto(ctx, p); err != nil {
		return core.Photo{}, fmt.Errorf("submission: store photo row: %w", err)
	}
	return p, nil
}

// StartReview moves a new submission to reviewing and stamps who picked it up.
//
// A submission already being reviewed is left exactly as it is, including its
// reviewer: the inbox is shared, the button is on a page that may be seconds
// stale, and failing there would be noise about nothing. A closed submission is
// refused — reopening a decision is not what this button means.
func (s *Service) StartReview(ctx context.Context, admin core.User, id string) error {
	if err := requireAdmin(admin); err != nil {
		return err
	}
	sub, err := s.store.Submission(ctx, id)
	if err != nil {
		return err
	}
	if sub.Status == core.SubmissionReviewing {
		return nil
	}
	if sub.Status != core.SubmissionNew {
		return fmt.Errorf("%w: submission %s is already %s and cannot go back into "+
			"review", core.ErrInvalid, id, sub.Status.Label())
	}
	stamp := now()
	sub.Status = core.SubmissionReviewing
	sub.ReviewedAt = &stamp
	sub.ReviewedBy = admin.ID
	if err := sub.Validate(); err != nil {
		return err
	}
	return s.store.UpdateSubmission(ctx, sub)
}

// Decline closes a submission with a reason the submitter can read.
//
// The reason is required, and refused here as well as by core.Submission.Validate
// and by the schema's CHECK. Three layers for one rule because a bare "no"
// produces the same submission again next week, and a report is only as
// trustworthy as its weakest write path.
//
// An already-accepted submission is refused: it has become an offer, and
// declining it would leave a record saying both.
func (s *Service) Decline(ctx context.Context, admin core.User, id, reason string) error {
	if err := requireAdmin(admin); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: give a reason — the submitter cannot act on a bare no, "+
			"and will send the same thing again", core.ErrInvalid)
	}
	sub, err := s.store.Submission(ctx, id)
	if err != nil {
		return err
	}
	if sub.Status == core.SubmissionAccepted {
		return fmt.Errorf("%w: submission %s was already accepted as offer %s",
			core.ErrInvalid, id, sub.OfferID)
	}
	stamp := now()
	sub.Status = core.SubmissionDeclined
	sub.ReviewNote = reason
	sub.ReviewedAt = &stamp
	sub.ReviewedBy = admin.ID
	if err := sub.Validate(); err != nil {
		return err
	}
	return s.store.UpdateSubmission(ctx, sub)
}

// Accept converts a submission into a real, sellable offer on the terms the
// administrator agreed, and returns that offer.
//
// The offer is created as a DRAFT: conversion settles the price and the shelf,
// not the decision to publish, and a draft is the one status no export can pick
// up. Its owner price is c.Owner, falling back to what the submitter asked for
// when the administrator did not name one — the submitter IS the item's owner,
// so "what they hope to receive" and "what the owner is to be paid" are the same
// figure. A submitter's asking price used as the owner price is validated
// against the shop price like any other, because a fallback that skips the
// margin check is a fallback that quietly lists items at a loss.
//
// ⚠ THIS IS NOT ATOMIC, AND CANNOT BE. It is three writes across two aggregates
// — create the offer, move the photographs, stamp the submission accepted — and
// this package has no transaction that spans another aggregate's store. The
// order is therefore chosen so that every partial failure leaves the SUBMISSION
// STILL OPEN IN THE INBOX, because that is the one state a human is looking at:
//
//   - create failed: nothing happened.
//   - photo move failed: a draft offer exists with no pictures; the submission
//     still has them and is still open.
//   - accept stamp failed: a draft offer exists holding the pictures; the
//     submission is still open and now shows none.
//
// ⚠ RE-RUNNING Accept AFTER A PARTIAL FAILURE IS NOT SAFE ON ITS OWN: it mints a
// SECOND offer from the same submission, and in the third case that second offer
// gets no photographs, because the first one already took them. The recovery is
// manual and the evidence is visible — the stray draft is in the drafts list.
// Delete it and accept again, or finish it by hand. What is guaranteed is that a
// failure never removes a submission from the inbox while its conversion is
// incomplete, and that a submission already accepted is refused outright rather
// than converted twice.
func (s *Service) Accept(ctx context.Context, admin core.User, id string, c core.Conversion) (core.Offer, error) {
	if err := requireAdmin(admin); err != nil {
		return core.Offer{}, err
	}
	if err := c.Validate(); err != nil {
		return core.Offer{}, err
	}

	sub, err := s.store.Submission(ctx, id)
	if err != nil {
		return core.Offer{}, err
	}
	if sub.Status == core.SubmissionAccepted {
		return core.Offer{}, fmt.Errorf("%w: submission %s was already accepted as offer "+
			"%s; converting it again would create a second offer for one item",
			core.ErrInvalid, id, sub.OfferID)
	}

	terms := c
	if terms.Owner.IsZero() {
		terms.Owner = sub.Asking
		// Re-check the agreed terms with the seeded figure in place. c.Validate
		// above never saw it, so without this an asking price above the shop
		// price, or in another currency, would reach the offer unexamined.
		if err := terms.Validate(); err != nil {
			return core.Offer{}, fmt.Errorf("no owner price was agreed, so the submitter's "+
				"asking price was used: %w", err)
		}
	}

	sku := strings.TrimSpace(terms.SKU)
	t := now()
	if sku == "" {
		// The same counter the intake counter draws on, so a converted submission
		// is indistinguishable from stock taken in over the counter — an operator
		// reading a label should not be able to tell which door an item came
		// through. The format lives in core precisely so these two callers cannot
		// drift apart.
		//
		// No retry here, unlike intake. A collision means somebody typed this
		// reference by hand before the counter reached it, and telling the
		// administrator to try again — which allocates the next number — is
		// better than this package reaching into the offer aggregate's error
		// vocabulary to classify one, which is the coupling OfferWriter exists to
		// avoid.
		n, err := s.seq.NextSequence(ctx, core.SequenceOfferSKU)
		if err != nil {
			return core.Offer{}, fmt.Errorf("submission %s: allocate reference: %w", id, err)
		}
		sku = core.FormatSKU(n)
	}
	o := core.Offer{
		ID:          uuid.NewString(),
		SKU:         sku,
		Title:       sub.Title,
		Description: terms.Description,
		Condition:   terms.Condition,
		Status:      core.StatusDraft,
		Quantity:    1,
		Shop:        terms.Shop,
		Owner:       terms.Owner,
		LocationID:  terms.LocationID,
		CreatedAt:   t,
		UpdatedAt:   t,
	}
	if err := o.Validate(); err != nil {
		return core.Offer{}, err
	}
	if err := s.offers.CreateOffer(ctx, o); err != nil {
		return core.Offer{}, fmt.Errorf("submission %s: create offer: %w", id, err)
	}

	// Before the accept stamp, never after: a submission that has left the inbox
	// while its pictures are still attached to it is a submission nobody will
	// look at again, and an offer that will be listed with no images.
	if err := s.store.MovePhotosToOffer(ctx, sub.ID, o.ID); err != nil {
		return core.Offer{}, fmt.Errorf("submission %s: move photos to offer %s: %w",
			id, o.ID, err)
	}

	// Read the offer back so the caller renders the photographs as the database
	// now holds them, rather than as this function assumed it would. Still before
	// the stamp, so a failure here lands in the same recoverable state as a
	// failed move rather than in one where the submission is closed on an offer
	// nobody could confirm.
	created, err := s.offers.Offer(ctx, o.ID)
	if err != nil {
		return core.Offer{}, fmt.Errorf("submission %s: read back offer %s: %w",
			id, o.ID, err)
	}

	stamp := now()
	sub.Status = core.SubmissionAccepted
	sub.OfferID = o.ID
	// The note now records what was AGREED rather than why it was refused —
	// "agreed 30 EUR, collecting Tuesday". Whatever the submission carried from
	// an earlier decline describes a decision that has since been changed, so it
	// is replaced rather than kept beside a contradictory outcome.
	sub.ReviewNote = strings.TrimSpace(c.Note)
	sub.ReviewedAt = &stamp
	sub.ReviewedBy = admin.ID
	if err := sub.Validate(); err != nil {
		return core.Offer{}, err
	}
	if err := s.store.UpdateSubmission(ctx, sub); err != nil {
		return core.Offer{}, fmt.Errorf("submission %s: mark accepted: %w", id, err)
	}
	return created, nil
}

// requireAdmin refuses a non-administrator with [ErrNotPermitted].
func requireAdmin(u core.User) error {
	if !u.IsAdmin {
		return fmt.Errorf("%w: %s is not an administrator", ErrNotPermitted, u.Name())
	}
	return nil
}

// nextPosition returns the first free display position after the photos a
// submission already has.
//
// It is the highest position plus one rather than the count, because a deletion
// leaves a gap and renumbering on delete would move the primary image — the one
// the administrator sees in the inbox, and the one a marketplace shows first
// once the item is converted.
func nextPosition(photos []core.Photo) int {
	next := 0
	for _, p := range photos {
		if p.Position >= next {
			next = p.Position + 1
		}
	}
	return next
}

// now is the write side's clock, truncated to the second because that is the
// resolution the RFC3339 columns store. Without the truncation the struct a
// caller is handed back would not equal the one the next read returns.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }
