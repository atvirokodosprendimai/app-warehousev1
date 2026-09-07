# ADR-019: Treat an undescribed photograph group as a normal state, so two people can share one intake

**Status:** Accepted
**Date:** 2026-09-07
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-004, ADR-007, ADR-010, ADR-011, ADR-012, `internal/core/offer.go`
**Governs:** `internal/core/offer.go`, `internal/core/cart.go`, `internal/core/ports.go`, `internal/offer/repo.go`, `migrations/00008_an_undescribed_draft.sql`
**Enforced-by:** `internal/core/offer_test.go::TestADraftNeedsNoTitle`
**Invalidates:** ADR-004's sentence "`Offer.Validate()` requires a title and a SKU" — the SKU half stands, the title half does not. ADR-004's DECISION is untouched and extended; only that one factual clause is superseded, and ADR-004 now carries a pointer here.
**Served-path change:** One person can walk the warehouse photographing things without naming any of them, and a second person picks the results out of a "Needs describing" queue later and writes the title, description and category — instead of one person having to do both at the shelf.

## Context

Reported by M on 2026-09-07: *"flow : one person makes "group of photos" ( about 1 thing ) 2nd person creates titles descriptions etc. need new menu item for photos which needs to be categoriezed, described etc."*

This is a **division of labour**, not a new kind of thing. Photographing stock and
cataloguing it are different jobs, done well by different people, at different
times, and often on different devices — a phone at the shelf versus a keyboard at
a desk. Today the application forces them into one pass, because the first thing
intake asks for is a title and **nothing can be photographed until an offer
exists to attach photographs to**: `/offers/{id}/photos` needs the `{id}`.

So the photographer is made to invent a name for every item before they may take
a picture of it. That is the same defect ADR-004 already identified one field
over, and ADR-004 named the consequence precisely: an operator asked for a value
they do not have yet types a placeholder, and a placeholder that reaches a listed
status ships to a marketplace as though it were real. A shelf of items called
"lamp", "lamp 2", "box of stuff" is exactly that failure with the title instead
of the price.

⚠ **The queue is the feature, not the storage.** M asked for a *menu item*. The
work that matters is making "photographed but not yet described" a state the
application can SHOW somebody, because a division of labour needs a hand-off
point. Where the photographs are kept is an implementation detail underneath it.

## Existing Primitives Audit

- **ADR-004's whole shape** — **reused, not re-derived.** "An unpriced draft is a
  normal state, surfaced as its own queue" is this decision one field over.
  `NeedsPricing()` (derived, never stored), `OfferFilter.NeedsPricing`, the
  `offers_needs_pricing_idx` index, the sidebar count, the filter chip and the
  dashboard tile are all copied in shape. This record adds no new mechanism; it
  adds a second instance of one that is already load-bearing.
- **The offer aggregate** — **reused**. A photograph group IS a draft offer with
  photos and no title yet. Photos already attach to offers (`offer_photos`), the
  photo id is already the public URL (ADR-007), and the reference is already
  allocated at creation (ADR-010) — so an untitled draft is still identifiable,
  labellable and findable by `WH0000042`.
- **The offer editor** — **reused as the describing screen.** It already carries
  Title, Description, Condition and the per-offer marketplace category, and after
  the 2026-09-07 mobile pass it renders Photos directly under Details. The second
  person needs no new screen; they need a way to FIND the work.
- **`internal/submission`** — **deliberately NOT reused.** See Alternatives.
- **`Offer.Validate`'s exportable-price guard** — **reshaped**: the title gets the
  identical treatment, refused at the same boundary rather than at creation.
- **The `CHECK` on `shop_minor`** — **reshaped into a trigger** for the title, for
  a SQLite reason given under Decision.

## Decision

**A draft offer may have no title. A title becomes mandatory exactly when the
offer enters a status that is exported**, which is the rule ADR-004 already
applies to the price, on the same boundary, for the same reason:

    if o.Status.Exportable() && strings.TrimSpace(o.Title) == "" { … refuse … }

`NeedsDescribing()` — `StatusDraft && Title == ""` — is **derived, never stored**,
for ADR-004's reason: a stored duplicate goes stale the moment somebody types a
title without also moving a status, and the queue then shows work already done.

The queue is surfaced the way the pricing queue is: `OfferFilter.NeedsDescribing`,
`/offers?needs_describing=1`, a sidebar entry **"Needs describing"** with a live
count, a filter chip, and a dashboard tile.

⚠ **The rule is asserted twice, as ADR-004 requires — but by a TRIGGER, not a
`CHECK`.** ADR-004's argument for doubling up is that the domain check gives an
operator a sentence they can act on while the constraint stops any other writer
getting around it, and that argument is unchanged here. SQLite cannot add a
`CHECK` to an existing table: it would mean rebuilding `offers` — twelve columns,
three indexes, two existing constraints and a foreign key — and reversing that
rebuild in a down migration, on a project where **down migrations have never once
been run** (`docs/adr/BACKLOG.md`). A `BEFORE INSERT` / `BEFORE UPDATE` trigger
pair raising `ABORT` enforces the identical invariant, is added and dropped
without touching the table, and its down migration is two `DROP TRIGGER`
statements that are trivially correct.

An untitled offer renders as **"Untitled"** in muted type beside its reference and
photo count, never as an empty cell. An empty cell reads as a rendering fault; the
word plus the count reads as work waiting.

What would FAIL this decision: `TestADraftNeedsNoTitle` refusing an untitled
draft; a listed or pending offer being accepted with an empty title by either the
domain or the database; or `HoldReason` letting an untitled offer into an export.

## Alternatives Considered

- **A separate `photo_groups` aggregate** — a new table holding a batch of
  photographs with no title, converted into an offer once described, exactly as
  `internal/submission` converts one. **Rejected**, and it was the closest call.
  It leaves `Offer.Validate` untouched, which is worth something. But it puts a
  THIRD half-built-thing concept into a product that already has two (a draft
  offer and a submission), and every one of them needs its own repository,
  service, handlers, screens, photo-moving path and inbox. The thing being
  modelled here is not a new noun: it is an offer somebody has not finished
  writing. Modelling it as a separate noun would mean `MovePhotosToOffer` runs
  again, and the reference (ADR-010) would be allocated at conversion rather than
  at photograph time — so the label on the box could not be written until after
  the second person had done their job, which defeats the division of labour this
  record exists to enable.
- **Reuse the submissions inbox** — staff photograph into a submission with no
  title, and the existing review-and-convert flow catalogues it. **Rejected on
  ADR-011**, which separates the two on purpose: a submission is a *message from
  a person offering us something they own*, carrying an asking price, a submitter,
  and an accept/decline decision with a reason. None of that applies to our own
  stock being catalogued in two passes, and `Accept` builds the offer from
  `sub.Title` — the submitter's words — which is precisely the field the
  photographer does not have. It is also admin-only, and cataloguing is not an
  administrative act.
- **Let the photographer type a placeholder title** — the status quo.
  **Rejected**: it is the failure ADR-004 exists to prevent, and a placeholder in
  the TITLE is worse than one in the price, because `Offer.Validate` would happily
  let "lamp 2" reach a marketplace while a placeholder price of 0.00 is refused by
  both the domain and a constraint.
- **A dedicated multi-step intake wizard for one person** — considered first and
  set aside once M described the two-person flow. A wizard makes one person's
  single pass tidier; it does nothing for a hand-off between two people, and it
  would have had to ask for a title at step 1 anyway for exactly the reason this
  record removes.
- **Allow an untitled offer at ANY status, guarded only in the exporter** —
  **rejected**: the exporter is too late. `HoldReason` would silently drop the
  item from a download the operator believes is complete, which is the failure
  mode the "Not being sent" table already exists to prevent.

## Component / Boundary Impact

`internal/core` owns the rule and the derived predicate; `internal/offer` owns the
query; `internal/web` owns the queue's presentation. No new package, no new
aggregate, no new route beyond a query-string filter on the existing listing. One
reason to change: what the application considers a finished catalogue entry.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `core.Offer.Validate` | title required only when `Status.Exportable()` | `internal/core` | every write path |
| `core.Offer.NeedsDescribing()` | new, derived | `internal/core` | the listing row flag, the queue |
| `core.HoldReason` | new reason `"no title yet"` | `internal/core` | the export "Not being sent" table |
| `core.OfferFilter.NeedsDescribing` | new filter field | `internal/core` | `internal/offer` repository |
| `/offers?needs_describing=1` | new listing filter | `internal/web` | the nav entry, the chip, the tile |
| `Page.NeedsDescribing` | new count | `internal/web` | the sidebar badge |
| `offers_needs_describing_idx` | new index | migration 00008 | the queue query |
| `offers_title_required_when_published` triggers | new, insert + update | migration 00008 | every writer, including hand-run SQL |

## Inter-task Contracts

T2 consumes `core.Offer.NeedsDescribing` and `core.OfferFilter.NeedsDescribing`,
both produced by T1. T1 lands the rule and the queue's data path; T2 lands the
menu item and the screens. T1 is independently shippable and leaves no
half-visible state: the filter simply has nothing pointing at it yet.

## Implementation

Two tasks; see `tasks/README.md`.

## Consequences

- **Positive:** two people can share one intake. The photographer moves at the
  speed of a camera, and nobody invents a name for an object they are holding but
  cannot identify.
- **Positive:** the hand-off is visible. "Needs describing" is a queue with a
  count, so the work is discoverable rather than remembered.
- **Positive:** no new aggregate. The photograph group is an offer from the first
  moment, so it has its reference (ADR-010), its photo URLs (ADR-007) and its
  search row (ADR-012) immediately, and nothing has to be migrated between tables
  when it is finally described.
- **Positive:** the title is now guarded at the publication boundary by both the
  domain and the database, where before it was guarded at creation by the domain
  only. A hand-run `UPDATE` could previously publish an offer with an empty title;
  it can no longer.
- **Negative:** `Offer.Validate` no longer refuses an untitled offer outright, so
  a bug that drops a title now surfaces at publication rather than at write. The
  trigger and `TestADraftNeedsNoTitle`'s sibling cases are what bound that.
- **Negative:** every screen that renders a title must handle an empty one. Missing
  one leaves a blank cell that reads as a rendering fault.
- **Neutral:** existing data is unaffected — every offer already has a title, and
  the new rule only widens what is permitted.

## Out of Scope

- A dedicated cataloguing screen distinct from the offer editor (permanent: boundary: the editor already carries Title, Description, Condition and the per-offer category, and a second screen over the same fields is a second place to keep in step)
- Assigning a photograph group to a particular cataloguer, or any claim/lock so two people do not describe the same item at once (deferred: docs/adr/BACKLOG.md)
- Bulk-describing several groups in one screen (deferred: docs/adr/BACKLOG.md)
- Splitting or merging a photograph group after the fact — moving a picture from one item to another (deferred: docs/adr/BACKLOG.md)
- Deriving a suggested title from the photographs (permanent: boundary: this application makes no model calls and has no image pipeline, so adding one is a different decision with its own cost, latency and privacy surface)
- Allegro's and Shopify's per-offer categories (deferred: docs/adr/BACKLOG.md — ADR-016 wired eBay only)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An untitled offer reaches a marketplace | Low | **High** | Refused by `Validate` at the exportable boundary, by the trigger pair in the database, and by `HoldReason` before an export renders it — three independent gates, and the mutation log kills a mutant at each |
| A screen renders an untitled offer as a blank cell and looks broken | **Med** | Med | `TestUntitledOffersRenderAsUntitled` asserts the word appears on the listing; the row also carries the reference and the photo count |
| The queue and the predicate drift apart, so the count disagrees with the list | Med | Med | The repository clause and `NeedsDescribing()` are asserted against each other by `TestOffersFilterNeedsDescribingMatchesThePredicate`, the same pairing ADR-004 uses |
| The trigger fires on an UPDATE that never touched the title, blocking an unrelated edit | Low | Med | The trigger's `WHEN` tests the NEW row's status and title only, so an update that leaves both valid passes; asserted by a test that edits a listed offer's description |
| Somebody "fixes" `Validate` by restoring the unconditional title check | Med | Med | `TestADraftNeedsNoTitle` fails, and this record is named in its failure message |
| A down migration is needed and has never been exercised | Med | Low | This migration's down is two `DROP TRIGGER` and one `DROP INDEX`, which is why a trigger was chosen over rebuilding the table |

## Rollback

Drop the two triggers and the index (the down migration), restore the
unconditional title check in `Offer.Validate`, and remove the filter field, the
nav entry, the chip and the tile. Any untitled drafts created in the meantime
would then fail validation on their next write, so rollback should be preceded by
`SELECT id, sku FROM offers WHERE trim(title) = ''` and a pass to name them —
which is the describing work this record exists to make visible.

## Follow-ups

- Nothing here is browser-verified beyond the walk recorded in the T2 verification
  log; see `docs/adr/BACKLOG.md`.
