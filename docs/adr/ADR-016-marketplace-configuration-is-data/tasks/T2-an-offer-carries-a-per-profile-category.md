# Task ADR-016-T2: An offer carries a per-profile category

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** M
**Produces:** `core.Offer.Categories` (T2), the `offer_categories` table
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the categories loading with the offer rather than on demand`, `the cascade that removes them with the offer`

## Goal

An offer can name its own category per marketplace profile, stored beside it and
loaded with it, so mixed stock exports in one file.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `migrations/00007_offer_categories.sql` | add | The table, its cascade and its index |
| `internal/core/offer.go` | edit | `Categories map[string]string` on `Offer`, and the profile-name validation |
| `internal/offer/repo.go` | edit | Load categories with the offer and with a list; write them on update |
| `internal/offer/service.go` | edit | `SetCategory`, the only writer — this is what SELECTS the column |
| `internal/web/view/offer_detail.templ` | edit | A category input per registered profile on the offer page |
| `internal/web/handlers_offer.go` | edit | The handler behind that input; without it the field is decoration |

## Ordered Steps

1. [S1] Write `TestOfferRoundTripsItsPerProfileCategories` and confirm it is RED — the column does not exist.
2. [S2] Add `migrations/00007_offer_categories.sql` with the cascade and index.
3. [S3] Add `Categories` to `core.Offer` and validate the profile name.
4. [S4] Load categories in `Offer`, `OfferBySKU` and `Offers` — in ONE query for the list path, as the photo loader already does.
5. [S5] Add `SetCategory` to the service and the field plus handler to the offer page.

## Acceptance

```bash
set -o pipefail
go test ./internal/offer/ -run '^TestOfferRoundTripsItsPerProfileCategories$' -count=1 2>&1 | tee /tmp/adr016-t2-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t2-new.out && \
go test ./internal/offer/ ./internal/store/ -count=1 2>&1 | tee /tmp/adr016-t2-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t2-reg.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestOfferRoundTripsItsPerProfileCategories` | `internal/offer/repo_test.go` | A category saved against a profile comes back with the offer | — | S1, S2, S3, S4 |
| `TestOffersLoadEveryRowsCategoriesInOneQuery` | `internal/offer/repo_test.go` | The list path does not issue one query per offer | — | S4 |
| `TestDeletingAnOfferCascadesItsCategories` | `internal/offer/repo_test.go` | Categories do not outlive the offer they describe | — | S2 |
| `TestSetCategoryRefusesAnUnknownProfile` | `internal/offer/service_test.go` | A typo'd profile cannot mint a category nothing will ever read | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestOfferRoundTripsItsPerProfileCategories` |
| 2 — something selects it | `Service.SetCategory` and the handler behind the offer-page input; the mutation targets the loader, so a category that is stored and never read goes red |
| 3 — the caller can discover it | The per-profile input on the offer page, one per registered profile |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

<Tool-written by `adr-verify … --mutant …`. Empty at authoring.>

## Invariants

- A profile name that no exporter is registered under is refused, so a typo cannot mint a category nothing reads.
- Categories cascade with their offer.
- An offer with no category is valid and exports on the default — ADR-004's intake flow is not blocked.

## Risks

- The list path can regress into one query per offer. `TestOffersLoadEveryRowsCategoriesInOneQuery` is what refuses that.

## Stop Condition

Stop and ask if loading categories with every offer measurably slows the offers
list — the alternative is loading them only on the export path, which is a
different decision from the one ADR-016 records.

## Out of Scope

- Per-offer condition or item location — ADR-016 rejects both with a reason.
- A category picker or taxonomy browser (deferred: docs/adr/BACKLOG.md)

## Verification Log

<Tool-written by `adr-verify <this-file>` — do not hand-write entries.>
