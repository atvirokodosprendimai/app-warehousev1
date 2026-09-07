# Decision records

Fifteen decisions that shaped this application, written down because none of
them can be recovered from the shape of the code. Each record says what was
decided, what was rejected and why, what it costs, and — in its `Enforced-by:`
header — the check that goes red when the decision is violated.

**ADR-001 to ADR-014 are retrospective.** The application was built first and
those were written afterwards, on 2026-09-06, so none of them has a `tasks/`
directory and none claims a task is `done`. What holds each up is the named test,
not a task log.

**ADR-015 is forward work** and therefore has the full apparatus: a `tasks/`
directory, a task that was red before it was green, and a Verification Log and
Mutation Log the tool wrote rather than the author.

## The corpus

| # | Decision | What breaks if you undo it |
|---|---|---|
| [001](ADR-001-open-the-database-twice.md) | Open the database twice, once to write and once to read | Concurrent read-then-write transactions start losing writes to `SQLITE_BUSY` |
| [002](ADR-002-money-is-integer-minor-units.md) | Store money as integer minor units carrying its own currency | Rounding error becomes reachable, silently, in figures people are paid |
| [003](ADR-003-the-owner-price-never-leaves-the-building.md) | Keep three prices, and never export the owner price | What a holder is privately owed becomes readable on a public marketplace feed |
| [004](ADR-004-an-unpriced-draft-is-a-normal-state.md) | Treat an unpriced draft as a normal state | Operators type placeholder prices, and a placeholder can reach a marketplace |
| [005](ADR-005-storage-is-one-recursive-tree.md) | Make storage one recursive tree whose levels are all optional | The schema has to change every time the warehouse grows or loses a level |
| [006](ADR-006-registration-closes-after-the-first-account.md) | Close registration permanently after the first account | Either a shipped credential or a seeding step somebody can forget |
| [007](ADR-007-a-photo-uuid-is-its-public-capability.md) | Treat a photograph's UUID as its public capability | Marketplaces cannot fetch images, and produce listings with no pictures and no error |
| [008](ADR-008-no-forms-except-the-photo-upload.md) | Use no `<form>` elements except the photo upload | Photo upload silently stops working — this one already happened |
| [009](ADR-009-one-stream-per-page-and-the-payload-is-an-id.md) | One SSE stream per page, and only an id on the wire | Streams die at the server's write timeout, or stale patches overwrite current state |
| [010](ADR-010-references-are-allocated-by-the-database.md) | Allocate human references from a database counter, never reused | A number written on a box gets handed to a second item |
| [011](ADR-011-a-submission-is-not-an-offer.md) | Keep a submission separate from an offer until converted | Half-built rows sit in the table exports read |
| [012](ADR-012-search-is-an-index-the-database-maintains.md) | Make search an external-content FTS5 index kept by triggers | Search goes quietly stale rather than failing |
| [013](ADR-013-run-every-test-against-the-real-migrations.md) | Run every test against the real migrations | The suite passes against a schema production does not have — this one already happened |
| [014](ADR-014-deployment-values-that-shape-an-artefact-live-in-the-database.md) | Keep deployment values that shape an artefact in the database | The public domain can only be fixed by whoever can restart the process |
| [015](ADR-015-an-upload-says-it-is-uploading.md) | Make the photo upload say that it is uploading | A slow upload on a phone is indistinguishable from a dead control |

[BACKLOG.md](BACKLOG.md) holds every deferral these records made, each naming the
record that punted it.

## Two records worth reading first

**ADR-008** and **ADR-013** are the only two describing a failure that has
already happened here, and they fail the same way: a check that was green while
the thing it named was broken.

- ADR-008 — photo upload was exercised by a smoke test that built its own
  multipart body with `curl`. That proves the *server* accepts an upload and says
  nothing about whether the *page* can produce one. **A test that constructs the
  request cannot test the code whose job is constructing it.**
- ADR-013 — two packages carried hand-copied `CREATE TABLE` fixtures. They
  drifted, and the suite stayed green against a schema that does not exist in
  production.

## Are these checks real?

An `Enforced-by:` header is a claim, and a check that cannot fail is decoration.
So each one was verified by **breaking the mechanism it names and confirming the
test goes red** — 2026-09-06 for the retrospective corpus, thirteen mutants, one
per record:

    KILLED=13  SURVIVED=0  INCONCLUSIVE=0

ADR-015 added three more on 2026-09-07, each bound by `adr-verify --covers` to
one mechanism it declares in `Rests-on:` — the indicator itself, the
lowercase-survival rule, and the has-a-consumer rule. Its fourth declared
mechanism carries no mutant on purpose, and the task says why rather than faking
one: the fence performs that step itself, so it cannot fail for that reason
inside its own run.

ADR-009 carries no mutant because its `Enforced-by:` is honestly `None`.

**The campaign paid for itself immediately: two of the first thirteen mutants
survived, and both were defects in these records.**

- **ADR-001** named `TestTxlockImmediateIsHonouredByTheDriver`. That test builds
  its own DSNs, so it proves the *driver* acts on `_txlock` and says nothing about
  whether `writerDSN` uses it. Removing `immediate` from the application's own
  writer left the entire `internal/store` package green.
  `TestTheWriterDSNCarriesTheImmediateTxlock` was written to close that, and the
  record now names it.
- **ADR-006** named `TestRegisterMintsAnAdminAndThenClosesTheBootstrap`. Breaking
  `BootstrapOpen` so it never closes leaves that test green, because `Register`
  refuses a second account through its own conditional insert. The real
  consequence is a registration page that never disappears, and
  `TestBootstrapOpenIsTrueOnlyWhileNoAccountExists` is what goes red for it.

Neither was visible from reading the records. Both took one mutation each.

## What is not covered

Four gaps, all in BACKLOG.md, and the first is the largest thing wrong with this
repository:

1. **Nothing looks at what the screens LOOK like.** The browser walks assert
   behaviour; ADR-021 shipped two screens with eleven class names that had no
   stylesheet rules and every check passed, because none of them reads a class
   name. CI keeps the screenshots now, which makes looking possible without
   making anybody look.

Closed on 2026-09-07, and listed so they are not rediscovered:

2. ~~There is no CI.~~ `.github/workflows/checks.yml` runs `gofmt`, `go vet`,
   `go test` and `scripts/smoke.sh` on every push. Before it, GitHub's
   default-setup CodeQL reported green against commits whose real checks nobody
   had run.
3. ~~Four packages still build their own test schema.~~ `internal/fx`,
   `internal/auth`, `internal/location` and `internal/cart` run the real
   migrations; `internal/store/schema_guard_test.go` enforces it tree-wide, which
   ADR-013's own Enforced-by test could not. The drift it predicted had already
   happened in `internal/location`.
4. ~~Down migrations have never been run.~~
   `TestEveryMigrationCanBeRolledBack` runs the set down to zero, checks nothing
   survived, and puts it back up again.

## Checking these records

    adr-lint  docs/adr/ADR-00N-*.md     # blocking: structure, dispositions, resolvable pointers
    adr-judge docs/adr/ADR-00N-*.md     # advisory: is there evidence, and will a reader understand it
    adr-debt  docs/adr                  # sweeps deferrals so they resurface

Quote a verdict with the version that produced it — these tools' output is
version-dependent. The corpus was written and checked against
`quality-harness 2.79.0`.
