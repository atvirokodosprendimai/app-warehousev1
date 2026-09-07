# ADR-011: Keep a submission separate from an offer until an administrator converts it

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-003, ADR-007, ADR-010, `migrations/00003_submissions.sql`
**Governs:** `internal/submission/*.go`, `migrations/00003_submissions.sql`
**Enforced-by:** `internal/submission/service_test.go::TestAcceptConvertsTheSubmissionIntoADraftOfferCarryingItsPhotos`
**Invalidates:** none — checked
**Served-path change:** A staff member photographs something, names it and says what they hope for; it lands in an administrator's inbox, the administrator rings them, agrees a price, and one action turns it into a draft offer attached to a place — keeping the photographs' URLs.

## Context

Retrospective record. The requirement: *"a simple user logs in, writes a title,
adds a photo, and the admin gets it as a message. If it is valuable the admin
calls the user, says a price, and — if it is agreed — converts that inbox thing
into an exportable product with all fields and prices, attached to a warehouse."*

The tempting implementation is "a submission is a draft offer with a flag". It is
wrong in both directions:

- A staff member has a photograph, a name, and an idea of what they want. They do
  not have a SKU, a condition, a location or a shop price, and demanding them
  either blocks the submission or admits half-built offers into the catalogue.
- An offer in the catalogue is a thing an export can pick up. A half-built one
  behind a flag is one forgotten `WHERE` clause away from a marketplace.

## Existing Primitives Audit

- `core.Money` (ADR-002) — **reused** for the asking price.
- `core.Sequencer` / `FormatSKU` (ADR-010) — **reused** to mint the reference at
  conversion, which is why the format lives in `core` rather than in `offer`.
- `offer_photos` — **reshaped**: `submission_photos` has the same shape on
  purpose, so accepting can *move* rows rather than copy bytes.
- The offer aggregate is deliberately **not** reused as the submission's
  storage; that is the whole decision.

## Decision

`submissions` is its own table and its own aggregate. It carries what a submitter
actually has — title, note, photographs, and an **optional** asking price, because
"what will you give me for this?" is a legitimate submission and demanding a
number makes people invent one.

Status is `new → reviewing → accepted | declined`, with two rules the database
enforces rather than the code:

- A **declined** submission must carry a review note. A decline without a reason
  produces the same submission again next week.
- An **accepted** submission must name the offer it became, or the trail from
  "we agreed a price on the phone" to "this is the listing" is broken.

Accepting mints an offer and **moves the photo rows to `offer_photos` keeping the
same ids**. It does not re-upload: the id is the public URL (ADR-007), and a
re-upload would mint a new UUID and break a link an administrator may already have
shared with the submitter or a buyer.

What the submitter asked for becomes the offer's **owner** price by default,
because the submitter *is* the item's owner — which is exactly what `Owner` means
(ADR-003). Seeding an owner price above the shop price is refused: it would be a
listing that loses money on every sale.

Conversion is all-or-nothing. If the photo move fails, or the offer cannot be
created, the submission is left **open** rather than half-converted — a submission
marked accepted with no offer is a record nobody can act on.

Foreign keys carry the intent: `submitted_by` is `RESTRICT`, because deleting a
person must not erase the record of what they handed over and what was agreed;
`offer_id` is `SET NULL`, so if the offer is later deleted the submission survives
as a record that it was accepted.

What would FAIL this decision: the acceptance test not carrying the photos across,
`TestMovePhotosToOfferKeepsTheSamePhotoIDs` returning new ids, or
`TestAcceptLeavesTheSubmissionOpenWhenThePhotoMoveFails` leaving a half-converted
row. All run on every `go test ./...`.

## Alternatives Considered

- **A draft offer with a `submitted` flag.** Rejected: it puts unfinished rows in
  the table exports read, and it forces the submitter to supply fields they do not
  have.
- **A generic "message" or ticket table.** Rejected: the photographs and the
  asking price are structured data the conversion needs; a free-text message would
  have to be re-typed.
- **Copy the photo bytes on acceptance.** Rejected: a new id breaks a shared URL,
  and it doubles the storage for no benefit. Moving the row is one `INSERT` and
  one `DELETE` and the bytes never move.
- **Let the submitter set the shop price.** Rejected: the price is agreed in a
  phone call, by the administrator, after research. That is the process the
  application is modelling.

## Component / Boundary Impact

`internal/submission` owns the aggregate and the conversion, and it is the only
package that writes both `submissions` and `offers` in one transaction.
`internal/offer` does not know submissions exist. One reason to change: how a
proposal becomes stock.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `submissions` + `submission_photos` tables | new | `migrations/00003_submissions.sql` | `internal/submission` |
| CHECK: declined ⇒ review note; accepted ⇒ offer id | new constraints | `migrations/00003_submissions.sql` | every writer |
| `MovePhotosToOffer` keeping ids | new | `internal/submission` | `internal/blob` (untouched), `internal/export` |
| `CountOpen()` for the unread banner | new | `internal/submission` | `internal/web` SSE stream (ADR-009) |
| Asking price seeds `Offer.Owner` | new behaviour | `internal/submission` | `internal/offer` |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/submission/` and
`migrations/00003_submissions.sql`. No tasks directory: this is a retrospective
record.

## Consequences

- **Positive:** the catalogue contains only things somebody decided to sell.
- **Positive:** a photograph's URL survives conversion, so a link that has gone
  out keeps working.
- **Positive:** the audit trail from proposal to listing is in the data, enforced
  by constraints rather than by convention.
- **Negative:** two photo tables with the same shape is duplication, accepted
  deliberately so the aggregates stay separate.
- **Negative — a known wart:** `core.Photo` has **no `SubmissionID` field**, so a
  submission's photo carries the submission id in `Photo.OfferID`. It works, it is
  tested, and the field name is a lie. It is written down here rather than left
  for somebody to discover.
- **Neutral:** every administrator-only method refuses a non-administrator
  explicitly, with a test that walks all of them.

## Out of Scope

- Letting a submitter edit a submission after sending it (deferred: docs/adr/BACKLOG.md)
- Notifying the submitter of a decision by email or SMS (deferred: docs/adr/BACKLOG.md)
- Renaming `Photo.OfferID` to something honest (deferred: docs/adr/BACKLOG.md)
- Bulk acceptance of several submissions (permanent: boundary: each one is a phone call and a negotiated price; a bulk action would model a process nobody performs)
- A submitter seeing what their item eventually sold for (permanent: boundary: the sale price is the business's, and the submitter is owed the agreed owner price regardless — ADR-003)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A conversion half-completes | Low | High | One transaction; two tests drive the failure of each leg |
| `Photo.OfferID` holding a submission id misleads a future reader | **Med** | Med | Named in Consequences and deferred in the backlog; the field is never used cross-aggregate |
| An accepted submission points at a deleted offer | Low | Low | `SET NULL`, deliberately — the submission survives as history |
| A non-administrator reaches an admin-only method | Low | High | `TestEveryAdminOnlyMethodRefusesANonAdmin` walks every one of them |

## Rollback

The tables are additive; dropping them would remove the inbox and leave offers
untouched. Submissions already converted have produced independent offers that do
not reference them, apart from the nullable back-pointer.

## Follow-ups

- [x] `core.Photo.OfferID` should be an aggregate-neutral name. **Done 2026-09-07**: it is `core.Photo.ParentID`, across seventeen sites in five packages. ⚠ `core.Submission.OfferID` is a DIFFERENT field and a true one — a submission accepted onto an offer really does carry that offer's id — so it was left alone; the two are told apart by their type, not by their name, which is exactly why the rename was worth doing. The database column stays `offer_id` on `offer_photos`, which is the honest half: those rows really are an offer's.
