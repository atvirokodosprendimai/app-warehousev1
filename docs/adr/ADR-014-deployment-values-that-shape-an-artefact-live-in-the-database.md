# ADR-014: Keep deployment values that shape an artefact in the database

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-007, ADR-003, `internal/core/settings.go`
**Governs:** `internal/core/settings.go`, `internal/settings/*.go`, `migrations/00005_settings.sql`
**Enforced-by:** `internal/export/export_test.go::TestWriteRejectsBaseURLAMarketplaceCannotFetch`
**Invalidates:** none — checked
**Served-path change:** An administrator sets the public domain on a Settings page; it takes effect immediately without a restart, survives one, and the export screen says plainly when the address in use only resolves on this machine.

## Context

Retrospective record, opened by a direct report: *"there is no way to set public
URLs"*, then *"domain"*.

The public origin is the address a marketplace fetches photographs from
(ADR-007). It was environment-only, which produced the worst available failure
shape:

- A deployment that gets it wrong produces listings with **no pictures and no
  error from anyone**. Shopify and eBay fetch server-side; a failed fetch is a
  listing that simply has no images. Nobody is told.
- The default is `http://localhost:8080`, which is *valid*, *absolute*, and
  reachable by exactly one machine — so a fresh install is in the broken state by
  default.
- The person who notices is the person looking at the listing. The person who can
  fix an environment variable and restart the process is somebody else.

## Existing Primitives Audit

- Environment variables — **kept**, as the start-up default. This record does not
  remove them; it adds a layer above.
- `internal/store`'s writer handle (ADR-001) — **reused**.
- There was no settings mechanism to reshape; this is the first, and it is
  deliberately the smallest one that answers the question.

## Decision

A `settings` table of key/value pairs, read at request time, written by an
administrator through a Settings page. Key/value rather than a column per
setting, because the alternative is a migration every time somebody needs one
more knob, and these are read as a handful of named strings rather than queried
or joined.

The environment variable becomes the **start-up default**: used when nothing is
stored, overridden the moment an administrator sets a value, and never consulted
again. No restart, and the value survives one.

The validation is split in two, and the split is the interesting part:

- **`ValidatePublicBaseURL` refuses what cannot work.** Absoluteness *is*
  decidable from the string: a relative address, a missing scheme, a path, a query
  or a fragment are all refused with a sentence saying why.
- **`ReachablePublicly` is a WARNING, not a validation.** `localhost` and
  `127.0.0.1` are perfectly correct for somebody running the application on their
  laptop to try it out. Refusing them would break the ordinary first-run case to
  guard against a mistake that only bites at export time. So the address is
  accepted, and the export screen says plainly that a marketplace will not be able
  to fetch from it — at the one place where the fact becomes consequential.

And the last line of defence is in the exporter itself: `export.Write` **refuses**
a base URL a marketplace cannot fetch rather than producing a file full of
unusable links. A refusal is read the moment it happens; a bad export is
discovered by a person looking at a listing days later.

What would FAIL this decision: `TestWriteRejectsBaseURLAMarketplaceCannotFetch`
accepting a relative base, or `TestReachablePubliclyIsAWarningNotAValidation`
turning the warning into a refusal and breaking the first-run case. Both run on
every `go test ./...`.

## Alternatives Considered

- **Environment variable only.** This is what was there. Rejected: it requires
  process access to change, and the failure it causes is silent and discovered by
  somebody who cannot fix it.
- **Refuse a localhost origin outright.** Rejected: it breaks the legitimate
  first-run case. The correct place for the objection is the export screen, where
  the address is about to matter.
- **Derive the origin from the incoming request's `Host` header.** Rejected: it
  is attacker-controlled, and it would put whatever host somebody typed into a
  file that goes to a marketplace. It would also differ between an admin browsing
  over a LAN address and the public name.
- **A configuration file on disk.** Rejected: it is a third place state lives, and
  it needs the same restart or a watcher.
- **A column per setting.** Rejected: a migration for every knob.

## Component / Boundary Impact

`internal/core` owns the validation and the reachability warning.
`internal/settings` owns storage. `internal/export` owns the refusal. `cmd/warehouse`
owns the environment fallback. One reason to change: how this deployment describes
itself to the outside world.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `settings` key/value table | new | `migrations/00005_settings.sql` | `internal/settings` |
| `core.SettingPublicBaseURL` | new key | `internal/core` | settings, web, export |
| `core.ValidatePublicBaseURL` | new; refuses what cannot work | `internal/core` | Settings page |
| `core.ReachablePublicly` | new; warns, never refuses | `internal/core` | Settings page, export screen |
| `PUBLIC_BASE_URL` env var | demoted to start-up default | `cmd/warehouse` | first run only |
| `export.Write` refusing a non-absolute base | new guard | `internal/export` | every export |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/core/settings.go`,
`internal/settings/`, `migrations/00005_settings.sql` and the export guard. No
tasks directory: this is a retrospective record.

## Consequences

- **Positive:** the one setting whose misconfiguration is silent can now be fixed
  by the person who notices, immediately, without process access.
- **Positive:** the failure is caught three times over — refused at the Settings
  page if it cannot work, warned about at the export screen if it only resolves
  locally, refused by the exporter if it would produce unusable links.
- **Negative:** state now lives in two places (environment and database) with a
  precedence rule somebody has to know. It is documented in the README.
- **Negative:** a key/value table invites more settings, and settings are where
  configuration sprawl starts. Today there is one.
- **Neutral:** the warning is advisory by design, so a laptop deployment stays
  usable and a production one that ignores it produces pictureless listings.

## Out of Scope

- Any setting other than the public origin (permanent: boundary: one setting is what the reported problem needed; a second is a new decision, not an inevitability)
- Checking that the origin actually resolves from the internet (permanent: boundary: the application cannot see itself from outside, and a DNS lookup from inside proves nothing about a marketplace's network)
- Per-marketplace origins (permanent: boundary: photographs are served from one place)
- Secrets in the settings table (permanent: boundary: this table is read by the admin UI and is not a secret store)
- A settings audit log (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An administrator sets an origin that is absolute but wrong | Med | High | The export screen warns for local-only addresses; a wrong-but-public address is not detectable from here |
| Precedence between env and database confuses somebody | Med | Low | Database wins whenever a value is stored; documented in the README |
| The settings table grows into a configuration system | Med | Med | Named in Out of Scope: one setting, and a second is a new decision |
| The exporter's guard is bypassed by a future export path | Low | High | `export.Write` is the only writer of export bytes |

## Rollback

Deleting the row restores the environment default; dropping the table restores
environment-only behaviour with a code change. No stored offer data depends on
it — the origin is applied at render time, never persisted into a row.

## Follow-ups

None — nothing was left open when this record was written.
