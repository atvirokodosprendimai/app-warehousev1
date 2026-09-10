# Task ADR-021-T3: An offer is asked its category's questions, root first

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** a category picker and a Details-adjacent card on the offer editor rendering every inherited field, and the handler that stores the answers
**Consumes:** the `TaxonomyStore` ports T1 produces, and `core.Offer.CategoryID` / `Offer.Fields`
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `an offer's fields are its ancestors' fields root-first`, `each of the five kinds renders its own control`, `an unrecognised kind renders as text rather than failing the page`, `changing the category re-asks the questions`, `a field card is never before the photographs`

## Goal

An operator who has photographed a turbocharger and filed it under
"Car parts › Engine › Turbocharger" is asked the engine questions and the
turbocharger questions, in that order, on the screen they are already on.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/routes.go` | edit | `POST /offers/{id}/fields` |
| `internal/web/handlers_offer.go` | edit | the category picker's write, and the field-value write |
| `internal/web/view/offer_detail.templ` | edit | the picker on Details, and a card rendering the resolved fields |
| `internal/web/view/offer_fields_contract_test.go` | create | the assertions below |
| `scripts/smoke.sh` | edit | files an offer under a category, answers a field, reads it back |
| `internal/web/view/model.go` | edit | `OfferDetail.Categories` and `.Fields`, `FieldsByLevel`, and `FieldSignal`/`FieldBind` — the per-field datastar binding, which is a spread because templ attribute names are static |

<The card renders whatever T1 resolved. It holds no knowledge of which fields
exist — that is the whole point of the record, and a test that hard-codes a
field name would be asserting a fixture rather than the mechanism.>

## Ordered Steps

1. [S1] Write the tests below against the current editor and confirm they are RED: there is no picker, no field card, and no `/fields` route.
2. [S2] Render the category picker on the Details card, bound kebab-case, storing `Offer.CategoryID`. ⚠ Not `Offer.Categories` — that is ADR-016's per-marketplace map on the Marketplace card, and this task must not touch it. [proof: acceptance]
3. [S3] Render a card holding the resolved fields, root-first, in `position` order within each ancestor, each labelled with the level it comes from so an operator can see why they are being asked. [proof: acceptance]
4. [S4] Render a control per kind: `text` an input, `longtext` a textarea, `number` an input with `inputmode="numeric"` and its unit shown beside it, `choice` a select over the operator's options, `bool` a checkbox. [proof: acceptance]
5. [S5] Render an UNRECOGNISED kind as a text input rather than failing the page — the parent ADR's Risks say a row can carry a kind an older binary does not know, and a page that will not render is a worse answer than a field that reads as text. [proof: acceptance]
6. [S6] Store the answers keyed by field id, and re-render the card when the category changes so the questions follow the filing. [proof: acceptance]
7. [S7] Keep the card AFTER Photos and BEFORE Status: ADR-020 put the camera first and the status last, and this card is neither. [proof: acceptance]
8. [S8] Regenerate the committed `*_templ.go`. ⚠ Through `templ generate` — never by editing a `*_templ.go`. [proof: acceptance]
9. [S9] Extend the smoke walk. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate && \
go test ./internal/web/view/ -run '^TestAnOfferIsAskedItsAncestorsQuestions$' -count=1 2>&1 | tee /tmp/adr021t3-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t3-new.out && \
go test ./internal/web/... -run '^(TestAnOfferIsAskedItsAncestorsQuestions|TestEachFieldKindRendersItsOwnControl|TestAnUnknownKindRendersAsText|TestTheFieldCardSitsAfterPhotosAndBeforeStatus|TestTheCameraIsTheFirstCard|TestStatusIsTheLastCard|TestNoFormsOutsideFileUpload|TestEveryIndicatorSignalHasAConsumer)$' -count=1 2>&1 | tee /tmp/adr021t3-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t3-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr021t3-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t3-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr021t3-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr021t3-smoke.out
```

ADR-020's two ordering tests are inside the fence on purpose: this task inserts a
card into the column those tests describe, and an insertion is exactly how a
card order gets broken by somebody who was not thinking about it.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnOfferIsAskedItsAncestorsQuestions` | `internal/web/view/offer_fields_contract_test.go` | An offer under a three-deep node renders all three levels' fields, root first, each labelled with its level | — | S1, S3 |
| `TestEachFieldKindRendersItsOwnControl` | `internal/web/view/offer_fields_contract_test.go` | All five kinds render distinct controls, and `number` shows its unit | — | S4 |
| `TestAnUnknownKindRendersAsText` | `internal/web/view/offer_fields_contract_test.go` | A row carrying a sixth kind renders a text input and the page still renders | — | S5 |
| `TestTheFieldCardSitsAfterPhotosAndBeforeStatus` | `internal/web/view/offer_fields_contract_test.go` | The new card did not displace the camera from first or the status from last | — | S7 |
| `TestTheCameraIsTheFirstCard` | `internal/web/view/intake_contract_test.go` | ADR-020's ordering survives the insertion | — | S7 |
| `TestStatusIsTheLastCard` | `internal/web/view/intake_contract_test.go` | Likewise | — | S7 |
| `TestNoFormsOutsideFileUpload` | `internal/web/view/upload_contract_test.go` | The picker and the card introduced no `<form>` (ADR-008) | — | S2, S4 |
| `TestEveryIndicatorSignalHasAConsumer` | `internal/web/view/upload_contract_test.go` | Every busy indicator this card adds is wired to something | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestAnOfferIsAskedItsAncestorsQuestions` renders the editor and finds the fields |
| 2 — something selects it | `POST /offers/{id}/fields` answers; the smoke walk stores a value |
| 3 — the caller can discover it | The card is on the offer editor an operator already lands on after pressing New offer (ADR-020) — no new navigation to find, which is the point of putting it there rather than behind a link |
| 4 — it is used | The smoke walk files an offer under a category, answers a field, and reads the value back on a fresh GET |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/view/model.go` · the levels render leaf-first, so an operator filing a turbocharger is asked the turbo questions before the general car ones — every question is still on the page and nothing looks missing, only the order the record chose is reversed · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · covers:an offer's fields are its ancestors' fields root-first
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · a long-text question falls through to the default branch and renders a single-line input, so a paragraph about what is wrong with a part has to be typed into a one-line box and is silently truncated by nothing but the operator giving up · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · covers:each of the five kinds renders its own control
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · a field whose kind this binary does not recognise renders NO control at all — the label is there, the input is not, so the question is asked and cannot be answered. This is the exact state a row written by a newer version and read by an older one produces · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · covers:an unrecognised kind renders as text rather than failing the page
- 2026-09-07 · b35d8d8* · mutant killed · exit 5 · `internal/web/handlers_offer.go` · filing an offer under a category is accepted and flashes "Saved", and nothing is stored — so the operator picks a category, is told it worked, and the questions never appear. A silent no-op behind a success message is the worst shape this failure could take · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · covers:changing the category re-asks the questions
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · the questions card is put above the photographs, so an operator who has just pressed New offer and is still holding the object meets a form before the camera — the exact step ADR-020 exists to remove, reintroduced by a card that was added later and not thought about · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · covers:a field card is never before the photographs

## Invariants

- No `<form>` outside the photo upload (ADR-008).
- Every `data-bind:` is kebab-case.
- A category is never REQUIRED to save an offer (ADR-004, ADR-019, and the parent's Decision 5).
- The camera stays first and the status stays last (ADR-020).

## Risks

- **A field's control is generated from data, so a malformed option list reaches the markup.** The `choice` options are operator-authored text and are escaped by templ like everything else; the risk is a list of two hundred options rendering as an unusable select, not an injection.
- **`Offer.Fields` is empty on the list read path** by T1's S7. This task only reads whole offers, so nothing here notices — but a later screen that renders the card from a listing would silently show no fields. Named here because that is where somebody would hit it.
- **A required field is NOT enforced** and this task adds no asterisk that implies it is. The parent ADR is explicit: demanding a value at the shelf produces a placeholder. Rendering the `required` flag as a hint without a block is deliberate.

## Stop Condition

Stop and ask if rendering the resolved fields turns out to need a second query
per field — the resolution is one ancestor walk by design, and a per-field read
appearing means T1's port is the wrong shape rather than that this screen needs
a cache.

## Out of Scope

- Shaping the tree — T2 owns it.
- Anything in `internal/export` — T4 owns it.
- Filtering the listing by a field's value — ADR-021 defers it to `BACKLOG.md`.
- Blocking publication on a missing required field — ADR-021 refuses it permanently.

## Verification Log

To be completed by `adr-verify` during execution.
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:15138
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:15066
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:15244
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:15020
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:14784
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:14606
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:63b62054c60e806b059ba5ff9bf6801a14ecf388aae135582dfe8d37cd81fc23 · ms:14314
