# Task ADR-022-T1: Give a marketplace must-have a closed vocabulary and one read path

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — the rename reaches every export profile)
**Owner:** unassigned
**Produces:** `core.MarketplaceField`, `core.MarketplaceFields()`, `core.MarketplaceOption`, `core.Offer.Marketplace`, `core.Offer.MarketplaceValue()`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the closed vocabulary`, `the option's own validation`, `the single accessor`

## Goal

Replace `core.Offer.Categories` with `core.Offer.Marketplace` keyed by `{Profile, Field}`, add the
closed `MarketplaceField` vocabulary and the `MarketplaceOption` type, so every must-have value is
read through one accessor instead of a bare map lookup per profile.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/marketplace.go` | add | The vocabulary, the option type and its validation |
| `internal/core/marketplace_test.go` | add | The failing tests, written first |
| `internal/core/offer.go` | edit | `Categories` → `Marketplace`; add `MarketplaceValue` |
| `internal/core/settings.go` | edit | `ValidExportProfile` is reused by option validation; no behaviour change |
| `internal/export/ebay.go` | edit | The one existing reader of `o.Categories` — **this is the line that selects the accessor**; deleting it makes an offer export under the default and no test would say so without T1's export assertion |
| `internal/offer/repo.go` | edit | Builds the map on read |
| `internal/offer/service.go` | edit | `SetCategory` keeps its name until T2 widens it |
| `internal/web/view/offer_detail.templ` | edit | Seeds the signal from the map |
| `internal/export/export_test.go`, `internal/export/ebay_test.go`, `internal/export/recar_test.go`, `internal/offer/repo_test.go` | edit | Fixtures build the map |

## Ordered Steps

1. [S1] Write `TestMarketplaceFieldVocabularyIsClosed`, `TestAMarketplaceOptionRefusesWhatCannotReachACSV` and `TestOfferMarketplaceValueIsTheOnlyWayIn` in `internal/core/marketplace_test.go` and confirm they are RED — the types do not exist, so the package does not compile. [proof: acceptance]
2. [S2] Add `core.MarketplaceField` with `FieldCategory`/`FieldCondition`/`FieldLocation`, `MarketplaceFields()` and `ValidMarketplaceField`. ⚠ CLOSED, not open: each member has a home in `export.Options`, so a fourth is a code change rather than a form field. An open vocabulary would let the settings screen mint a field nothing reads.
3. [S3] Add `core.MarketplaceOption` with `Validate()`. ⚠ It refuses an empty `Value` AND an empty `Label` separately: a blank value writes an empty cell into a required marketplace column, and a blank label renders a dropdown entry nobody can choose between. Refuse an unknown profile through the existing `ValidExportProfile` rather than a second list.
4. [S4] **Delete** `Offer.Categories` and add `Offer.Marketplace map[MarketplaceKey]string`. ⚠ DELETE, never alias — the compiler is the enumerator here. `Offer.CategoryID` is a different thing one letter away (ADR-021), so a textual sweep would edit the wrong sites, which is precisely how the `Photo.ParentID` rename was done and why it was done that way. [proof: acceptance]
5. [S5] Add `Offer.MarketplaceValue(profile string, f MarketplaceField) string`, the one read path, returning empty when unset. Fallback to a configured default is the CALLER's job — an accessor that silently substituted a default would make "this offer says nothing" indistinguishable from "this offer says the same as the default", and T4's dropdown has to tell them apart.
6. [S6] Fix every compile error the deletion produces, in `internal/export`, `internal/offer` and `internal/web`. [proof: acceptance]
7. [S7] Assert in `internal/export/ebay_test.go` that eBay still prefers the offer's own category over `Options.Category`, through the new accessor. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ -count=1 \
  -run '^TestMarketplaceFieldVocabularyIsClosed$|^TestAMarketplaceOptionRefusesWhatCannotReachACSV$|^TestOfferMarketplaceValueIsTheOnlyWayIn$' \
  2>&1 | tee /tmp/adr022-t1.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr022-t1.out \
  && go build ./... \
  && go test ./internal/core/ ./internal/export/ ./internal/offer/ -count=1 2>&1 | tee -a /tmp/adr022-t1.out \
  && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr022-t1.out
```

The three new tests run ALONE first so none of the existing suites can carry the verdict. `go build
./...` is inside the fence on purpose: it is what proves the deletion in S4 was followed everywhere,
and it is the only check that can see a reader nobody converted.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestMarketplaceFieldVocabularyIsClosed` | `internal/core/marketplace_test.go` | `MarketplaceFields()` is exactly the three, and `ValidMarketplaceField` refuses anything else | — | S2 |
| `TestAMarketplaceOptionRefusesWhatCannotReachACSV` | `internal/core/marketplace_test.go` | Empty value, empty label and unknown profile are each refused with their own message | — | S3 |
| `TestOfferMarketplaceValueIsTheOnlyWayIn` | `internal/core/marketplace_test.go` | The accessor returns the stored value per `{profile, field}`, empty when unset, and does not substitute a default | — | S5 |
| `TestEBayPrefersTheOffersOwnCategory` | `internal/export/ebay_test.go` | eBay reads the offer's category through the accessor and falls back to `Options.Category` | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The three `internal/core` tests |
| 2 — something selects it | `internal/export/ebay.go` reads `MarketplaceValue`; the mutation on that line proves the read is reached |
| 3 — the caller can discover it | `MarketplaceFields()` is what T3's settings screen enumerates; a field absent from it cannot be configured at all |
| 4 — it is used | Nothing measures this yet — T4's smoke assertions are the first observation of a value actually chosen |

## Mutation Log

## Invariants

- `Offer.Owner` is not reachable through any of this; a marketplace must-have is never a price.
- Empty stays legal: no method here refuses an offer that names no marketplace value (ADR-004, ADR-019).
- `internal/export` stays pure — the values ride on the offer, nothing here opens a database.

## Risks

- The rename touches four packages at once, so the commit is large. Mitigated by the compiler: there is no partial state that builds.
- `Offer.Marketplace` is a map with a struct key, which is unusual in this codebase. It is the shape that makes the accessor total; a nested map would need two lookups and a nil check at every call site.

## Stop Condition

Stop and ask if `Offer.Categories` turns out to be read anywhere that cannot name a
`MarketplaceField` — that would mean the vocabulary is not actually closed and the decision needs
revisiting before the schema in T2 is committed to.

## Out of Scope

- The tables — T2 owns storage.
- Widening `SetCategory` — T2.
- Any interface change — T3 and T4.

## Verification Log
