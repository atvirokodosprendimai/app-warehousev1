# ADR-015 Tasks

Implementation tasks for ADR-015: Make the photo upload say that it is uploading.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The upload shows a busy state, and the indicator is proved wired | done | — | `go test ./internal/web/view/` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

None.

## Notes

- `internal/web/view/*_templ.go` is generated from `*.templ` and is committed, so
  the acceptance fence regenerates them first. ⚠ It uses
  `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`, NOT
  `go tool templ generate`: there is no `tool` directive in `go.mod` and no
  `templ` on PATH, so the `go tool` form fails with `go: no such tool "templ"`.
  The first `adr-verify` run of this task exited 2 for exactly that reason, and
  the entry is still in the Verification Log below because failures stay.
  A mutation campaign against a `.templ` must also restore the generated file —
  `adr-verify --also-restore internal/web/view/offer_detail_templ.go` — because
  the tool restores the file it mutated, not what that file produces.
- Nothing here is browser-verified. The tests assert rendered markup, which is
  the layer below the one that actually has to work; see ADR-008 and the browser
  entry in `docs/adr/BACKLOG.md`.
