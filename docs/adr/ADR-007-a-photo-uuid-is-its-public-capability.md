# ADR-007: Treat a photograph's UUID as its public capability

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-006, ADR-014, `internal/core/offer.go`
**Governs:** `internal/blob/*.go`, `internal/core/offer.go`
**Enforced-by:** `internal/blob/dir_test.go::TestBadIDsAreRefusedByEveryMethod`
**Invalidates:** none — checked
**Served-path change:** `GET /p/<uuid>.<ext>` serves a photograph to anybody who has the URL, with no cookie and no credential — which is the only way Shopify's and eBay's servers can fetch it.

## Context

Retrospective record, and the one place where this otherwise closed application
(ADR-006) deliberately serves an anonymous request.

Marketplaces do not proxy images. Given a CSV, Shopify and eBay fetch each URL
**from their own servers**, with no session and no headers we control. So an
image URL behind authentication produces a listing with no pictures — and, worse,
no error: the import succeeds, the listing appears, and the absence is only
noticed by a person looking at it.

That forces the URL to be public. The question this record answers is what makes
that acceptable.

## Existing Primitives Audit

- `google/uuid` — **reused** as the identifier. Version 4, 122 bits of
  randomness; nothing home-grown.
- The filesystem — **reused** as blob storage, sharded by the first bytes of the
  id so no directory grows unbounded. No object store, no database BLOB column.
- `core.Photo` already existed as the row type; the public path is **added** to it
  as a method rather than assembled by each caller.

## Decision

A photograph's UUID *is* its public name. `Photo.PublicPath()` is
`/p/<id><ext>`, and `/p/` is served without authentication. The capability is the
URL: knowing it is what grants access, and it is unguessable because a v4 UUID is.

Three properties are load-bearing together, and each one fails differently
without the others:

- **Public** — or a marketplace cannot fetch it at all.
- **Stable** — or a link that has already gone out breaks. This is why accepting
  a submission (ADR-011) *moves* the photo rows and keeps the same id rather than
  re-uploading: a re-upload would mint a new UUID and break a URL an administrator
  may already have shared.
- **Unguessable** — because it is public. A sequential id would let anybody who
  found one photo enumerate the whole warehouse, which is a disclosure of stock
  levels and of what is worth stealing.

The extension is carried on the URL because a fetcher that sniffs the URL rather
than the `Content-Type` header still has to get it right.

The other half of "public" is that the id reaches the filesystem. Every `blob`
method validates the id before touching a path, so an id that is not a UUID
cannot become a traversal. That check is the `Enforced-by` above.

What would FAIL this decision: `TestBadIDsAreRefusedByEveryMethod` letting a
non-UUID through to a path operation. It runs on every `go test ./...`, and
`scripts/smoke.sh` separately fetches a photo anonymously against a real running
binary, which is the only check that the route is actually reachable without a
cookie.

## Alternatives Considered

- **Serve images behind the session.** Rejected on the measured behaviour of the
  consumer: the marketplace fetches server-side and has no session, and the
  failure is a silent listing with no pictures.
- **Signed, expiring URLs.** Rejected: a marketplace re-fetches an image at times
  nobody controls — on edit, on re-index, sometimes months later — so an
  expiring URL is a listing that loses its pictures later, which is worse than
  one that never had them because nobody is watching by then.
- **A sequential or filename-derived id.** Rejected: it makes the whole
  collection enumerable from one leaked URL, and it leaks the operator's original
  filenames.
- **Upload images to the marketplace directly via its API.** Rejected as a much
  larger surface — per-marketplace credentials, rate limits, and a second copy to
  keep in step — for a CSV-based workflow that does not need it.

## Component / Boundary Impact

`internal/blob` owns bytes on disk and the id validation. `internal/core` owns the
URL construction. `internal/web` owns the one unauthenticated route. One reason to
change: how images reach a marketplace.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `GET /p/{id}{ext}` unauthenticated | new route, deliberately outside the auth middleware | `internal/web` | Shopify, eBay, any browser with the link |
| `core.Photo.PublicPath()` / `.PublicURL(base)` | new | `internal/core` | `internal/export`, `internal/web` |
| `blob.Dir` sharded filesystem store | new | `internal/blob` | offer, submission |
| `offer_photos.id` = the public UUID | new column semantics | `migrations/00001_init.sql` | everything above |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`. The route is registered outside the
authentication middleware in `cmd/warehouse`; the id validation is in
`internal/blob`. No tasks directory: this is a retrospective record.

## Consequences

- **Positive:** exports work, which is the whole point of the application.
- **Positive:** a link shared with a buyer or a holder keeps working across a
  submission conversion, because the id never changes.
- **Negative:** anybody with a URL has the image for ever. There is no revocation
  short of deleting the photo.
- **Negative:** the route is unauthenticated, so it is the one place a
  misconfiguration exposes something. It serves only bytes from the photo
  directory and only for a well-formed UUID.
- **Neutral:** the URL is only as useful as the base it is built on, which is why
  the public origin is a first-class setting (ADR-014).

## Out of Scope

- Revoking a photograph URL that has already been shared (permanent: boundary: a capability URL cannot be recalled from a third-party server that has already fetched it; deleting the photo is the available remedy)
- Watermarking or resizing on the fly (deferred: docs/adr/BACKLOG.md)
- Serving images from a CDN (permanent: boundary: the public origin is a single setting, so putting a CDN in front is a deployment choice this application neither needs to know about nor prevents)
- Rate limiting the public route (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An id reaches the filesystem unvalidated and becomes a traversal | Low | High | Every `blob` method validates first; `TestBadIDsAreRefusedByEveryMethod` covers all of them |
| Stock is enumerated from one leaked URL | Low | Med | A v4 UUID is not enumerable |
| The public origin is wrong, so every URL 404s at the marketplace | **High** | High | This has a record of its own (ADR-014): the export refuses a base URL a marketplace cannot fetch, and the settings page warns when the address only resolves locally |
| A photo is deleted while a marketplace still lists it | Med | Low | The listing shows a broken image; the offer is the thing that should be withdrawn |

## Rollback

Putting `/p/` behind the session is a one-line routing change, and it would break
every export. There is no partial rollback: the route is public or the product
does not work.

## Follow-ups

None — nothing was left open when this record was written.
