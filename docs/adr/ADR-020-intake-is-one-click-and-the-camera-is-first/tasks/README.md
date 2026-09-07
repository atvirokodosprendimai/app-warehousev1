# ADR-020 Tasks

Implementation tasks for ADR-020: make intake one click, and order the offer
editor camera-first and status-last. See the parent ADR for the decision.

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
| T1 | Intake is one click, and the editor reads camera-first and status-last | done | — | `go test ./internal/web/... && scripts/smoke.sh` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

None — one task. ADR-020's Inter-task Contracts section says why it is not split:
a one-click create that still lands on Details puts the operator back in the flow
the record exists to remove, so a half-landed change is worse than none.

## Notes

- `internal/web/view/*_templ.go` is generated from `*.templ` and is committed, so
  the acceptance fence regenerates them first. ⚠ It uses
  `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`, NOT
  `go tool templ generate`: there is no `tool` directive in `go.mod` and no
  `templ` on PATH, so the `go tool` form fails with `go: no such tool "templ"`.
- ⚠ **Two existing tests assert the screen this task DELETES.**
  `queue_contract_test.go::TestIntakeDoesNotDemandATitle` renders `IntakeScreen`,
  and `upload_contract_test.go` includes it in two screen maps. They are not
  wrong — they pinned a real ADR-019 property — so they are retired in place with
  a reason and a successor named, not quietly dropped. The successor property is
  stronger: a screen that does not exist cannot demand anything.
- ⚠ `scripts/smoke.sh` creates offers by POSTing `newTitle`. After this task
  `POST /offers` ignores its body, so the smoke walk must create the offer and
  then NAME it through `POST /offers/{id}` — which is the flow M asked for, so
  the script ends up describing the product rather than working around it.
