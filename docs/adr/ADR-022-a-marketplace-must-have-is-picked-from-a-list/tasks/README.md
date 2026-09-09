# ADR-022 Tasks

Implementation tasks for ADR-022: Pre-enter a marketplace's must-have values and pick each one from
a list. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` headers. This README is a
derived index — when it disagrees with a task file, the task file wins and this is regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T3 |

Strictly sequential, and the chain is real rather than conservative: T2 stores the types T1 defines,
T3 reads the repository T2 builds, and T4 renders the read model T3 produces.

⚠ **T3 ships before T4 on purpose.** The dropdown is useless until a list exists, so the screen that
fills the list has to be reachable first. A warehouse that upgrades mid-way gets the option editor
and the unchanged text box, which is the order in which each half is worth having.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Give a marketplace must-have a closed vocabulary and one read path | pending | — | `go test ./internal/core/ -run '^TestMarketplaceFieldVocabularyIsClosed$\|…' && go build ./... && go test ./internal/core/ ./internal/export/ ./internal/offer/` |
| T2 | Store the option lists, and widen the per-offer table to hold three kinds of value | pending | — | `go test ./internal/marketplace/ -run '^TestAnExistingOfferCategorySurvivesTheWidening$\|…' && go test ./internal/store/ ./internal/offer/ ./internal/marketplace/` |
| T3 | Let an administrator enter the list each dropdown will offer | pending | — | `bash scripts/smoke.sh` + named assertions |
| T4 | Pick each must-have from a dropdown on the offer editor | pending | — | `bash scripts/smoke.sh` + `bash scripts/browser.sh` + named assertions |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `core.MarketplaceField`, `core.MarketplaceOption`, `core.Offer.Marketplace`, `core.Offer.MarketplaceValue()` | T2, T3, T4 | T1 before all — it deletes `Offer.Categories`, so nothing else builds until it lands |
| T2 | `marketplace_options` + `offer_marketplace_values` schema, `marketplace.Repo`, `OfferStore.SetMarketplaceValue()` | T3, T4 | T2 before T3 |
| T3 | `App.marketplaceOptions(ctx, profile)` | T4 | T3 before T4 |

## Notes

- `internal/web` contains **no Go tests**. T3 and T4 are proved in `scripts/smoke.sh` and
  `scripts/browser/`, which is this repository's established proof for that package — and the reason
  both fences name their assertions individually rather than trusting `SMOKE_FAILURES=0`, which is
  also what a walk prints when nothing was added.
- T2 carries the only data migration. Its Down loses per-offer condition and location values,
  because the table it restores has no column for them. That is recorded in the ADR's Rollback
  section rather than discovered during one.
