package core

import (
	"fmt"
	"strings"
	"time"
)

// SubmissionStatus is where a proposal sits in the admin's triage.
type SubmissionStatus string

const (
	// SubmissionNew has been sent and nobody has looked at it.
	SubmissionNew SubmissionStatus = "new"
	// SubmissionReviewing has been picked up — usually meaning the admin is
	// ringing the submitter to agree a price.
	SubmissionReviewing SubmissionStatus = "reviewing"
	// SubmissionAccepted became an offer. OfferID says which.
	SubmissionAccepted SubmissionStatus = "accepted"
	// SubmissionDeclined was not taken, with a reason the submitter can read.
	SubmissionDeclined SubmissionStatus = "declined"
)

// SubmissionStatuses lists the triage states in order.
func SubmissionStatuses() []SubmissionStatus {
	return []SubmissionStatus{
		SubmissionNew, SubmissionReviewing, SubmissionAccepted, SubmissionDeclined,
	}
}

// Label returns a human-readable name.
func (s SubmissionStatus) Label() string {
	switch s {
	case SubmissionNew:
		return "New"
	case SubmissionReviewing:
		return "Reviewing"
	case SubmissionAccepted:
		return "Accepted"
	case SubmissionDeclined:
		return "Declined"
	}
	return string(s)
}

// Valid reports whether s is a status this application defines.
func (s SubmissionStatus) Valid() bool {
	for _, k := range SubmissionStatuses() {
		if k == s {
			return true
		}
	}
	return false
}

// Open reports whether the submission still needs the admin's attention. It is
// what the inbox badge counts.
func (s SubmissionStatus) Open() bool {
	return s == SubmissionNew || s == SubmissionReviewing
}

// ParseSubmissionStatus validates a status from a form or query string.
func ParseSubmissionStatus(s string) (SubmissionStatus, error) {
	st := SubmissionStatus(strings.ToLower(strings.TrimSpace(s)))
	if !st.Valid() {
		return "", fmt.Errorf("%w: unknown submission status %q", ErrInvalid, s)
	}
	return st, nil
}

// Submission is something a staff user is offering to the warehouse.
//
// It is deliberately NOT an offer. A staff member has a photograph, a name for
// the thing, and an idea of what they want for it — and nothing else an export
// would need. Making them fill in an offer instead would either block the
// submission behind fields they cannot answer, or admit half-built offers into
// the catalogue where an export might pick one up. A submission is a message
// that becomes an offer only when an administrator decides it should.
//
// The submitter is the item's OWNER, which is why Asking seeds the offer's owner
// price on conversion rather than its shop price: what the person handing the
// thing over wants for it is exactly what [Offer.Owner] means.
type Submission struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// SubmittedBy is the user who sent it.
	SubmittedBy string
	// SubmitterName is denormalised for display, so the inbox does not need a
	// join per row to show who sent what.
	SubmitterName string
	// Title is what the submitter calls the thing.
	Title string
	// Note is anything else they want to say — condition, history, urgency.
	Note string
	// Asking is what the submitter hopes to receive. Optional: "what will you
	// give me for this" is a legitimate submission, and demanding a number would
	// make people invent one.
	Asking Money
	// Status is the triage state.
	Status SubmissionStatus
	// DeclineReason is shown to the submitter when it was not taken. It is
	// required on a decline: "no" without a reason produces the same submission
	// again next week.
	DeclineReason string
	// OfferID is the offer this became, set only once accepted.
	OfferID string
	// Photos are the images the submitter attached, in order.
	Photos []Photo
	// CreatedAt is when it was sent, in UTC.
	CreatedAt time.Time
	// ReviewedAt and ReviewedBy record who closed it and when.
	ReviewedAt *time.Time
	ReviewedBy string
}

// Validate checks a submission's own rules.
func (s *Submission) Validate() error {
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("%w: say what the item is", ErrInvalid)
	}
	if strings.TrimSpace(s.SubmittedBy) == "" {
		return fmt.Errorf("%w: a submission needs a submitter", ErrInvalid)
	}
	if !s.Status.Valid() {
		return fmt.Errorf("%w: unknown submission status %q", ErrInvalid, s.Status)
	}
	if s.Status == SubmissionDeclined && strings.TrimSpace(s.DeclineReason) == "" {
		return fmt.Errorf("%w: a declined submission needs a reason — the submitter "+
			"cannot act on a bare no, and will send the same thing again", ErrInvalid)
	}
	if s.Status == SubmissionAccepted && strings.TrimSpace(s.OfferID) == "" {
		return fmt.Errorf("%w: an accepted submission must name the offer it became",
			ErrInvalid)
	}
	if s.Asking.Currency != "" {
		if _, err := s.Asking.Exponent(); err != nil {
			return err
		}
	}
	return nil
}

// PrimaryPhoto returns the first photograph, or the zero Photo.
func (s *Submission) PrimaryPhoto() Photo {
	if len(s.Photos) == 0 {
		return Photo{}
	}
	return s.Photos[0]
}

// Ready reports whether the submission carries enough to be worth a decision:
// something to look at, and something to call it.
//
// A submission with no photograph is not refused — somebody may be describing an
// item they are about to bring in — but the inbox marks it, because a decision
// about a thing nobody can see is not really a decision.
func (s *Submission) Ready() bool {
	return len(s.Photos) > 0 && strings.TrimSpace(s.Title) != ""
}

// Conversion is the terms an administrator agreed before turning a submission
// into a sellable offer.
//
// It exists as a type so the agreement is stated in one place and validated
// once, rather than being assembled from loose arguments at the call site where
// a swapped pair of prices would be silent and expensive.
type Conversion struct {
	// SKU is the offer's handle. Empty means generate one.
	SKU string
	// Shop is what the item will be listed at.
	Shop Money
	// Owner is what the submitter is to be paid — the figure agreed on the phone.
	// It defaults to the submission's Asking when left zero.
	Owner Money
	// LocationID is where the item is being put. Required: the point of accepting
	// something is that it physically arrives somewhere.
	LocationID string
	// Condition is free text for the listing.
	Condition string
	// Description is the listing body.
	Description string
}

// Validate checks the agreed terms.
func (c *Conversion) Validate() error {
	if strings.TrimSpace(c.LocationID) == "" {
		return fmt.Errorf("%w: choose where the item is being stored — an accepted "+
			"item that is nowhere cannot be found again", ErrInvalid)
	}
	if c.Shop.IsZero() {
		return fmt.Errorf("%w: set the shop price. Accepting an item without one "+
			"puts it straight back into the pricing queue", ErrInvalid)
	}
	if !c.Owner.IsZero() && c.Owner.Currency != c.Shop.Currency {
		return fmt.Errorf("%w: the shop price is %s and the owner price is %s. Agree "+
			"both in one currency, or the margin cannot be shown",
			ErrInvalid, c.Shop.Currency, c.Owner.Currency)
	}
	if !c.Owner.IsZero() && c.Owner.Minor > c.Shop.Minor {
		return fmt.Errorf("%w: the owner is to be paid %s but the item would be listed "+
			"at %s, which loses money on every sale. Change one of them if that is "+
			"deliberate", ErrInvalid, c.Owner.String(), c.Shop.String())
	}
	return nil
}
