# ADR-010: Allocate human references from a database counter, and never reuse one

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-001, ADR-011, `internal/sequence/repo.go`
**Governs:** `internal/sequence/*.go`, `internal/core/sku.go`
**Enforced-by:** `internal/sequence/repo_test.go::TestConcurrentAllocationNeverRepeats`
**Invalidates:** none — checked
**Served-path change:** A new item gets a reference like `WH0000042`, short enough to write on a label by hand; and typing `42`, `WH42` or `wh0000042` into the search box finds it.

## Context

Retrospective record, prompted by a direct complaint about the first design:
*"this format is hard to write down by hand and find — so maybe auto-increment
`WH0000001` instead of `WH-20260906-C33A63AE`?"*

That is a usability requirement with a correctness consequence. A short reference
must be **unique** and, because it gets written on a physical box, it must never
be **reused**. Those two together rule out the obvious implementation.

`MAX(sku)+1` over the offers table is wrong twice over:

1. The column is a string, so "highest" orders lexically — `WH0000009` beats
   `WH0000010`.
2. A deleted row hands its number back to the next arrival. Somewhere there is a
   box with that number written on it, and now two things claim it.

## Existing Primitives Audit

- SQLite's `UPDATE … RETURNING` — **reused**. It increments and reads in one
  atomic statement, which is the whole mechanism.
- `internal/store`'s writer handle (ADR-001) — **reused**. `sequence.Repo` takes
  *only* the write handle, because every operation here is a mutation and the
  reader carries `query_only(1)` so it could not serve one anyway.
- The previous `WH-<date>-<random>` format is **replaced**, not reshaped: it was
  unique and unusable by hand.

## Decision

A `counters` table holds named sequences. `NextSequence(name)` is exactly one
statement:

    UPDATE counters SET value = value + 1 WHERE name = ? RETURNING value

One statement, deliberately. Two callers arriving together are serialised by the
database and receive different numbers. The obvious alternative — `SELECT`, add
one, `UPDATE` — is a read-then-write, which is the shape ADR-001's writer DSN
exists to make safe, and which would still be a lost update if anything ever
widened the writer pool.

Numbers are drawn from the counter, never from the offers table, so **deleting an
offer does not return its number**. The counter only goes up.

An unknown counter name is an **error**, not a lazily created row: a typo'd name
would otherwise silently start its own sequence at 1 and mint duplicate
references alongside the real one.

`FormatSKU` renders as `WH` plus seven zero-padded digits — nine characters, which
fits a small label, survives being read aloud, and groups when scanned down a
column. Seven is a **minimum, not a cap**: the millionth item gets a longer
reference rather than a wrapped one, because silently reusing a number already
written on a box is far worse than an uneven column.

`ParseSKU` accepts `1`, `WH1` and `wh0000001` for `WH0000001`. Somebody reading a
number off a box will not reproduce the padding, and a search that only matched
the exact stored string would make the short reference no easier to use than the
long one it replaced.

`FormatSKU` lives in `core` rather than in `offer` because the submission
aggregate (ADR-011) also mints references when it converts a proposal — and two
packages formatting the same identifier independently is exactly how two formats
end up in one warehouse.

What would FAIL this decision: `TestConcurrentAllocationNeverRepeats` returning a
duplicate, or `TestNumbersAreNotReused` handing a deleted offer's number back.
Both run on every `go test ./...`; the concurrency test drives 200 simultaneous
allocations under `-race` and asserts 200 distinct gapless numbers.

## Alternatives Considered

- **`MAX(sku)+1` on the offers table.** Rejected on both counts above: lexical
  ordering, and reuse after deletion.
- **`AUTOINCREMENT` on the offers table's rowid.** Closer — SQLite's
  `AUTOINCREMENT` genuinely does not reuse — but rejected because it ties the
  human reference to a physical row id, so the reference could not be minted
  before the row exists. Submission conversion needs exactly that.
- **A UUID or a random token.** That is what was there, and it is what the
  operator rejected: unguessable, unique, and impossible to write on a box or
  read down a column.
- **Date-prefixed sequences (`WH-20260906-0001`).** Rejected: longer, and the
  reset boundary invites the reuse question back in a subtler form.
- **Allocate in the application with a mutex.** Rejected: it is correct only
  while there is exactly one process, and nothing in the design says there always
  will be.

## Component / Boundary Impact

`internal/sequence` owns allocation and is the only writer of `counters`.
`internal/core` owns the format, so offers and submissions cannot disagree about
it. `core.Sequencer` is the port, satisfied by a compile-time assertion. One
reason to change: how this business names a thing on a shelf.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `counters` table, seeded with `offer_sku` | new | `migrations/00006_counters.sql` | `internal/sequence` |
| `core.Sequencer` port | new | `internal/core` | `internal/offer`, `internal/submission` |
| `core.FormatSKU` / `ParseSKU` / `NormalizeSKUQuery` | new | `internal/core` | offer, submission, search |
| Search accepts a bare number as a reference | changed | `internal/offer` | `internal/web` search box |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/sequence/repo.go`,
`internal/core/sku.go` and `migrations/00006_counters.sql`. No tasks directory:
this is a retrospective record.

## Consequences

- **Positive:** references are short enough to write by hand and to find by
  typing part of one.
- **Positive:** allocation is atomic without any application-level locking, and
  stays correct if the process is ever run more than once.
- **Positive:** a number written on a box is never claimed by a second item.
- **Negative:** the counter is global, so references are not grouped by anything —
  no per-site or per-year prefix. That was the trade for shortness.
- **Negative:** an allocated number is consumed even if the offer is never
  created, so the sequence has gaps. Gaps are harmless; reuse is not.
- **Neutral:** a search for a bare number is interpreted as a reference *and*
  searched as text, so a title containing "42" still matches.

## Out of Scope

- Per-site or per-year reference prefixes (permanent: boundary: any grouping makes the reference longer, which is the problem this record was opened to fix)
- Barcode or QR generation (deferred: docs/adr/BACKLOG.md)
- Reclaiming numbers from deleted offers (permanent: boundary: this is the defect the record exists to prevent, not a feature)
- A check digit (permanent: boundary: seven digits read aloud are already short enough to repeat back; a check digit makes the label longer to catch an error the operator would catch anyway)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The writer pool is widened and allocation becomes a lost update | Low | High | Not possible with `UPDATE … RETURNING`; the comment on `NextSequence` says why the read-then-write form would be |
| A typo'd counter name starts a shadow sequence | Low | High | `ErrUnknownSequence`; the row must be seeded by a migration |
| The counter is reset or restored from an old backup | Low | High | Nothing prevents this; it is a restore procedure concern, and duplicate references would result |
| Seven digits are exhausted | Very low | Low | The reference simply grows; nothing wraps |

## Rollback

The format could change for *new* references without touching stored ones, since
`ParseSKU` reads what is there. Reverting to the random format would leave both
formats in one warehouse, which is the thing `FormatSKU`'s placement in `core`
exists to prevent — so in practice this is not reversible once labels are
printed.

## Follow-ups

None — nothing was left open when this record was written.
