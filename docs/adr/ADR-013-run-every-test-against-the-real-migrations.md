# ADR-013: Run every test against the real migrations, never a copied schema

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-001, ADR-012, `internal/store/migrate.go`
**Governs:** `internal/store/*.go`, `migrations/*.sql`
**Enforced-by:** `internal/offer/repo_test.go::TestSearchIndexFollowsUpdatesAndDeletes`
**Invalidates:** none — checked
**Served-path change:** None — this ADR changes only measurement and tooling.

## Context

Retrospective record, written because the failure it prevents **already
happened once** in this repository.

Two packages — `internal/offer` and `internal/submission` — had test helpers
carrying hand-copied `CREATE TABLE` statements, with a comment asking whoever
changed the schema to keep them in step. They drifted at the first opportunity:
adding the full-text index (ADR-012) left the suite **green against a schema
production does not have**. Every test passed. None of them was testing the
database the application runs on.

That is the specific hazard of a copied fixture: it does not fail when it drifts,
it succeeds against the wrong thing. The comment asking for discipline is exactly
as strong as whoever read it last.

## Existing Primitives Audit

- `pressly/goose` — **reused** as the migration runner, both in production and in
  tests. There is one runner and one code path.
- `embed.FS` — **reused**: `migrations` is embedded, so a built binary carries its
  own schema and a test needs no files relative to its working directory.
- The hand-copied `CREATE TABLE` fixtures are **deleted**, not reshaped.

## Decision

Every test that needs a database calls the same migration runner the binary
calls, against the embedded `migrations` FS. No package holds a copy of any
schema statement.

The consequence that makes it work is that a schema change is **impossible to
half-apply**: a migration that adds a table, an index or a trigger reaches the
tests on the next run, because the tests read the same files. A test asserting
behaviour that depends on migration 4 (`TestSearchIndexFollowsUpdatesAndDeletes`)
can only pass if migration 4 actually ran, which is why it is this record's
`Enforced-by`: reintroduce a copied schema without the FTS objects and that test
goes red immediately.

`internal/store/migrate_test.go` additionally asserts the schema's own
constraints — that a sold offer needs a date, that sibling codes are unique but
reusable across parents, that an occupied location cannot be deleted, that photos
cascade with their offer — so the constraints are tested as constraints rather
than only through the Go code that also enforces them.

**What this record does NOT claim.** `go test ./...`, `go vet`, `gofmt -l` and
`scripts/smoke.sh` all exist and all pass, and **nothing runs them automatically**.
There is no CI workflow in this repository. GitHub's default-setup CodeQL runs and
reports success, which is worse than silence here: it puts a green tick against a
commit whose actual checks nobody ran. A check nobody runs is not a check, and
this is the largest open gap in the repository — recorded in
`docs/adr/BACKLOG.md`, not closed.

## Alternatives Considered

- **Hand-copied `CREATE TABLE` in a test helper.** This is what was there.
  Rejected by evidence: it drifted, silently, and the suite stayed green.
- **A generated schema dump checked into the test fixtures.** Rejected: it is a
  copy with a generator in front of it, and it is stale the moment somebody adds
  a migration without regenerating. The failure is still silent.
- **An in-memory database with a schema built by an ORM.** Rejected: this
  application has no ORM, and the schema carries CHECK constraints, triggers and
  a virtual table that no ORM would produce.
- **Test against a shared long-lived database.** Rejected: tests would depend on
  each other's state and could not run in parallel.

## Component / Boundary Impact

`internal/store` owns the migration runner and the embedded FS. Every test
package depends on it and none reimplements it. One reason to change: how this
application's schema is applied.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `migrations.FS` (`embed.FS`) | new; the single source of schema | `migrations` | `internal/store`, every test |
| `store.Migrate(db)` | new; the only application of schema | `internal/store` | `cmd/warehouse`, every test helper |
| Copied `CREATE TABLE` fixtures in `internal/offer` and `internal/submission` | **deleted** | — | — |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`. The fixtures were removed and both packages' test
helpers now run `store.Migrate`. No tasks directory: this is a retrospective
record.

## Consequences

- **Positive:** a schema change reaches the tests automatically, and a test that
  needs a schema object fails if the object is missing.
- **Positive:** constraints and triggers are exercised, which a Go-only fixture
  cannot do.
- **Negative:** every database test pays the cost of running all migrations. On
  this schema that is milliseconds; it will not stay free for ever.
- **Negative:** a migration that is wrong breaks every package's tests at once
  rather than one. That is the correct blast radius, but it is a wide one.
- **Neutral:** `TestMigrateIsIdempotent` runs the whole set twice, so a migration
  that is not re-runnable is caught by the suite rather than by a deployment.

## Out of Scope

- Continuous integration — running these checks on every push (deferred: docs/adr/BACKLOG.md)
- Testing a migration's *down* direction (deferred: docs/adr/BACKLOG.md)
- Testing against a database restored from production (permanent: boundary: there is no production deployment yet; when there is, this becomes a real question)
- A browser-driven check of the pages (deferred: docs/adr/BACKLOG.md)
- Mutation testing across the suite (permanent: boundary: guards are mutation-checked individually where they matter — ADR-008 — rather than by a campaign this repository has no runner for)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Somebody reintroduces a copied schema for speed | Med | High | This record; and a copied schema missing the FTS objects fails `TestSearchIndexFollowsUpdatesAndDeletes` at once |
| **Nobody runs the checks before pushing** | **High** | **High** | **Nothing. There is no CI. This is the open gap, and the CodeQL green tick actively disguises it** |
| Migration time grows until tests are slow | Low | Low | Visible immediately as suite runtime |

## Rollback

None applicable — this is a testing policy. Reverting it means reintroducing the
copied fixtures that produced the drift.

## Follow-ups

- [ ] Add a CI workflow running `gofmt -l`, `go vet ./...`, `go test ./...` and `scripts/smoke.sh` on every push. Recorded in `docs/adr/BACKLOG.md`. **Not built — this is the single largest gap in the repository.**
