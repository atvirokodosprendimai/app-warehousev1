# Task ADR-022-T2: Store the option lists, and widen the per-offer table to hold three kinds of value

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — a migration over another record's table)
**Owner:** unassigned
**Produces:** `marketplace_options` table, `offer_marketplace_values` table, `marketplace.Repo`, `OfferStore.SetMarketplaceValue()`
**Consumes:** `core.MarketplaceField`, `core.MarketplaceOption`, `core.Offer.Marketplace` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the row-carrying migration`, `the reversible Down`, `the uniqueness constraint`

## Goal

Add `marketplace_options`, reshape `offer_categories` into `offer_marketplace_values` carrying its
existing rows forward, and give both a repository.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `migrations/00010_marketplace_options.sql` | add | Both tables, and the data migration |
| `internal/marketplace/repo.go` | add | Reads and writes the option lists |
| `internal/marketplace/repo_test.go` | add | Against the real migrations (ADR-013) |
| `internal/offer/repo.go` | edit | Reads/writes the widened per-offer table |
| `internal/offer/service.go` | edit | `SetCategory` → `SetMarketplaceValue`, validating the field |
| `internal/core/ports.go` | edit | The `OfferStore` interface — **this is what selects the widened method**; leaving the old signature here compiles a service nothing can call with a condition |
| `internal/web/app.go` | edit | Constructs the new repo — the composition root, without which the repo is unreachable |
| `internal/offer/repo_test.go` | edit | Fixtures |

## Ordered Steps

1. [S1] Write `TestAnExistingOfferCategorySurvivesTheWidening` in `internal/marketplace/repo_test.go` and confirm it is RED — the migration does not exist. ⚠ It must insert into `offer_categories` at schema version 9, migrate up, and assert the row is readable as `field='category'`. A test that starts from the NEW schema cannot see a migration that drops data. [proof: acceptance]
2. [S2] Write `migrations/00010_marketplace_options.sql`: create `marketplace_options`, create `offer_marketplace_values`, `INSERT INTO … SELECT offer_id, profile, 'category', category FROM offer_categories`, then drop `offer_categories`. Order matters — copy before drop, in one statement block.
3. [S3] Write the Down: recreate `offer_categories`, copy `field='category'` rows back, drop both new tables. ⚠ Condition and location values are LOST going down, because the old table has no column for them. That is a real consequence of the rollback, recorded in the ADR, not a defect to paper over. [proof: acceptance]
4. [S4] Add `UNIQUE (profile, field, value)` to `marketplace_options`. Two rows offering `20081` twice is a dropdown with a duplicate entry the operator cannot tell apart, and it is the shape a double-submit produces.
5. [S5] Add `marketplace.Repo` with `Options(ctx, profile)`, `AddOption`, `RemoveOption`, `Reorder`. Reads return in `position` order, then `label`, so the list is stable across two identical positions.
6. [S6] Widen `OfferStore.SetCategory` to `SetMarketplaceValue(ctx, id, profile, field, value)` in `internal/core/ports.go` and both implementations; an empty value DELETES the row rather than storing a blank, so "unset" has one representation. [proof: acceptance]
7. [S7] Construct the repo in `internal/web/app.go`. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/marketplace/ -count=1 \
  -run '^TestAnExistingOfferCategorySurvivesTheWidening$|^TestAnOptionListRefusesADuplicateValue$|^TestAnEmptyValueUnsetsRatherThanStoringABlank$' \
  2>&1 | tee /tmp/adr022-t2.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr022-t2.out \
  && go test ./internal/store/ ./internal/offer/ ./internal/marketplace/ -count=1 2>&1 | tee -a /tmp/adr022-t2.out \
  && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr022-t2.out
```

`./internal/store/` is in the regression half deliberately: it holds
`TestEveryMigrationCanBeRolledBack`, which executes the Down written in S3. Without it the rollback
is asserted by nobody.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnExistingOfferCategorySurvivesTheWidening` | `internal/marketplace/repo_test.go` | A row written at schema 9 is readable as `field='category'` at schema 10 | — | S1, S2 |
| `TestAnOptionListRefusesADuplicateValue` | `internal/marketplace/repo_test.go` | The uniqueness constraint refuses a second `20081` for the same profile and field | — | S4 |
| `TestAnEmptyValueUnsetsRatherThanStoringABlank` | `internal/marketplace/repo_test.go` | Setting empty removes the row; reading back yields no value | — | S6 |
| `TestOptionsComeBackInPositionOrder` | `internal/marketplace/repo_test.go` | Ordering is stable and total | — | S5 |
| `TestEveryMigrationCanBeRolledBack` | `internal/store/migrate_test.go` | 00010's Down executes and leaves no application table behind | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The `internal/marketplace` tests |
| 2 — something selects it | `internal/web/app.go` constructs the repo; `core.OfferStore` names the widened method, so a service calling it fails to build if the interface is not widened |
| 3 — the caller can discover it | The migration is in `migrations/embed.go`'s FS, so `store.Migrate` finds it without a registration list |
| 4 — it is used | Nothing measures this yet — T3 is the first writer of an option row |

## Mutation Log

## Invariants

- Migrations are append-only; 00010 never edits 00007.
- Every test runs the real migrations — no package copies a `CREATE TABLE` (ADR-013).
- A write inside an open transaction uses that transaction's handle; the writer pool is one connection and a stray non-transactional write deadlocks.

## Risks

- The data migration is the one irreversible-ish step. Mitigated by S1 asserting the copy from the OLD schema, which is the only way to see a drop.
- SQLite cannot rename a table's columns in place; the create-copy-drop shape is required and is what the existing migrations already do.

## Stop Condition

Stop if any deployment is found to hold `offer_categories` rows for a profile that is not in
`core.KnownExportProfiles()` — the copy would carry a row nothing can ever read, and the right
answer is a decision about those rows rather than a silent migration.

## Out of Scope

- Any screen — T3 and T4.
- Seeding a starter list of eBay categories (deferred: docs/adr/BACKLOG.md)

## Verification Log
