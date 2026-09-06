# ADR-005: Make storage one recursive tree whose levels are all optional

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `internal/core/location.go`, `README.md`
**Governs:** `internal/core/location.go`, `internal/location/*.go`
**Enforced-by:** `internal/core/location_test.go::TestWhereInheritsFromTheNearestAncestor`
**Invalidates:** none — checked
**Served-path change:** A bin in somebody else's garage in another city answers "who has this and where" without that bin storing either fact; and a warehouse that starts as one room grows buildings and aisles later without any stock moving.

## Context

Retrospective record. The operator's actual requirement started as *"simple
warehouse details like A000005 so I know where I put it"* and immediately added
*"later will need bigger, like a building with rooms, shelves, segments"* and then
*"some warehouses could be other people in other cities"*.

Those are one requirement, not three, and the middle one is a trap: a schema with
`building`, `room`, `shelf` columns models today's warehouse and has to be
migrated when a level is inserted or skipped. A tiny warehouse has no building.
A friend's garage has no aisle.

The third — stock in a place somebody else holds — is what makes inheritance
necessary rather than decorative. Every bin in that garage has the same custodian
and the same city, and copying those onto each bin means they drift the day the
friend moves.

## Existing Primitives Audit

- No prior location model existed. The flat code the operator types (`A000005`)
  is **reshaped** rather than replaced: it survives as the bin's own `Code`, and
  the tree is what gives it meaning once there is more than one place.
- SQLite's `TEXT` path column with a `LIKE` prefix — **reused** as the
  materialised address, rather than adding a recursive CTE at every read.

## Decision

One `locations` table, self-referencing by `ParentID`. `Kind` is an ordered but
**not mandatory** ladder: site → building → room → aisle → shelf → segment → bin.
A child must be strictly deeper than its parent, but not by one — skipping levels
is normal in a small warehouse and forbidding it would force empty placeholder
rows that mean nothing.

`Path` is the materialised full address (`KAUNAS-GARAGE/R1/S3/A000005`), unique
across the tree, so it can be pasted into a search box and resolve to exactly one
place. A rename or a move re-addresses the whole subtree in one transaction.

`Custodian`, `CustodianContact`, `City` and `Country` are set on whichever node
they first become true of and are **inherited downward at read time** by
`Location.Where(ancestors)`. The nearest setting wins, so one room lent by a
different person inside a site you otherwise control overrides just that room.
Contact travels with the custodian it belongs to — taking a nearer custodian's
name with a further one's phone number would produce a contact that reaches the
wrong person, which is worse than no contact at all.

What would FAIL this decision: `TestWhereInheritsFromTheNearestAncestor` or
`TestWhereNearerCustodianWinsWithItsOwnContact` returning a placement assembled
from the wrong ancestor. Both run on every `go test ./...`.

## Alternatives Considered

- **Fixed columns for building / room / shelf / bin.** Rejected: it cannot
  express a level that does not exist yet, and every structural change becomes a
  migration. It also cannot express skipping a level, which is the ordinary case
  at the small end.
- **A flat code with a naming convention (`B1-R2-S3-A000005`).** Rejected: the
  structure is then in a string nobody can query, and "everything in room 2"
  becomes a substring match that breaks the first time a code contains a dash.
- **Copy custodian and city onto every descendant.** Rejected: it drifts. The
  copies are correct on the day they are written and wrong the day the
  arrangement changes, silently and per-row.
- **A recursive CTE instead of a materialised path.** Rejected for read cost and
  for the search box: a stored path is directly pasteable and directly
  prefix-matchable, and the re-addressing cost is paid only on a rename.

## Component / Boundary Impact

`internal/core` owns `Kind`, `Location`, `Validate` and the inheritance rule.
`internal/location` owns the tree operations — create, rename, move, delete — and
the path re-addressing. `internal/offer` holds only a `LocationID`. One reason to
change: how this business describes where a thing is.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `locations` table, self-referencing `parent_id`, unique `path` | new | `migrations/00001_init.sql` | `internal/location` |
| `core.Location.Where([]Location) Placement` | new; read-time inheritance | `internal/core` | `internal/web`, `internal/offer` read models |
| `offers.location_id` nullable FK, `ON DELETE RESTRICT` | new | `migrations/00001_init.sql` | `internal/offer` |
| Sibling-unique code, tree-unique path | new constraints | `migrations/00001_init.sql` | `internal/location` |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/core/location.go` and
`internal/location/`. No tasks directory: this is a retrospective record.

## Consequences

- **Positive:** the schema does not change when the warehouse grows a level.
- **Positive:** an off-site holding is expressible with two fields on one node,
  and every item inside it answers correctly without storing anything.
- **Positive:** a location that still holds stock cannot be deleted — the foreign
  key restricts it, so stock cannot be orphaned by tidying.
- **Negative:** a rename re-addresses a subtree, so it is O(descendants) and must
  be transactional. `TestRenameReAddressesTheWholeSubtree` covers it.
- **Negative:** `LIKE` prefix matching on the path needs escaping; an underscore
  is a SQL wildcard and a code containing one would over-match. There is a test
  for exactly that (`TestRenameTreatsAnUnderscoreAsALetterNotAWildcard`).
- **Neutral:** depth is unbounded. Nothing needs it to be.

## Out of Scope

- Physical capacity or dimensions per location (permanent: boundary: this application tracks where a thing is, not whether it fits)
- Multi-tenancy — separate operators with separate trees (permanent: boundary: one business owns this deployment; a second would be a different product)
- A map or floor plan (permanent: boundary: a path is what somebody reads off a label; a plan is a different tool)
- Moving stock between locations in bulk (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A rename leaves a subtree half re-addressed | Low | High | Done in one transaction on the writer handle (ADR-001) |
| A code containing `/` splits the path | Med | High | `Validate` refuses it explicitly, with the reason in the error |
| A code containing `_` over-matches a prefix query | Med | Med | Escaped; `TestOffersLocationPathPrefixEscapesLikeWildcards` pins it |
| Inheritance surprises somebody who set a city on a bin | Low | Low | Nearest wins, which is the intuitive reading |

## Rollback

The tree is the storage model; there is no rollback short of a different schema.
A deployment that never creates anything but sites and bins is already the
degenerate flat case, which is the intended starting shape.

## Follow-ups

None — nothing was left open when this record was written.
