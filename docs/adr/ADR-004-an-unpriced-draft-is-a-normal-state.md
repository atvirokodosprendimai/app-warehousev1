# ADR-004: Treat an unpriced draft as a normal state, not an incomplete row

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-003, `internal/core/offer.go`
**Governs:** `internal/core/offer.go`, `internal/offer/service.go`
**Enforced-by:** `internal/core/offer_test.go::TestADraftNeedsNoPrice`
**Invalidates:** none — checked
**Served-path change:** An operator can photograph and title an item in one pass without inventing a price, and the items still waiting on that research appear as their own queue on the dashboard.

## Context

Retrospective record. The rule it describes is already in the code, at
`internal/core/offer.go` (`Offer.Validate`, `Offer.NeedsPricing`) and in the
`offers` CHECK constraint in `migrations/00001_init.sql`.

The intake flow the operator described on 2026-09-06 was, in their words,
*"photo, title, price later after research what it can offer for thing holder"*.
The research step happens away from the shelves, at a desk, and often for many
items at a sitting.

A schema that demands a price at creation does not change that ordering; it moves
the missing number into the database as a placeholder typed to get past the form.
Once stored, a placeholder is indistinguishable from a researched price — nothing
in the row records which it was — so an offer carrying one can reach `listed` and
be exported as a genuine figure to a marketplace.

That is the specific failure this decision avoids. It is a property of the data
model rather than a measured comparison between two implementations, and it is
stated here as such: no version of this application has been run with the price
required at creation, so there is nothing to measure it against.

## Existing Primitives Audit

- `core.Money.IsZero()` (ADR-002) — **reused** as the "not set" signal. This is
  why zero-means-unset had to be a deliberate part of the money type rather than
  an accident.
- `core.Status` — **reused**. The gate is placed on the status transition rather
  than on creation, which is the same set of states with the check moved to where
  the consequence is.
- No prior draft or workflow concept existed to reshape.

## Decision

`Offer.Validate()` requires a title and a SKU and does **not** require a price. A
price becomes mandatory exactly when the offer enters a status that is exported:

    if o.Status.Exportable() && o.Shop.IsZero() { … refuse … }

`NeedsPricing()` — `StatusDraft && Shop.IsZero()` — is **derived, never stored**.
A stored duplicate would go stale the moment somebody typed a price without also
moving a status, and the queue would then show work that is already done.

The same rule is asserted twice, on purpose: the domain refuses it in Go, and the
database refuses it in a `CHECK` constraint. The Go check gives the operator a
sentence they can act on; the constraint means no other writer — a migration, a
future importer, a hand-run `UPDATE` — can get around it.

What would FAIL this decision: `TestListingWithoutAPriceIsRefused` passing an
unpriced offer to `listed`, or `TestSetStatusRefusesAnExportedStatusWithoutAShopPrice`
allowing the transition at the service layer. Both run on every `go test ./...`.

## Alternatives Considered

- **Require a price at creation.** Rejected on the behaviour it produces: the
  operator types a placeholder, and a placeholder that reaches a marketplace is
  the exact failure the requirement was meant to prevent.
- **A separate `needs_pricing` boolean column.** Rejected: it is derivable from
  two fields already present, and a stored copy goes stale silently. The queue
  would then be wrong in the direction that hides work.
- **A distinct `StatusUnpriced`.** Rejected: it splits `draft` into two states
  that behave identically everywhere except one filter, and every consumer of
  `Status` would have to learn about it.
- **Allow a null price and refuse it only in the exporter.** Rejected as too
  late: the operator finds out at export time, in a batch, about an item they
  last looked at a week ago.

## Component / Boundary Impact

`internal/core` owns the rule; `internal/offer` enforces it on the status
transition; `migrations/00001_init.sql` carries the constraint that makes it true
of the data rather than of the code. `internal/web` reads `NeedsPricing` to build
the research queue. One reason to change: when this business considers an item
ready to publish.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `Offer.Validate()` | price not required; required on an exportable status | `internal/core` | offer, submission |
| `Offer.NeedsPricing()` | new, derived | `internal/core` | `internal/web` dashboard queue |
| `offers` CHECK: an exportable status needs a shop price | new constraint | `migrations/00001_init.sql` | every writer, including future ones |
| `OfferFilter.NeedsPricing` | new query filter | `internal/offer` | `internal/web` |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `Offer.Validate`, `Offer.NeedsPricing`, the
`offers` CHECK constraint, and the `NeedsPricing` query filter. No tasks
directory: this is a retrospective record.

## Consequences

- **Positive:** the data model matches the order the work actually happens in, so
  nobody is pushed into typing a number they do not have.
- **Positive:** the items awaiting research are a queue rather than a thing to
  remember.
- **Negative:** an offer can sit in `draft` for ever and nothing chases it; the
  queue makes it visible but does not escalate.
- **Neutral:** two enforcement points (domain and constraint) must be kept in
  step. They currently agree, and `internal/store/migrate_test.go` exercises the
  constraint directly.

## Out of Scope

- Reminding anybody that a draft has been unpriced for a long time (deferred: docs/adr/BACKLOG.md)
- Suggesting a price from comparable sales (permanent: boundary: the research step is a person looking at a marketplace; automating it is a different product)
- Bulk pricing of a whole batch in one screen (deferred: docs/adr/BACKLOG.md)
- A price on a `sold` offer being required (permanent: boundary: `sold` requires a sold price and a date, which is a different rule stated in the same `Validate`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Drafts accumulate unpriced and unnoticed | Med | Low | The research queue is on the dashboard, not behind a filter |
| The Go rule and the CHECK constraint drift apart | Low | Med | `migrate_test.go` asserts the constraint independently of the domain |
| A zero price is meant literally ("free") | Low | Med | Not representable, deliberately: zero means unset (ADR-002). A free item is out of this application's scope |

## Rollback

Adding the price requirement back to `Validate` is a one-line change; the CHECK
constraint would need a migration. No stored data becomes invalid either way,
because every offer that is currently exportable already has a price.

## Follow-ups

None — nothing was left open when this record was written.
