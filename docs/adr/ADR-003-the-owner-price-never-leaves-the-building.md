# ADR-003: Keep three prices, and never export the owner price

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-002, ADR-014, `internal/export/export.go`
**Governs:** `internal/export/*.go`, `internal/core/offer.go`
**Enforced-by:** `internal/export/shopify_test.go::TestShopifyNeverExportsTheOwnerPrice`
**Invalidates:** none — checked
**Served-path change:** An exported CSV contains the shop price and no trace of what the item's holder is owed; the margin is shown in the dashboard at the moment the operator is deciding whether to list.

## Context

This record is retrospective, and it is the one in this corpus with a
confidentiality consequence rather than a correctness one.

Stock lives in other people's places — a friend's garage in another city, a
partner's back room. Those people are owed a figure that was negotiated privately
with them. The marketplace listing is public by construction: Shopify and eBay
publish what they are given, and a CSV column nobody meant to fill is still a
column somebody can read.

So an offer carries three amounts that are three different facts, agreed with
different people at different times:

| Field | What it is | Who agreed it |
|---|---|---|
| `Shop` | what we publish | us |
| `Owner` | what the holder is to receive | the holder, privately |
| `Sold` | what it actually fetched | the buyer |

Storing only two of them and deriving the third loses whichever was agreed first.
A margin percentage in place of `Owner` would lose the actual sum owed, which is
the number that gets paid to a person.

## Existing Primitives Audit

- `core.Money` (ADR-002) — **reused** for all three amounts. They differ in
  meaning, not in representation.
- `core.Status.Exportable()` — **reused** as the other half of the export gate:
  it decides *whether* an offer is published, this decides *what* of it is.
- No prior export layer existed to reshape.

## Decision

`core.Offer` carries `Shop`, `Owner` and `Sold` as three independent `Money`
values. `Margin()` is `Shop − Owner` and `Realised()` is `Sold − Owner`; both are
derived at read time and neither is stored, so they cannot go stale against the
figures they come from.

No exporter may emit `Owner`, in any column, in any profile. The package comment
on `internal/export` states this as a package-level rule, and **each profile
carries its own test** that renders an offer with a distinctive owner amount and
asserts those digits appear nowhere in the produced bytes.

The check is deliberately on the *bytes*, not on the column list. A column-list
assertion tests the mapping the author wrote; a byte assertion tests the file the
marketplace receives, and it keeps working when somebody adds a column.

What would FAIL this decision: either `TestShopifyNeverExportsTheOwnerPrice` or
`TestEBayNeverExportsTheOwnerPrice` finding the owner digits in the output. Both
run on every `go test ./...`. A new profile added without such a test is the real
hazard, and it is not mechanically caught — see Risks.

## Alternatives Considered

- **Store a margin percentage instead of an owner price.** Rejected: the holder
  is owed a sum, not a ratio, and a percentage of a shop price that later changes
  silently changes what somebody is paid.
- **One price plus a per-location commission rule.** Rejected: the figure is
  negotiated per item, not per place — a valuable item in the same garage is
  often on different terms.
- **Redact the owner price in the exporter's writer, centrally.** Rejected as
  weaker than it looks: a central redactor has to know every column that could
  carry the value, which is the same knowledge the per-profile test asserts,
  except failing open instead of closed.
- **Keep the owner price in a separate table nobody joins in the export path.**
  Considered and rejected as over-engineering for the size: the join is not what
  causes the leak, a column mapping is, and the test catches a column mapping.

## Component / Boundary Impact

`internal/core` owns the three fields and the two derivations. `internal/export`
owns the boundary and is the only package that writes bytes anybody outside sees.
`internal/web` renders the margin on the pricing screen, which is inside the
authenticated boundary. One reason to change: what this business publishes.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `Offer.Shop` / `Offer.Owner` / `Offer.Sold` | three independent amounts | `internal/core` | offer, submission, export, web |
| `Offer.Margin()` / `Offer.Realised()` | derived, never stored | `internal/core` | `internal/web` pricing screen |
| Shopify `Variant Price`, eBay `StartPrice` | fed from `Shop` only | `internal/export` | Shopify, eBay |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`. The rule is stated in the `internal/export`
package comment and asserted in `shopify_test.go` and `ebay_test.go`. No tasks
directory: this is a retrospective record.

## Consequences

- **Positive:** the private figure cannot reach a public feed without a test
  going red.
- **Positive:** the margin is visible exactly where the decision is made, and it
  refuses to subtract across currencies rather than inventing a rate.
- **Negative:** three amounts is three times the surface for a mapping mistake,
  and only the owner one is guarded by a byte-level assertion.
- **Neutral:** `Realised()` gives a report the sold-versus-owed figure without
  any further schema.

## Out of Scope

- A per-holder statement or payout run (deferred: docs/adr/BACKLOG.md)
- Any marketplace profile beyond Shopify and eBay (permanent: boundary: two profiles are what this business sells through; a third would be a new record, and would need its own owner-price test)
- Hiding the owner price from signed-in staff (permanent: boundary: everyone with an account here is trusted with it; the boundary this record defends is the public feed)
- Currency conversion inside `Margin()` (permanent: boundary: a converted margin buries an undated rate inside a figure nobody can reproduce — ADR-002)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A third export profile is added without an owner-price test | Med | High | The package comment states the obligation; no mechanical check exists — this is the weakest point in the record and is named here for that reason |
| A debug or diagnostic endpoint dumps a whole offer | Low | High | No such endpoint exists; adding one would be a new decision |
| The margin is mistaken for the owner price on screen | Low | Med | Both are labelled, and the pricing card shows the arithmetic |

## Rollback

Removing the owner price entirely would be a schema migration and a product
change, not a rollback. The *disclosure* rule has no rollback by design: there is
no configuration that turns it off, which is the point.

## Follow-ups

- [ ] A registration-level check that every exporter has an owner-price test would close the one gap named in Risks. Not built; the profile set is two and both are covered.
