# ADR-022: Pre-enter a marketplace's must-have values and pick each one from a list

**Status:** Proposed
**Date:** 2026-09-09
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-004, ADR-014, ADR-016, ADR-021, docs/adr/BACKLOG.md
**Governs:** `internal/core/offer.go`, `internal/core/settings.go`, `internal/core/ports.go`, `internal/export/ebay.go`, `internal/offer/repo.go`, `internal/offer/service.go`, `internal/web/handlers_settings.go`, `internal/web/handlers_offer.go`, `internal/web/view/settings.templ`, `internal/web/view/offer_detail.templ`
**Enforced-by:** `internal/export/ebay_test.go::TestEBayPrefersTheOffersOwnCategory`
**Invalidates:** ADR-016 — the clause of its Decision that gives an offer exactly one per-profile value, the marketplace category. The table it created holds three kinds of value now, and the column named `category` becomes a `field`/`value` pair.
**Served-path change:** An operator filing an offer picks the eBay category, condition and dispatch location from dropdowns of values an administrator pre-entered, instead of typing a raw eBay category number into a free-text box, at `GET /offers/{id}` → `offerMarketplaceCard`.

## Context

M, 2026-09-09: *"what we need first - is default ebay category and other fields which is must have we need to preenter them and then select from dropdowns in product create/edit forms."*

Today exactly one marketplace must-have is per-offer, and it is free text. `offerMarketplaceCard`
renders `<input type="text" inputmode="numeric">` for the eBay category
(`internal/web/view/offer_detail.templ:772`), and the two other values eBay refuses a listing
without — `ConditionID` and `Location` — are single deployment-wide strings in `settings`
(`internal/core/settings.go:18-22`, ADR-016).

That shape has two costs the operator pays:

- **A raw eBay category number is not knowledge anybody holds.** `20081` is Antiques. Typing it from
  memory, per offer, is the sort of task where a transposed digit produces a listing in the wrong
  place — and ADR-016's own error text already concedes the problem it cannot solve: *"the numbers
  are eBay's own taxonomy and a warehouse that sells anything has no defensible default."* A
  warehouse that sells anything has no defensible default and DOES have a defensible short list.
- **Condition and location are deployment-wide when they are item facts.** A refurbished part and a
  broken-for-spares one are not the same condition, and a distributed warehouse (ADR-005) dispatches
  from more than one city. Both are settable only for the whole export run.

**The class this decision is about:** every site that resolves a marketplace must-have value for an
offer or for an export run. Enumerated by

```
grep -rln "\.Categories\[\|Offer\.Categories\|ConditionID\|opt\.Location\|SetCategory" internal/ \
  --include=*.go --include=*.templ | grep -v _test | grep -v _templ.go
```

which matched 15 files on 2026-09-09. Fourteen are members. `internal/core/taxonomy.go` is a false
positive from the `Categories` word collision BACKLOG.md already records — ADR-021's tree says what a
thing IS, `Offer.Categories` says where to LIST it — and that collision is the reason this record
renames the field rather than widening it in place.

## Existing Primitives Audit

| Primitive | Today | This decision |
|-----------|-------|---------------|
| `settings` key/value table (ADR-014) | Holds `ebay.category`, `ebay.condition_id`, `ebay.location` as three flat strings | **Reused unchanged.** It keeps holding the DEFAULT for each field; this record adds the list to pick from and the per-offer override, neither of which is a single named string. |
| `offer_categories` table (ADR-016) | `(offer_id, profile, category)` | **Reshaped** to `(offer_id, profile, field, value)`. One concept — a per-offer marketplace value — at the granularity there turned out to be. |
| `core.Offer.Categories map[string]string` | profile → category | **Replaced** by `Offer.Marketplace map[MarketplaceKey]string` keyed by `{Profile, Field}`. |
| `internal/settings.Repo` | Loads/saves `core.Settings` | **Extended** rather than duplicated: the option lists are marketplace configuration and belong beside the defaults they fall back to. |
| ADR-021 `category_fields` | Questions a category asks about the ITEM | **Not reused, deliberately.** A marketplace must-have is a fact about where a thing is LISTED, not about what it IS; folding them together is the collision above, one layer up. |

## Decision

For each export profile, the values a marketplace refuses a listing without become a **curated list
an administrator pre-enters once**, and every place that asks for one offers that list.

1. `core.MarketplaceField` is a closed vocabulary — `category`, `condition`, `location` — with
   `MarketplaceFields()` enumerating it. Closed because each member has a home in
   `export.Options`; a fourth is a code change, not a form field.
2. A new `marketplace_options` table holds `(profile, field, value, label, position)`. `value` is
   what reaches the CSV; `label` is what the operator reads. They are separate columns because
   `20081` and `Antiques` are both needed and neither can be derived from the other.
3. `offer_categories` becomes `offer_marketplace_values(offer_id, profile, field, value)`, carrying
   its existing rows forward as `field = 'category'`.
4. `core.Offer.Categories` becomes `core.Offer.Marketplace`, keyed by a `{Profile, Field}` struct,
   read through one accessor `Offer.MarketplaceValue(profile, field)`. The rename is deliberate and
   the compiler drives it: `Offer.Categories` and `Offer.CategoryID` are two different things one
   letter apart, and a widening that kept the old name would make that worse.
5. Settings gains a screen to manage each list. The offer editor renders a `<select>` per field.
6. **Empty remains legal at every level and always falls back to the profile default.** This is not
   a convenience — ADR-004 and ADR-019 both establish that a value the operator does not have yet
   must never be demanded at the shelf, and ADR-016's per-offer category is already optional for
   exactly that reason.

**What would make this decision wrong:** if an operator ends up maintaining the list more often than
they file offers, the list is a worse form of the free-text box. The falsifying observation is a
`marketplace_options` table that keeps growing by one row per offer filed. That is checkable —
nothing today produces the data, so it is an observation for later, not a gate now.

## Alternatives Considered

- **Fetch eBay's category tree over their API.** Rejected: it makes filing an offer depend on a
  network call to a third party, and this application has no outbound integration at all today.
  A short hand-curated list of the categories one warehouse actually uses is smaller, faster and
  works offline. Revisit when a deployment outgrows a list somebody is willing to type.
- **Leave the free-text box and add a `datalist` of suggestions.** Rejected: a `datalist` still
  accepts arbitrary input, so it removes none of the typo risk while adding a second way to be
  wrong. It would also need the same table.
- **Model the option lists as an ADR-021 category tree.** Rejected: that tree answers what an item
  IS and is inherited down a hierarchy. Marketplace must-haves are flat, per profile, and answer
  where an item is LISTED. Reusing the tree would put two unrelated vocabularies in one table and
  make the existing word collision permanent.
- **Keep `offer_categories` and add a second table for the other two fields.** Rejected: two tables
  for one concept, and every reader would have to know which field lives where. The reshape costs
  one migration and is paid once.

## Component / Boundary Impact

| Component | Ownership after | One reason to change? |
|-----------|-----------------|-----------------------|
| `internal/core` | Owns the `MarketplaceField` vocabulary, `MarketplaceOption` validation, and `Offer.Marketplace` | Yes — the domain vocabulary |
| `internal/marketplace` (new) | Owns storage and retrieval of option lists | Yes — persistence of the lists |
| `internal/offer` | Keeps owning per-offer values; its `SetCategory` widens to `SetMarketplaceValue` | Yes — the offer aggregate |
| `internal/export` | Unchanged in responsibility; reads values through the new accessor | Yes — rendering CSV |
| `internal/web` | Renders the lists and the dropdowns | Yes — the interface |

`internal/export` stays PURE: the values still ride on the offer the handler loaded, which is the
seam ADR-016 opened and this record widens rather than crosses.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `core.Offer.Categories` | Renamed to `Marketplace`, re-keyed by `{Profile, Field}` | `internal/core` | `internal/offer`, `internal/export`, `internal/web` |
| `core.Offer.MarketplaceValue(profile, field)` | New accessor — the one read path | `internal/core` | `internal/export`, `internal/web` |
| `core.MarketplaceField`, `MarketplaceFields()`, `MarketplaceOption` | New types | `internal/core` | `internal/marketplace`, `internal/web` |
| `marketplace_options` table | New | `migrations/00010` | `internal/marketplace` |
| `offer_marketplace_values` table | Replaces `offer_categories` | `migrations/00010` | `internal/offer` |
| `OfferStore.SetCategory` | Widens to `SetMarketplaceValue(ctx, id, profile, field, value)` | `internal/offer` | `internal/web` |
| `POST /offers/{id}/category` | Becomes `POST /offers/{id}/marketplace`, carrying all three signals | `internal/web` | the offer editor |
| `GET|POST /settings/marketplace` | New screen | `internal/web` | the administrator |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `core.MarketplaceField`, `MarketplaceOption`, `Offer.Marketplace`, `Offer.MarketplaceValue` | T1 | T2, T3, T4 | Yes — renames a field every export profile reads |
| `marketplace_options` + `offer_marketplace_values` schema, `marketplace.Repo` | T2 | T3, T4 | Yes — replaces a table ADR-016 created |
| `App.marketplaceOptions(ctx, profile)` read model | T3 | T4 | No — additive |

## Implementation

See `tasks/README.md`. Four tasks, sequential.

## Consequences

- **Positive:** an eBay category is chosen by its name and stored as its number, so the transposed
  digit stops being possible at the point where nobody would notice it.
- **Positive:** condition and dispatch location become item facts, which is what they are in a
  warehouse holding refurbished and broken-for-spares stock across more than one city (ADR-005).
- **Positive:** the `Offer.Categories` / `Offer.CategoryID` collision ends, and it ends by compiler
  rather than by grep — the two names are one letter apart and mean unrelated things.
- **Negative:** an administrator must enter the list before the dropdown is useful. An empty list
  renders an empty dropdown, which is a worse blank than a text box until it is filled once. T3
  ships before T4 so the list can exist first, and the field keeps a free-text escape when the list
  is empty.
- **Negative:** one more table and one data migration on a table another record created.
- **Neutral:** `export.Options.Category`/`ConditionID`/`Location` keep their meaning exactly — they
  are the fallback. Nothing about an export run changes when no offer overrides anything.

## Out of Scope

- Fetching or validating a category against eBay's live taxonomy (permanent: boundary: this application has no outbound integration, and adding one to file an offer would make intake depend on a third party being reachable)
- Per-marketplace REQUIRED item specifics that differ by category — a phone needs Brand and Storage, a chair does not (deferred: docs/adr/BACKLOG.md)
- Allegro's and Shopify's own must-have fields; only eBay's resolution is wired (deferred: docs/adr/BACKLOG.md)
- Renaming the `offer_marketplace_values.value` column's contents per profile — eBay's is a number, Allegro's a name (permanent: fact: the column is TEXT precisely so the schema has no opinion about which; citation: file `migrations/00007_offer_categories.sql:14`)
- Making the option list itself importable from a CSV (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The data migration drops per-offer categories somebody already set | Low | High | The migration copies rows before dropping, and T2's acceptance asserts a pre-existing `offer_categories` row survives as a `field='category'` row |
| The rename misses a read and an offer silently exports under the default | Med | High | The field is REMOVED rather than aliased, so every reader is a compile error — the same technique that found seventeen sites in the `Photo.ParentID` rename |
| An empty option list makes the field unusable | Med | Med | T4 renders the free-text input when the list is empty, so the screen is never worse than today |
| A `<select>` cannot express "leave empty" distinctly from "first option" | Med | Med | An explicit empty option labelled with the default it falls back to, asserted in the smoke walk |
| `internal/web` has no Go tests, so the dropdowns are provable only over the real binary | High | Med | Every T3/T4 claim is a `scripts/smoke.sh` assertion; that is the package's established proof (ADR-021 T5) |

## Rollback

`migrations/00010` carries a Down that recreates `offer_categories`, copies `field='category'` rows
back into it, and drops both new tables. Per-offer condition and location values are LOST on
rollback, because the old table has no column for them — that is stated here rather than discovered,
and it is the reason the Down is written and tested (ADR-013's `TestEveryMigrationCanBeRolledBack`
executes it) rather than assumed. The Go rename has no data component and is reverted with the
commit.

## Follow-ups

- [ ] Decide whether an option list should be seedable from the ADR-021 starter templates, so a
      warehouse that presses "Car parts" also gets recar's usual categories.
