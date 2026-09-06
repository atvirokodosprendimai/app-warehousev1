# ADR-006: Close registration permanently after the first account

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-001, `internal/auth/service.go`
**Governs:** `internal/auth/*.go`
**Enforced-by:** `internal/auth/service_test.go::TestBootstrapOpenIsTrueOnlyWhileNoAccountExists`
**Invalidates:** none — checked
**Served-path change:** The first person to reach a fresh deployment becomes the administrator; from that moment the registration page is gone for everybody, and every later account is created from inside the dashboard by an administrator.

## Context

Retrospective record, and the trust boundary of the application. The mechanism is
in `internal/auth/service.go` (`BootstrapOpen`, `Register`) and
`internal/auth/repo.go` (`CreateFirstAdmin`), over the `users` table created in
`migrations/00001_init.sql`.

The requirement, given on 2026-09-06, was in the operator's words *"auth without
public registration; if db has 0 users, first registered user becomes admin"*.
That is a deliberate and slightly unusual shape: the system is briefly open,
exactly once, and then closed for ever.

What it avoids is concrete rather than comparative, and neither claim below rests
on a measurement — both are properties of the mechanism, stated so a reader can
check them against the code rather than take them on authority:

- A shipped default credential is published the moment the source is. Anybody who
  can read the repository has it, on every deployment where nobody changed it.
- A seeding command is a step that can be omitted, and a deployment with no
  administrator is indistinguishable from one whose administrator has simply not
  signed in yet.

The window is real, and it is worth being precise about it: between the first
start and the first registration, anybody who can reach the port becomes the
administrator. That is a deployment concern, not something the application can
close, and `README.md` states it rather than hiding it.

## Existing Primitives Audit

- `golang.org/x/crypto/bcrypt` — **reused** for password hashing. No custom
  scheme, no pepper, no home-grown KDF.
- `alexedwards/scs` — **reused** for sessions, with the SQLite store. No
  hand-rolled cookie signing.
- `internal/store`'s writer handle (ADR-001) — **reused**, and it is what makes
  the bootstrap atomic: `CreateFirstAdmin` runs as a single conditional insert on
  a handle that takes its write lock at `BEGIN`.

## Decision

`BootstrapOpen()` is true only while `CountUsers() == 0`. `Register` is the only
path that mints an administrator, it works only while the bootstrap is open, and
the row it writes is inserted **conditionally on the table still being empty** —
`CreateFirstAdmin` is atomic, so two people racing to be first produce one
administrator and one refusal, not two administrators.

Every later account is created by an active administrator through `CreateUser`.
There is no self-service path, no invitation link and no password reset by email.

Two further rules fall out of "closed" and are enforced with it:

1. **Authentication gives one indistinguishable answer to every failure.** An
   unknown address, a wrong password and a disabled account produce the same
   error and the same work — a real bcrypt comparison against a dummy hash runs
   even when no user was found, so timing does not disclose which addresses
   exist.
2. **The last active administrator cannot be disabled.** Otherwise the system
   locks itself out permanently, and the only recovery is editing the database by
   hand.

What would FAIL this decision: `TestBootstrapOpenIsTrueOnlyWhileNoAccountExists`
reporting the bootstrap still open after the first account,
`TestCreateFirstAdminIsAtomicUnderConcurrentCallers` producing two
administrators, or `TestSetDisabledRefusesToRemoveTheLastActiveAdministrator`
allowing the lockout. All run on every `go test ./...`.

⚠ This record first named `TestRegisterMintsAnAdminAndThenClosesTheBootstrap`
here, and a mutation showed it does not bind: breaking `BootstrapOpen` so it
never closes leaves that test green, because `Register` refuses a second account
through its own conditional insert rather than by consulting `BootstrapOpen`.
The consequence of the broken version is not a second administrator — it is a
**registration page that never disappears**, offering a form that would always be
refused. `TestBootstrapOpenIsTrueOnlyWhileNoAccountExists` is the check that goes
red for that, and it is the one named above.

## Alternatives Considered

- **Ship a default administrator credential.** Rejected: a published password on
  a deployment somebody forgot to change is the single most common way a small
  application is compromised.
- **A seeding CLI command.** Rejected: it is a step that can be skipped, and a
  deployment with no administrator is indistinguishable from one whose
  administrator has not signed in yet.
- **An environment-variable bootstrap token.** Rejected as more moving parts for
  the same result: the token has to be transported, and it lands in a shell
  history or a process list.
- **Leave registration open with an approval queue.** Rejected: the requirement
  was explicitly no public registration, and an approval queue is a public
  registration with a delay.

## Component / Boundary Impact

`internal/auth` owns accounts, hashing, sessions and the bootstrap. It is the
only package that decides who anybody is; every other package receives an
already-identified user. `internal/web` owns the middleware that refuses an
unauthenticated request. One reason to change: who may use this system.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `auth.Service.BootstrapOpen()` | new; gates the registration route | `internal/auth` | `internal/web` router |
| `CreateFirstAdmin` conditional insert | new; atomic under concurrency | `internal/auth` | `Register` |
| `users` table with `role`, `disabled` | new | `migrations/00001_init.sql` | `internal/auth` |
| `sessions` table (scs store) | new | `migrations/00001_init.sql` | `internal/web` |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/auth/`. `scripts/smoke.sh` drives the
whole boundary over HTTP against a real binary — bootstrap, the permanently closed
registration, and an unauthenticated request being refused. No tasks directory:
this is a retrospective record.

## Consequences

- **Positive:** no shipped credential and no seeding step, so a fresh deployment
  has exactly one way to acquire an administrator and it is self-evident.
- **Positive:** the closed state is derived from the data (`CountUsers() == 0`),
  not from a flag somebody could flip back.
- **Negative:** the first-start window is genuinely open. On a public network, a
  deployment left unregistered is a deployment somebody else can claim.
- **Negative:** no password reset. An administrator who forgets their password
  needs another administrator, or the database.
- **Neutral:** the uniform authentication failure makes support harder — nobody
  can be told *why* their sign-in failed. That is the intended trade.

## Out of Scope

- Password reset by email (deferred: docs/adr/BACKLOG.md)
- Two-factor authentication (deferred: docs/adr/BACKLOG.md)
- OAuth or any external identity provider (permanent: boundary: this is a handful of accounts inside one business; a dependency on an external issuer would add a failure mode bigger than the problem)
- Closing the first-start window from inside the application (permanent: boundary: the application cannot know whether the port is reachable; binding to localhost until an administrator exists is a deployment decision, and the README says so)
- Rate limiting sign-in attempts (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A deployment is left unregistered on a public address | Med | High | Named in the README as the one thing to do first; not mechanically preventable |
| Two people register simultaneously | Low | High | `CreateFirstAdmin` is a conditional insert; the test drives it concurrently |
| The last administrator is disabled and locks everyone out | Low | High | Refused explicitly, with a test |
| Timing discloses which addresses have accounts | Low | Med | A real dummy bcrypt comparison runs on the not-found path; `TestTheDummyHashIsRealAndAsExpensiveAsARealOne` asserts it is not a stub |

## Rollback

Re-opening registration would mean removing the `BootstrapOpen` gate on the
route — a code change, no migration. Nothing about the stored data assumes the
closed state, so it is reversible; it would simply be a different product.

## Follow-ups

None — nothing was left open when this record was written.
