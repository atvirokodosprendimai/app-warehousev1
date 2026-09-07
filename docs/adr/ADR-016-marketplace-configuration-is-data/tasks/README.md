# ADR-016 Tasks

Implementation tasks for ADR-016: Make marketplace configuration data, at the
granularity the marketplace uses. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T1, T2 |

T1 and T2 are independent — one adds a stored default, the other a per-offer
override — and T3 is the resolution that consumes both. T1 alone already fixes
the reported error; T2 alone is inert until T3 reads it.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Marketplace defaults are settings, editable without a restart | done | — | `go test ./internal/settings/... ./internal/core/...` |
| T2 | An offer carries a per-profile category | done | — | `go test ./internal/offer/...` |
| T3 | The exporter resolves per offer and refuses by naming where to set it | done | — | `go test ./internal/export/...` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `core.Settings` marketplace fields | T3 | T1 before T3 |
| T2 | `core.Offer.Categories` | T3 | T2 before T3 |

## Notes

- `internal/export` must stay a pure function of what it is handed: it reads no
  settings and no database. T3 changes what the caller resolves and passes, not
  where the exporter looks.
- The three environment variables (`EBAY_CATEGORY`, `EBAY_CONDITION_ID`,
  `EBAY_LOCATION`) are undocumented in `README.md` today. T1 documents them as
  start-up defaults in the same change that demotes them.
