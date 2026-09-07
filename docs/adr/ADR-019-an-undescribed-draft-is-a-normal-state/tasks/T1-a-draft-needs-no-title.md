# Task ADR-019-T1: A draft needs no title, and publication still refuses one without

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** `core.Offer.NeedsDescribing`, `core.OfferFilter.NeedsDescribing`, the relaxed `Offer.Validate`, the `HoldReason` case, and the trigger pair guarding publication in the database
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exportable-status boundary in Offer.Validate`, `the derived NeedsDescribing predicate`, `the repository clause that must agree with that predicate`, `the database trigger that no Go path can bypass`, `HoldReason refusing an untitled offer before an export renders it`

## Goal

An offer may be created and kept as a draft with no title, and may not enter a
status that is exported until it has one — refused by the domain and, separately,
by the database.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/offer.go` | edit | Moves the title requirement to the exportable boundary; adds `NeedsDescribing` |
| `internal/core/cart.go` | edit | `HoldReason` gains "no title yet", so an export explains the hold rather than dropping the row |
| `internal/core/ports.go` | edit | `OfferFilter.NeedsDescribing`, the queue's contract |
| `internal/offer/repo.go` | edit | The SQL clause behind that filter |
| `migrations/00008_an_undescribed_draft.sql` | create | The trigger pair and the queue index |
| `internal/core/offer_test.go` | edit | `TestADraftNeedsNoTitle` and the publication-refusal siblings |
| `internal/offer/repo_test.go` | edit | The filter must return exactly what the predicate says |

<The rule is SELECTED by `Offer.Validate`, which every write path already calls —
there is no registry and no new call site. The database half is selected by the
trigger, which fires on any writer at all, which is the whole reason it exists.>

## Ordered Steps

1. [S1] Write `TestADraftNeedsNoTitle` and confirm it is RED against the current unconditional check.
2. [S2] Move the title requirement in `Offer.Validate` to the `Status.Exportable()` boundary, beside the shop-price rule it mirrors.
3. [S3] Add `NeedsDescribing()` — `StatusDraft && Title == ""` — derived, never stored.
4. [S4] Add the `"no title yet"` case to `HoldReason`, ordered after the status and price cases so the most fundamental reason is reported first.
5. [S5] Add `OfferFilter.NeedsDescribing` and its repository clause, and assert the clause agrees with the predicate. [proof: acceptance]
6. [S6] Add migration 00008: the `BEFORE INSERT` / `BEFORE UPDATE` trigger pair raising `ABORT`, and the queue index. Down drops all three. ⚠ It is 00008, not 00006 — 00006 and 00007 were already taken, and goose panics on a duplicate version rather than skipping it.
7. [S7] Correct ADR-004's line 47, which says `Validate` requires a title, and point it at ADR-019.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ -run '^TestADraftNeedsNoTitle$' -count=1 2>&1 | tee /tmp/adr019-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr019-new.out && \
go test ./internal/core/ ./internal/offer/ -run '^(TestADraftNeedsNoTitle|TestAPublishedOfferStillNeedsATitle|TestHoldReasonNamesAMissingTitle|TestADR004NoLongerClaimsValidateRequiresATitle|TestOffersFilterNeedsDescribingMatchesThePredicate|TestPublishingAnUntitledOfferIsRefusedByTheDatabase)$' -count=1 2>&1 | tee /tmp/adr019-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr019-named.out && \
go test ./internal/core/ ./internal/offer/ ./internal/cart/ ./internal/store/ -count=1 2>&1 | tee /tmp/adr019-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr019-reg.out
```

The new unit runs alone first so it cannot be carried by its siblings; then every
test this task NAMES runs under one filter, so the table cannot promise a check
the gate never selects; then the four packages that touch the rule, the queue and
the migrations run whole as the regression half. `internal/store` is in that list
because ADR-013 runs every test against the real migrations, so it is what proves
migration 00008 applies at all.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestADraftNeedsNoTitle` | `internal/core/offer_test.go` | A draft with no title validates, and `NeedsDescribing` reports it | — | S1, S2, S3 |
| `TestAPublishedOfferStillNeedsATitle` | `internal/core/offer_test.go` | Listed and pending are refused with an empty title | — | S2 |
| `TestHoldReasonNamesAMissingTitle` | `internal/core/offer_test.go` | An untitled offer is held out of an export with a reason rather than dropped | — | S4 |
| `TestADR004NoLongerClaimsValidateRequiresATitle` | `internal/core/offer_test.go` | The record this one supersedes no longer asserts the rule it superseded | — | S7 |
| `TestOffersFilterNeedsDescribingMatchesThePredicate` | `internal/offer/repo_test.go` | Every row the filter returns satisfies `NeedsDescribing`, and no row it omits does | — | S5 |
| `TestPublishingAnUntitledOfferIsRefusedByTheDatabase` | `internal/offer/repo_test.go` | The trigger refuses it even when the domain is bypassed | — | S6 |

⚠ `TestADR004NoLongerClaimsValidateRequiresATitle` reads a MARKDOWN file from a Go
test, which is unusual and deliberate. This corpus has already shipped a record
asserting something untrue for a day (ADR-013, corrected 2026-09-07), and the cost
was a session planning against it. S7 is otherwise a documentation edit that no
gate can see, which is exactly the class of step that silently does not happen.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestADraftNeedsNoTitle` calls `Validate` and `NeedsDescribing` directly |
| 2 — something selects it | Every write path already calls `Validate`; the trigger is selected by the database on any write at all. The Mutation Log is what shows the tests can see both |
| 3 — the caller can discover it | `OfferFilter.NeedsDescribing` is a documented field on an exported struct; `NeedsDescribing()` is an exported method with a doc comment |
| 4 — it is used | T2 is the user of both. Until it lands, this task is reachable and deliberately unused |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.
- 2026-09-07 · 5e89f28* · mutant killed · exit 1 · `internal/core/offer.go` · removing the exportable boundary lets an untitled offer be listed, and a marketplace receives a listing headed by an empty string · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · covers:the exportable-status boundary in Offer.Validate
- 2026-09-07 · 5e89f28* · mutant killed · exit 1 · `internal/core/offer.go` · a predicate that never fires empties the describing queue, so the second person is told there is no work while the shelf fills up · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · covers:the derived NeedsDescribing predicate
- 2026-09-07 · 5e89f28* · mutant killed · exit 1 · `internal/offer/repo.go` · a filter that never applies returns every offer, so the queue and the predicate disagree and the sidebar count promises work the listing does not show · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · covers:the repository clause that must agree with that predicate
- 2026-09-07 · 5e89f28* · mutant killed · exit 1 · `migrations/00008_an_undescribed_draft.sql` · moving the guard off UPDATE lets a writer that never calls Validate publish an untitled offer by changing its status, which is exactly the bypass the database half exists to stop · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · covers:the database trigger that no Go path can bypass
- 2026-09-07 · 5e89f28* · mutant killed · exit 1 · `internal/core/cart.go` · without this case an untitled offer is dropped from an export silently instead of appearing in the not-being-sent table with a reason · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · covers:HoldReason refusing an untitled offer before an export renders it

## Invariants

- A status that is exported never carries an empty title, whichever writer wrote it.
- `NeedsDescribing` stays derived. Nothing stores it.
- The repository clause and the predicate stay in agreement.
- The SKU requirement is untouched: an offer without a reference is still refused at creation, because ADR-010 makes the reference the thing written on the box.

## Risks

- Relaxing a validation rule is the kind of change that is easy to over-apply. The title is refused at exactly one new boundary and nowhere else; the SKU, the status and the quantity rules are unchanged.
- The trigger fires on every write to `offers`, including updates that do not touch the title. Its `WHEN` reads only the NEW row, so a valid row passes regardless of what changed.

## Stop Condition

Stop and ask if relaxing `Validate` turns out to break a caller that relies on it
to reject empty input for a different reason — that would mean the rule was doing
two jobs and the decision in ADR-019 needs revisiting rather than the
implementation.

## Out of Scope

- The menu item, the chip and the tile — T2.
- Any cataloguing screen — ADR-019 rejects a second one with a reason.

## Verification Log

To be completed by `adr-verify` during execution.
- 2026-09-07 · 5e89f28* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · ms:3116
- 2026-09-07 · 5e89f28* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · ms:4105
- 2026-09-07 · 5e89f28* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · ms:3475
- 2026-09-07 · 5e89f28* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · ms:3183
- 2026-09-07 · 5e89f28* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · ms:3375
- 2026-09-07 · 5e89f28* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ff8fb0db286ab290873f653fac4fb69c0293e832f8f9b5ecc21bf3c88c352a2 · ms:3363
