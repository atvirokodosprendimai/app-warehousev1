# ADR-020: Make intake one click, and order the editor camera-first, status-last

**Status:** Accepted
**Date:** 2026-09-07
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-004, ADR-008, ADR-010, ADR-011, ADR-019, `internal/web/handlers_offer.go`
**Governs:** `internal/web/handlers_offer.go`, `internal/web/routes.go`, `internal/web/view/screens.templ`, `internal/web/view/fragments.templ`, `internal/web/view/offers.templ`, `internal/web/view/offer_detail.templ`
**Enforced-by:** `internal/web/view/intake_contract_test.go::TestCreatingAnOfferAsksNothingFirst`
**Invalidates:** ADR-019's implementation detail that the intake SCREEN carries an optional title field. ADR-019's DECISION — that an undescribed draft is a normal state — is not weakened but relied upon: it is the reason a click alone can produce a valid offer.
**Served-path change:** Pressing "New offer" creates the draft and lands the operator on its photographs. Naming, pricing, shelving and everything else happen afterwards in any order, and the status controls sit last instead of above the work.

## Context

Reported by M on 2026-09-07: *"new offer 1st should create offer and instantly
show upload photos without aditional step, and editing shows photos first then
edit block"*, clarified in the same exchange as *"1step click offer, 2nd upload
photos 3rd - all other info"*, and then *"status: listing , draf, pending etc.
should be last block"*.

The intake screen asks three questions — a title, a reference, and a location —
and **every one of them is already optional.** ADR-019 made the title optional in
the domain. ADR-010 makes the reference the database's job, so leaving it blank
is the normal path. The location has its own card on the editor. So the screen
stands between the operator and the camera while collecting nothing that must be
collected there.

⚠ **The cost is paid at the shelf, by the person least able to pay it.** The
photographer is holding an object. Every second spent on a keyboard is a second
the object is in one hand. ADR-019 removed the *obligation* to name the thing but
left the *screen that asks*, and a form with three optional fields still reads as
work to be done: an operator does not know a field is safe to skip until somebody
tells them, and nobody is there to tell them.

This is the second half of the request ADR-019's own record left open, and the
mobile-drawer record named the condition precisely: *"a genuine wizard changes
the intake FLOW that ADR-004 and ADR-011 protect and would need its own record —
put to M rather than assumed."* It was put to M. This is the record.

⚠ **What M asked for is NOT a wizard.** A wizard is more steps, sequenced and
enforced. M asked for **fewer** steps and for the remaining ones to be
unsequenced: click, photograph, then everything else whenever. That distinction
is the whole decision, and it is why this record introduces no step machine, no
draft sub-state, and no "next" button.

⚠ **Status is last because it is the only card that is not data entry.** Every
other card records what the thing IS. The status card decides what the business
does with it — publish it, mark it sold — and M's ordering puts that after the
describing, not above it. It was previously the first card in the second column,
which on a phone put "Listed / Draft / Pending" between the photographs and the
price.

## Existing Primitives Audit

- **ADR-019's untitled draft** — **relied upon, not extended.** A click with no
  form can only produce a valid offer because a draft needs no title. Without
  ADR-019 this decision is unimplementable.
- **ADR-010's database-allocated reference** — **relied upon.** `Service.Create`
  already allocates a reference when none is given, with retries. The intake
  screen's "Reference" field was an override of a decision the database owns.
- **`Service.Create(ctx, title, sku)`** — **reused unchanged.** It already
  accepts two empty strings and produces a valid draft with `Quantity: 1`.
- **`Repo.UpdateOffer`** — **reused unchanged.** It already rewrites `sku`, so
  the reference override that leaves the intake screen has somewhere to land
  without new persistence code.
- **The offer editor's card set** — **reordered, not rebuilt.** Every card M
  wants in "step 3" already exists.
- **ADR-008's no-`<form>` rule** — **honoured.** The button is `data-on:click`
  posting to `/offers`, the same mechanism every other action uses.

## Decision

**1. "New offer" creates the offer on the click.** The control stops being a
link to `/offers/new` and becomes a button that posts to `/offers`. The handler
takes no input at all: it calls `Create(ctx, "", "")` and redirects to the new
offer. `GET /offers/new`, `GetIntake` and `IntakeScreen` are deleted rather than
left unreachable.

**2. The editor's first card is Photos, and Status is last.** DOM order becomes
Photos, Details, Pricing in the first column and Where it lives, Batches,
Marketplace, Status in the second. `.cols` is one column below 861px, so on a
phone the page reads exactly as M described it: camera, then everything else,
then status.

**3. The reference moves to step 3.** It becomes an editable field on the
Details card. This is the one capability the intake screen held that no other
screen offered, and M's flow moves it rather than removing it.

⚠ **A GET MUST NOT CREATE.** The old route was a GET that rendered a form; the
new control is a POST. Making `/offers/new` a GET that creates and redirects
would be smaller and is refused: a link that mutates is followed by prefetchers,
crawlers and the back button.

## Alternatives Considered

- **Keep the intake screen and only reorder the editor's cards** — the smallest
  change that answers M's second sentence. **Rejected** because it ignores the
  first: the screen IS the "aditional step" M named, and reordering cards behind
  it leaves the step in place.
- **Keep the screen but skip it when every field would be blank** — render the
  form only if there is something to ask. **Rejected** as a conditional flow: two
  paths to the same object, and the operator cannot predict which one they get,
  so the interface becomes something to learn rather than to use.
- **Create the offer on the click but land on Details as before** — half the
  change, and it is the half that ships worst. **Rejected**: it puts the operator
  back in the exact flow M rejected, one click earlier, which is why this record
  has one task rather than two.
- **Make it a real multi-step wizard with an enforced sequence** — the shape the
  earlier record guessed M might want. **Rejected on M's clarification**: the
  steps after the photograph are explicitly *"all other info"*, unordered, so
  enforcing a sequence would add the ceremony this record removes.
- **Drop the custom reference entirely rather than moving it** — simpler, and it
  reads as consistent with ADR-010. **Rejected** as a silent capability loss:
  ADR-010 gives the database the allocation, not a monopoly, and an operator
  re-cataloguing existing stock has references already written on the boxes.
- **A confirmation dialog before creating, to prevent accidental drafts** —
  **rejected**: it is precisely the extra step this record deletes, reintroduced
  under another name, and ADR-019's queue already makes an abandoned draft
  visible and disposable.

## Component / Boundary Impact

| Component | Impact |
|-----------|--------|
| `internal/web` | The only package that changes. One route deleted, one handler simplified, one screen deleted, cards reordered, one field moved. |
| `internal/offer` | None. `Create` and `UpdateOffer` already do what is needed. |
| `internal/core` | None. |
| `migrations` | None. |

## Wiring & Contract Changes

- `GET /offers/new` — **removed.** No replacement address; the button posts.
- `POST /offers` — **stops reading signals.** It previously parsed `newTitle`,
  `newSku` and `newLocation`; it now takes no body. An existing client sending
  those fields is not broken, it is ignored.
- `POST /offers/{id}` — **gains `offerSku`**, so the reference is correctable in
  step 3.

## Inter-task Contracts

None — this record has one task. The two halves ship together deliberately: a
one-click create that lands on Details puts the operator back in the flow this
record exists to remove, so shipping half is worse than shipping neither.

## Implementation

One task, `T1`. See `tasks/`.

## Consequences

- A click now writes to the database. Somebody who presses "New offer" and
  changes their mind leaves an untitled draft behind. **That is not new** —
  ADR-019 already made an untitled draft a normal, findable state, and it appears
  in the "Needs describing" queue where a person can see and delete it. It is
  more likely now, and that is the cost of the flow M asked for.
- The reference moves from a screen seen once to a card seen every time, so a
  reference typo becomes correctable rather than permanent.
- `POST /offers` accepting no body makes the button usable from any page,
  because it no longer depends on signals only one screen declared.
- The status controls move below the fold on a phone. That is the intent: they
  are the last thing you do, not the first thing you see.

## Out of Scope

- Exposing `core.Offer.Quantity` in the interface, which M asked for in the same message (deferred: docs/adr/BACKLOG.md — it is presentation of an existing, already-persisted, already-exported field rather than a flow decision, and carries no record for the same reason the mobile drawer and the responsive listing carry none)
- Deleting an abandoned draft in one gesture from the describing queue (deferred: docs/adr/BACKLOG.md)
- Any change to what a photograph is or where its bytes live (permanent: boundary: ADR-007 owns the photograph's identity and storage, and this record changes only the order in which a person meets the upload control)
- Enforcing that a published reference can never change (permanent: boundary: ADR-010 states it as guidance to the operator and no constraint ever enforced it, so adding enforcement here would be a new decision smuggled in under a reordering)
- A confirmation step before creating a draft (permanent: boundary: it is the step this record exists to delete, and ADR-019's queue already makes an abandoned draft visible)

## Risks

- **Accidental drafts.** Mitigated by ADR-019's queue rather than by a
  confirmation dialog: a dialog would reintroduce the step this record removes.
- **The reference becomes editable after publication.** `UpdateOffer` already
  returns `ErrSKUTaken` on a collision, and ADR-010's "once published it should
  not change" is guidance to the operator, not a constraint the database
  enforced. This record does not add an enforcement it did not have; it does make
  the field reachable, and the hint text says so.
- **The status card below the fold on a phone.** Somebody looking for "mark it
  sold" now scrolls. Accepted on M's instruction, and the card is still the last
  thing in a single readable column rather than hidden behind anything.

## Rollback

Restore `GetIntake`, `IntakeScreen` and the `GET /offers/new` route, and revert
the button to a link. Nothing persisted changes shape, so no migration is
involved and no data written under this decision becomes invalid.

## Follow-ups

- If accidental drafts become a real nuisance rather than a predicted one,
  BACKLOG's one-gesture delete is the answer, not a confirmation step.
