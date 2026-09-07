# ADR-019 Tasks

Implementation tasks for ADR-019: Treat an undescribed photograph group as a
normal state, so two people can share one intake. See the parent ADR for the
decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A draft needs no title, and publication still refuses one without | done | — | `go test ./internal/core/ ./internal/offer/ ./internal/cart/ ./internal/store/` |
| T2 | The undescribed groups are a queue somebody can find | pending | — | `go test ./internal/web/view/ && scripts/smoke.sh` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

T2 consumes `core.Offer.NeedsDescribing()` and `core.OfferFilter.NeedsDescribing`,
both produced by T1. Nothing else crosses.

T1 is independently shippable: it relaxes the rule, adds the derived predicate and
the repository clause, and leaves them with no caller. A half-landed T1 shows an
operator nothing new rather than showing them something broken.

## Notes

- `internal/web/view/*_templ.go` is generated from `*.templ` and is committed, so
  the acceptance fence regenerates them first. ⚠ It uses
  `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`, NOT
  `go tool templ generate`: there is no `tool` directive in `go.mod` and no
  `templ` on PATH, so the `go tool` form fails with `go: no such tool "templ"`.
- ⚠ The database half of T1 is a TRIGGER pair, not a `CHECK`. SQLite cannot add a
  `CHECK` to an existing table, and the alternative — rebuilding `offers` and
  reversing that rebuild in a down migration — is not worth it on a project whose
  down migrations have never been run. See the parent ADR's Decision.
- ⚠ ADR-004 line 47 said "`Offer.Validate()` requires a title and a SKU". T1
  makes the title half false, so T1 also corrects that line in place and points it
  here. A record left asserting something this corpus has just made untrue is the
  failure ADR-013 already had once.
