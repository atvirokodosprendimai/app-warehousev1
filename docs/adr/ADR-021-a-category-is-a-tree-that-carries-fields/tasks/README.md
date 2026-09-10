# ADR-021 Tasks

Implementation tasks for ADR-021: make a category a tree that carries its own
fields, inherited downward, and let an operator choose which of those fields
leave the building in a CSV. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 2 | T3 | T1 |
| 3 | T4 | T1, T3 |
| 4 | T5 | T1, T2 |

T2 and T3 are independent of each other and may land in either order: T2 is how
a tree comes to exist, T3 is how an offer answers it. Neither is useful without
T1 and each is useful without the other — an operator can shape a taxonomy
before any offer uses it, and an offer filed under a seeded category renders its
fields whether or not the admin screen exists yet.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The tree, its fields, and the values that hang off an offer | done | — | `go test ./internal/taxonomy/ ./internal/core/ && go test ./...` |
| T2 | An operator can shape the tree and the questions it asks | done | — | `go test ./internal/web/... && scripts/smoke.sh` |
| T3 | An offer is asked its category's questions, root first | done | — | `go test ./internal/web/... && scripts/smoke.sh` |
| T4 | A field can be exported, and the CSV's columns are the union over the batch | done | — | `go test ./internal/export/... && scripts/smoke.sh` |
| T5 | A trade's whole question set arrives in one press | done | — | `go test ./internal/taxonomy/ ./internal/web/view/ && scripts/smoke.sh` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

T1 produces every port the other three consume, and produces no screen: a tree
nobody can edit and no offer references is inert, which is the correct
half-landed state and is why T1 is independently shippable.

⚠ **T4 consumes a shape T3 establishes, not merely a port.** `internal/export`
is a PURE package — it is handed `[]core.Offer` and an `io.Writer` and touches
no database — so a custom field can only reach a CSV by riding on the offer the
caller already loaded. That is the seam ADR-016 opened for
`Offer.Categories map[string]string`, and T1 widens it in the same shape rather
than passing a second slice alongside the offers.

## Notes

- `internal/web/view/*_templ.go` is generated from `*.templ` and is committed, so
  T2's and T3's acceptance fences regenerate them first. ⚠ It uses
  `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`, NOT
  `go tool templ generate`: there is no `tool` directive in `go.mod` and no
  `templ` on PATH, so the `go tool` form fails with `go: no such tool "templ"`.
- ⚠ **NEVER hand-edit a `*_templ.go`.** It is generated and committed; a mutant
  or a fix planted there is erased by the next `templ generate` and, worse,
  passes in the meantime. Every change goes through the `.templ` source.
- ⚠ **The materialised-path traps are already paid for once** in ADR-005 and are
  not to be rediscovered: a subtree rewrite uses `substr(path,1,n)` rather than a
  concatenated `LIKE` (where `_` is a wildcard and silently matches a sibling
  subtree); `substr`/`length` count CHARACTERS while Go's `len` counts bytes, so
  every offset comes from `utf8.RuneCountInString`; and `UNIQUE(parent_id, code)`
  is INERT at the root because SQL treats every NULL as distinct, so
  `UNIQUE(path)` is what catches a duplicate root.
- ⚠ **`Offer.CategoryID` is not `Offer.Categories`.** The first is what the item
  IS (this record); the second is where to list it on each marketplace
  (ADR-016). A task that touches one and means the other is the likeliest bug in
  this set, which is why the parent ADR carries it as a named risk and
  `BACKLOG.md` carries the rename.
- Tests run the real migrations (ADR-013). No task here may add a hand-copied
  `CREATE TABLE` fixture.
