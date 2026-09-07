# Task ADR-019-T2: The undescribed groups are a queue somebody can find

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** the "Needs describing" sidebar entry with its count, the listing filter chip, the dashboard tile, the untitled row rendering, and an intake that does not demand a title
**Consumes:** `core.Offer.NeedsDescribing`, `core.OfferFilter.NeedsDescribing` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the sidebar count query`, `the needs_describing query-string filter`, `the untitled row rendering`, `the intake path that creates an offer with no title`

## Goal

A photographer can create a photograph group without naming it, and a cataloguer
can find every one of them from a menu item with a live count.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/app.go` | edit | Computes `Page.NeedsDescribing`, beside the existing pricing count |
| `internal/web/view/model.go` | edit | `Page.NeedsDescribing`; `RowsPath` carries the filter so live search keeps it |
| `internal/web/handlers_offer.go` | edit | Parses `needs_describing=1`; names the screen |
| `internal/web/view/layout.templ` | edit | The menu item M asked for |
| `internal/web/view/offers.templ` | edit | The filter chip, the dashboard tile, the row flag, and the untitled title |
| `internal/web/view/screens.templ` | edit | Intake stops demanding a title and says why |
| `internal/web/view/queue_contract_test.go` | create | The assertions below |
| `scripts/smoke.sh` | edit | The queue is reachable over HTTP and returns the untitled draft |

<The queue is SELECTED by `offerFilterFrom` reading `needs_describing=1`, and the
menu item is what makes that address reachable without typing it. Both are what
the tests below assert.>

## Ordered Steps

1. [S1] Write the three tests below against the current markup and confirm all three are RED: there is no menu entry, no untitled rendering, and intake still demands a title.
2. [S2] Add `Page.NeedsDescribing` and compute it in `a.page`, beside the pricing count.
3. [S3] Parse `needs_describing=1` in `offerFilterFrom`, and name the screen "Needs describing". [proof: acceptance]
4. [S4] Carry the filter in `OfferList.RowsPath`, so the live search does not silently drop it.
5. [S5] Add the sidebar entry, the filter chip and the dashboard tile.
6. [S6] Render an untitled offer as "Untitled" with its reference and photo count, never as an empty cell.
7. [S7] Make the intake title optional, and say on screen that an unnamed item goes to the describing queue.
8. [S8] Regenerate the committed `*_templ.go`. [proof: acceptance]
9. [S9] Extend `scripts/smoke.sh` to walk the two-person flow over HTTP: create untitled, find it in the queue, describe it, watch it leave. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate && \
go test ./internal/web/view/ -run '^TestUntitledOffersRenderAsUntitled$' -count=1 2>&1 | tee /tmp/adr019t2-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr019t2-new.out && \
go test ./internal/web/view/ -run '^(TestUntitledOffersRenderAsUntitled|TestTheDescribingQueueIsReachableFromTheMenu|TestIntakeDoesNotDemandATitle|TestRowsPathKeepsTheDescribingFilter|TestNoFormsOutsideFileUpload|TestEveryIndicatorSignalHasAConsumer)$' -count=1 2>&1 | tee /tmp/adr019t2-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr019t2-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr019t2-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr019t2-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr019t2-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr019t2-smoke.out
```

`templ generate` leads because the committed generated files are what the tests
read. The smoke run is the last segment because it is the only one that exercises
the queue as an ADDRESS — a rendered link proves the markup, and only an HTTP
request proves the route answers.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestUntitledOffersRenderAsUntitled` | `internal/web/view/queue_contract_test.go` | An untitled row shows the word, its reference and its photo count, never a blank cell | — | S1, S6 |
| `TestTheDescribingQueueIsReachableFromTheMenu` | `internal/web/view/queue_contract_test.go` | The sidebar renders a link to `/offers?needs_describing=1` carrying its count | — | S1, S2, S5 |
| `TestIntakeDoesNotDemandATitle` | `internal/web/view/queue_contract_test.go` | Intake carries no `required` on the title and says where an unnamed item goes | — | S1, S7 |
| `TestRowsPathKeepsTheDescribingFilter` | `internal/web/view/queue_contract_test.go` | The live-search path keeps the filter, so typing in the box does not silently widen the queue to every offer | — | S4 |
| `TestNoFormsOutsideFileUpload` | `internal/web/view/upload_contract_test.go` | The change introduced no form | — | — |
| `TestEveryIndicatorSignalHasAConsumer` | `internal/web/view/upload_contract_test.go` | Any indicator added here is wired to something | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheDescribingQueueIsReachableFromTheMenu` renders the sidebar and finds the link |
| 2 — something selects it | `offerFilterFrom` reads the query parameter; the smoke run drives the real URL and gets the untitled draft back |
| 3 — the caller can discover it | The menu item IS the discovery path, and that is the thing M asked for — an address nobody links to is not a queue |
| 4 — it is used | The smoke run walks it end to end: create untitled, appear in the queue, describe, leave the queue |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.

## Invariants

- The queue's count and its listing agree, because both come from the same filter.
- An untitled offer never renders as an empty cell anywhere a person can reach.
- No `<form>` is introduced outside the photo upload (ADR-008).
- The intake still allocates a reference, so an unnamed item is still labellable.

## Risks

- A screen that renders `Offer.Title` and was not updated shows a blank. The test covers the listing; the offer editor shows an empty input, which is correct there because that is the field being filled in.
- The sidebar count runs a second query on every page render, as the pricing count already does. Same cost, same justification, and the same warn-and-continue handling if it fails.

## Stop Condition

Stop and ask if the queue turns out to need per-person assignment to be usable —
that is a different decision (ADR-019 defers it) and building it here would widen
this task into a workflow feature.

## Out of Scope

- Assigning a group to a cataloguer, or locking one while it is being described — ADR-019 defers both.
- Bulk describing — ADR-019 defers it.
- A cataloguing screen separate from the offer editor — ADR-019 rejects it with a reason.

## Verification Log

To be completed by `adr-verify` during execution.
