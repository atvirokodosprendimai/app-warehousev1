# Task ADR-020-T1: Intake is one click, and the editor reads camera-first and status-last

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** the posting "New offer" control, a `POST /offers` that reads no input, the deletion of `GET /offers/new` and `IntakeScreen`, the camera-first / status-last card order, and the reference as an editable field on Details
**Consumes:** `offer.Service.Create` and `offer.Repo.UpdateOffer` (both pre-existing and unchanged)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the New offer control creating rather than navigating`, `the create endpoint requiring no input`, `the camera-first card order`, `the status-last card order`, `the reference editable after creation`, `the New offer control on every page`

## Goal

Pressing "New offer" creates the draft and lands the operator on its
photographs, with everything else — including the reference, and finally the
status — available afterwards in any order.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/routes.go` | edit | `GET /offers/new` is removed; nothing replaces the address |
| `internal/web/handlers_offer.go` | edit | `GetIntake` and `intakeSignals` deleted; `PostOffers` reads no input; `offerSignals` gains the reference; `PostOffer` writes it |
| `internal/web/view/screens.templ` | edit | `IntakeScreen` deleted |
| `internal/web/view/fragments.templ` | edit | "New offer" becomes a button that posts |
| `internal/web/view/offers.templ` | edit | the empty-state "Add the first offer" likewise |
| `internal/web/view/offer_detail.templ` | edit | card order, and the reference becomes an input |
| `internal/web/view/intake_contract_test.go` | create | the assertions below |
| `internal/web/view/queue_contract_test.go` | edit | retires the test that renders the deleted screen, in place and with a reason |
| `internal/web/view/upload_contract_test.go` | edit | drops `IntakeScreen` from two screen maps |
| `scripts/smoke.sh` | edit | walks the new flow: create, then name |

<The control is what CREATES, and the card order is what the operator meets
next. Both are what the tests below assert.>

## Ordered Steps

1. [S1] Write the four tests below against the current markup and confirm all four are RED: the control is a link, Details precedes Photos, Status is not last, and no reference input exists.
2. [S2] Make `PostOffers` read no input and delete `intakeSignals`. [proof: acceptance]
3. [S3] Delete `GetIntake`, the `GET /offers/new` route and `IntakeScreen`; the smoke run asserts the address is gone rather than merely unlinked, because an unreachable screen still answering is a second way to create an offer that nothing tests. [proof: acceptance]
4. [S4] Turn both "New offer" / "Add the first offer" controls into buttons posting to `/offers`.
5. [S5] Reorder `OfferScreen`: Photos, Details, Pricing in column one; Where it lives, Batches, Marketplace, Status in column two.
6. [S6] Add the reference to `offerSignals`, seed it in `offerSignals(d)`, render it on the Details card, and write it in `PostOffer`.
7. [S7] Retire the two tests that render the deleted screen, in place, naming their successor.
8. [S8] Regenerate the committed `*_templ.go`. [proof: acceptance]
9. [S9] Rewrite the smoke walk to create then name, and assert the create endpoint needs no body. [proof: acceptance]
10. [S10] ⚠ **CORRECTION, 2026-09-07, after M reported it.** Render `NewOfferAction` in the SHELL rather than passing it from a handler, and stop passing it from the dashboard and the offers listing so it is not rendered twice. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate && \
go test ./internal/web/view/ -run '^TestCreatingAnOfferAsksNothingFirst$' -count=1 2>&1 | tee /tmp/adr020t1-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr020t1-new.out && \
go test ./internal/web/view/ -run '^(TestCreatingAnOfferAsksNothingFirst|TestEveryPageCarriesTheNewOfferControl|TestTheCameraIsTheFirstCard|TestStatusIsTheLastCard|TestTheReferenceIsEditableAfterCreation|TestNoFormsOutsideFileUpload|TestEveryIndicatorSignalHasAConsumer)$' -count=1 2>&1 | tee /tmp/adr020t1-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr020t1-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr020t1-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr020t1-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr020t1-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr020t1-smoke.out
```

`templ generate` leads because the committed generated files are what the view
tests read. The smoke run is last because it is the only segment that proves the
create ENDPOINT answers with no body — a rendered button proves the markup, and
only an HTTP request proves the route behind it needs nothing.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestCreatingAnOfferAsksNothingFirst` | `internal/web/view/intake_contract_test.go` | The "New offer" control posts to `/offers` and is not a link to a form, on both the chrome and the empty state | — | S1, S4 |
| `TestTheCameraIsTheFirstCard` | `internal/web/view/intake_contract_test.go` | Photos precedes Details and Pricing in DOM order, which on a phone is reading order | — | S1, S5 |
| `TestStatusIsTheLastCard` | `internal/web/view/intake_contract_test.go` | No card follows Status, so the decision M put last stays last | — | S1, S5 |
| `TestTheReferenceIsEditableAfterCreation` | `internal/web/view/intake_contract_test.go` | The Details card carries a bound reference input, so the capability the deleted screen held still exists | — | S1, S6 |
| `TestNoFormsOutsideFileUpload` | `internal/web/view/upload_contract_test.go` | The new button introduced no `<form>` (ADR-008) | — | S4, S7 |
| `TestEveryIndicatorSignalHasAConsumer` | `internal/web/view/upload_contract_test.go` | Any indicator added here is wired to something | — | S4, S7 |
| `TestEveryPageCarriesTheNewOfferControl` | `internal/web/view/intake_contract_test.go` | The shell renders the control on a page that passes no actions of its own, exactly once, without displacing a page's own action | — | S10 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCreatingAnOfferAsksNothingFirst` renders the chrome and finds the posting control |
| 2 — something selects it | The smoke run POSTs `/offers` with no body and gets an offer id back |
| 3 — the caller can discover it | `TestEveryPageCarriesTheNewOfferControl` renders the shell with no page actions and finds the control. ⚠ **THIS ROW WAS WRONG WHEN IT WAS FIRST WRITTEN.** It claimed "the control is in the top bar on every page" and nothing asserted it: the button was passed in by the DASHBOARD and the OFFERS LISTING handlers only, so every other page — including the offer editor an operator lands on immediately after creating — had none. M found it by doing the job on 2026-09-07: *"new offer should be visible on all pages, because i created photo and aditional clicks to create new ones"*. Photographing a shelf is a LOOP, and the loop was broken at exactly the point it repeats. Corrected by moving the control into the shell, where no handler can forget it, and pinned by the test named here. |
| 4 — it is used | The smoke run walks it end to end: create with nothing, name it afterwards, find it by that name |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.
- 2026-09-07 · 2883131* · mutant killed · exit 1 · `internal/web/view/fragments.templ` · the New offer control links to the deleted intake screen again, so the first thing a new installation offers is a 404 and nothing can be created at all · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · covers:the New offer control creating rather than navigating
- 2026-09-07 · 2883131* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · the reference binds a camelCase attribute that the HTML parser lowercases to offersku — a DIFFERENT signal than the handler reads, so editing the reference silently saves nothing and no error is raised anywhere · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · covers:the reference editable after creation
- 2026-09-07 · 2883131* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · the keyboard comes back before the camera, so an operator who has just pressed New offer and is still holding the object meets a title field first — the exact step M asked to remove · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · covers:the camera-first card order
- 2026-09-07 · 2883131* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · Status stops being the last block, so on a phone the publish/sold decision sits above the marketplace card again instead of at the end where M put it · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · covers:the status-last card order
- 2026-09-07 · 2883131* · mutant killed · exit 43 · `internal/web/handlers_offer.go` · the create endpoint demands a title again, so the top-bar button — which sends no body from any page — creates nothing and the one-click flow is dead everywhere while every view test still passes · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · covers:the create endpoint requiring no input
- 2026-09-07 · c25f529* · mutant killed · exit 1 · `internal/web/view/fragments.templ` · the New offer control links to the deleted intake screen again, so the first thing a new installation offers is a 404 and nothing can be created at all · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · covers:the New offer control creating rather than navigating
- 2026-09-07 · c25f529* · mutant killed · exit 47 · `internal/web/handlers_offer.go` · the create endpoint demands a title again, so the top-bar button — which sends no body from any page — creates nothing and the one-click flow is dead everywhere while every view test still passes · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · covers:the create endpoint requiring no input
- 2026-09-07 · c25f529* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · the keyboard comes back before the camera, so an operator who has just pressed New offer and is still holding the object meets a title field first — the exact step M asked to remove · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · covers:the camera-first card order
- 2026-09-07 · c25f529* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · Status stops being the last block, so on a phone the publish/sold decision sits above the marketplace card again instead of at the end where M put it · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · covers:the status-last card order
- 2026-09-07 · c25f529* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · the reference binds a camelCase attribute that the HTML parser lowercases to offersku — a DIFFERENT signal than the handler reads, so editing the reference silently saves nothing and no error is raised anywhere · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · covers:the reference editable after creation
- 2026-09-07 · c25f529* · mutant killed · exit 1 · `internal/web/view/layout.templ` · somebody decides two buttons is clutter and hides New offer wherever a page has its own action, so it disappears from the warehouse page again — the control is back to being present on some pages and not others, which is precisely what M reported · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · covers:the New offer control on every page

## Invariants

- No `<form>` is introduced outside the photo upload (ADR-008).
- Creating an offer never requires a request body.
- A created offer always has a reference, because `Service.Create` allocates one.
- The editor renders every card it rendered before; only their order changes.

## Risks

- A click writes to the database, so an accidental press leaves an untitled draft. ADR-019's queue is where it becomes visible and disposable; this task adds no confirmation step, which is deliberate and recorded in the parent.
- `POST /offers` ignoring its body means a stale client sending `newTitle` silently gets an untitled offer rather than an error. Accepted: there is no such client outside this repository, and the smoke script is updated in the same task.
- ⚠ **The reference input has no killed mutant for its COLLISION path.** The test asserts the field is rendered and bound; that a duplicate reference is refused is `Repo.UpdateOffer`'s pre-existing `ErrSKUTaken`, already covered in `internal/offer`. Nothing here re-proves it, and `Rests-on` claims only `the reference editable after creation`.

## Stop Condition

Stop and ask if removing the intake screen turns out to lose a capability this
task has not moved — the reference is the one that was found, and if a second
appears it is a sign the screen was carrying a decision nobody recorded.

## Out of Scope

- Exposing quantity in the interface — ADR-020 defers it as presentation of an existing field.
- Deleting an abandoned draft in one gesture — ADR-020 defers it.
- Enforcing an immutable published reference — ADR-020 refuses it as a new decision.

## Verification Log

To be completed by `adr-verify` during execution.
- 2026-09-07 · 2883131* · exit 0 · `set -o pipefail …` · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · ms:14057
- 2026-09-07 · 2883131* · exit 0 · `set -o pipefail …` · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · ms:13815
- 2026-09-07 · 2883131* · exit 0 · `set -o pipefail …` · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · ms:13925
- 2026-09-07 · 2883131* · exit 0 · `set -o pipefail …` · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · ms:13769
- 2026-09-07 · 2883131* · exit 0 · `set -o pipefail …` · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · ms:12982
- 2026-09-07 · 2883131* · exit 0 · `set -o pipefail …` · acceptance-sha256:7c3a73abe40cbff2feff5ca3aeb05562a4c62679d4b1e86ad59ed01dc7d8cb9c · ms:14408
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:14502
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:14253
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:14112
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:14056
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:13688
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:13896
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:14086
- 2026-09-07 · c25f529* · exit 0 · `set -o pipefail …` · acceptance-sha256:8f0b094541789bf2f92808fa710e998f038837ce0bde3477bc2fc8226327edf6 · ms:13783
