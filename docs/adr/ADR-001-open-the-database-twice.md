# ADR-001: Open the database twice, once to write and once to read

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `README.md`, `internal/store/store.go`
**Governs:** `internal/store/*.go`
**Enforced-by:** `internal/store/store_test.go::TestTheWriterDSNCarriesTheImmediateTxlock`
**Invalidates:** none — checked
**Served-path change:** Concurrent requests that read a row and then write it — which is almost every service method — stop failing with `SQLITE_BUSY`; before this two simultaneous users lost writes.

## Context

This record is retrospective. The decision was taken and implemented while the
application was first built, and is written down now because it is the one piece
of the architecture that cannot be inferred from the shape of the code: a reader
seeing two `*sql.DB` fields for one file reads it as redundancy or as a
performance tweak, and would collapse them.

SQLite grants a **deferred** transaction its write lock on the first *write*
statement. A transaction that reads first must therefore UPGRADE its lock, and on
an upgrade conflict SQLite returns `SQLITE_BUSY` **immediately, without consulting
the busy handler** — because two transactions both waiting to upgrade would
deadlock. So `busy_timeout` does nothing at all for the read-then-write shape.

Measured 2026-09-06 on `modernc.org/sqlite` v1.58, 8 goroutines × 40
read-then-write transactions against a temporary database on this machine:

| DSN | transactions failed |
|---|---|
| deferred (the driver default) | 257 / 320 |
| `_txlock=immediate` | 0 / 320 |

## Existing Primitives Audit

- `database/sql` connection pooling — **reused**, and it is what makes the split
  expressible at all: two `sql.DB` values over one file, with different pool
  sizes and different DSNs.
- `modernc.org/sqlite` DSN pragmas (`_txlock`, `_pragma`) — **reused**. No
  wrapper, no custom driver: the behaviour is bought entirely with connection
  string parameters, which is why the test has to assert it rather than trust it.
- There was no prior storage layer to reshape; this is the first.

## Decision

`store.Open` returns a `DB` holding two handles onto the same file.

`Write` carries `_txlock=immediate`, so it takes the write lock at `BEGIN` and
there is no upgrade to conflict over; contention degrades into an ordinary lock
wait, which `busy_timeout` *does* honour. It is capped at one connection, because
SQLite admits one writer regardless and a larger pool would only queue in a place
with worse diagnostics.

`Read` is a pool of 8 and carries `query_only(1)`, so the driver itself refuses a
write on that handle. That turns "a read model must not write" from a
code-review rule into something the database enforces.

Two checks hold this up, and they answer different questions. The distinction was
not obvious when this record was first written, and a mutation found it.

`TestTxlockImmediateIsHonouredByTheDriver` asserts that the DRIVER acts on
`_txlock`. It asserts BOTH arms of the table above — that deferred loses
transactions and that immediate loses none — because a silently ignored DSN knob
is byte-identical to an honoured one from the caller's side, so only a differing
failure count distinguishes them. If the driver ever stops honouring `_txlock`,
the deferred arm stops failing and the test goes red rather than passing
vacuously.

`TestTheWriterDSNCarriesTheImmediateTxlock` asserts that THIS APPLICATION uses
it. That is a separate claim, and until 2026-09-06 nothing made it: the
contention test builds its own DSNs through a helper, so removing `immediate`
from `writerDSN` left the entire `internal/store` package green. The mutation
campaign over this corpus found that, and the test was written to close it.

It asserts the DSN string rather than the behaviour, because the behaviour is
masked here: the writer pool is capped at one connection, so two read-then-write
transactions cannot overlap on it and no upgrade conflict can be provoked through
`Open` at all. The knob is what keeps the property true on the day somebody
widens that pool for throughput — which is exactly the day nothing else would
notice it had gone.

The threshold is valid for `modernc.org/sqlite` in WAL mode; it says nothing
about other drivers or about a network filesystem.

## Alternatives Considered

- **One handle, pool capped at 1.** Also removes the failure. Rejected because it
  serialises every read through the single writer connection and throws away the
  reader/writer concurrency WAL was enabled for — the wrong trade for a
  read-heavy dashboard.
- **Leave it deferred and raise `busy_timeout`.** Rejected on the measurement: the
  busy handler is never consulted for an upgrade conflict, so this changes
  nothing. It is the intuitive fix and it is inert, which is exactly why the
  number is written down here.
- **Retry the transaction on `SQLITE_BUSY`.** Rejected because a read-then-write
  retried from the top re-reads state that may have changed, so the retry has to
  be correct as well as present — real work, in every service method, to recover
  from a condition that can be designed out in a DSN.
- **A different embedded engine.** Rejected: the requirement is one file and no
  cgo, and this is the constraint the whole storage layer was chosen under.

## Component / Boundary Impact

`internal/store` owns both handles and is the only package that constructs them.
Every other package receives `*sql.DB` values it did not open — a repository takes
the write handle, a read model takes the read handle — so the asymmetry is visible
in each constructor's signature. One reason to change: how this application talks
to SQLite.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `store.DB{Write, Read *sql.DB}` | new type; the only way to obtain a handle | `internal/store` | every repository package, `cmd/warehouse` |
| Writer DSN `_txlock=immediate`, one connection | new | `internal/store` | all writes |
| Reader DSN `query_only(1)`, eight connections | new | `internal/store` | all read models |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`. The decision lives in `writerDSN` and `readerDSN`
in `internal/store/store.go`, and in the pool sizing in `Open`. No tasks
directory: this is a retrospective record, so there is no work to schedule and
nothing here may be marked `done` — the evidence that it holds is the
`Enforced-by:` test, not a task log.

## Consequences

- **Positive:** the read-then-write shape is safe by construction, and the read
  path cannot write even by mistake.
- **Positive:** the failure mode is now a bounded wait rather than an immediate
  error, so load shows up as latency instead of as lost writes.
- **Negative:** two handles must be threaded through wiring, and a constructor
  that takes the wrong one compiles fine and fails at runtime — mitigated by the
  reader refusing writes loudly rather than silently succeeding.
- **Negative:** writes are serialised application-wide. Acceptable at this size;
  it is the same serialisation SQLite would impose anyway.
- **Neutral:** `busy_timeout` is now generous, precisely because the only waits
  that should reach it are short writer queues.

## Out of Scope

- Multi-process access to the same database file (permanent: boundary: one binary owns its database file, and a second writer process would need a lock protocol this design does not have)
- Any non-SQLite backend (permanent: boundary: one file and no cgo is the constraint this application is built under)
- Read replicas or a connection proxy (permanent: boundary: pointless below the scale where one machine stops being enough)
- Automatic retry of a busied transaction (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A future driver release stops honouring `_txlock` | Low | High | The test asserts both arms; the deferred arm stops failing and the suite goes red |
| Someone widens the writer pool for throughput | Med | High | The cap carries a comment saying why; `sequence.NextSequence` documents that it would become a lost update |
| A repository is handed the read handle by mistake | Med | Low | `query_only(1)` makes it fail immediately and unmistakably at the first write |
| Long-running writes block all others | Low | Med | `busy_timeout` bounds the wait; no transaction in this application does I/O while holding the write lock |

## Rollback

Collapse `DB` to a single `*sql.DB` opened with `writerDSN` and one connection,
and delete the reader. No schema or file-format change is involved — both handles
address the same file with the same journal mode — so rollback is a code change
with no migration and no data movement. Reads become serialised; nothing becomes
incorrect.

## Follow-ups

None — nothing was left open when this record was written.
