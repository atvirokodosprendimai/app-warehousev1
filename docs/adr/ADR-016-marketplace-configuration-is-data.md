# ADR-016: Make marketplace configuration data, at the granularity the marketplace uses

**Status:** Accepted
**Date:** 2026-09-07
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-014, ADR-003, `internal/export/export.go`, `README.md`
**Governs:** `internal/settings/*.go`, `internal/core/settings.go`, `internal/export/*.go`, `migrations/00007_offer_categories.sql`
**Enforced-by:** `internal/export/ebay_test.go::TestEBayPrefersTheOffersOwnCategory`
**Invalidates:** none — checked
**Served-path change:** An administrator sets the eBay category, condition and item location on the Settings page instead of restarting the process with environment variables; and any individual offer can override the category, so a warehouse selling a lamp and a chair exports both in one file.

## Context

Reported by M on 2026-09-07, as the error the export screen actually produced:

    ebay is not configured: ebay: export: invalid options: a category id is
    required; the numbers are eBay's own taxonomy and a warehouse that sells
    anything has no defensible default

— followed by *"where i can configure these?"*. The answer today is: nowhere.

Three things are wrong, and only the first is the one that was reported.

1. **`EBAY_CATEGORY` is environment-only and undocumented.** It is read once in
   `cmd/warehouse/config.go` into `export.Options` at start-up, defaults to `""`,
   and appears in **no** table in `README.md` — which lists five variables and not
   these three. So the message says a value is required, and nothing anywhere
   says where to put it.
2. **The message is right and the design is wrong.** "A warehouse that sells
   anything has no defensible default" is exactly true, and it argues against a
   single per-export-run category rather than for a better one. eBay's and
   Allegro's category numbers are per ITEM. One category per export file means one
   export per category.
3. **ADR-014 predicted this.** Its Out of Scope says: *"Any setting other than the
   public origin (permanent: boundary: one setting is what the reported problem
   needed; a second is a new decision, not an inevitability)."* This is that
   second decision, arriving three weeks early and for the same reason: a
   deployment value that shapes a produced artefact, changeable only by whoever
   can restart the process, discovered to be wrong at the moment of export.

## Existing Primitives Audit

- **The `settings` table and `internal/settings`** (ADR-014) — **reused**
  unchanged. `SaveSettings` already writes every key in ONE transaction, with a
  comment saying it was built that way precisely so a second setting would not
  arrive as a second `UPDATE` that can half-apply. That foresight is what makes
  this cheap.
- **The environment-as-start-up-default precedence** (ADR-014) — **reused**
  verbatim: the stored value wins whenever one exists, the variable is the
  first-run default, and nothing needs a restart.
- **`export.Options`** — **reshaped**: `Category` stops being *the* category and
  becomes the *default* for offers that name none.
- **`core.Offer`** — **extended** with a per-profile category map. No new
  aggregate; the categories belong to the offer and are loaded with it.

## Decision

**Per-profile defaults live in `settings`.** Three new keys — `ebay.category`,
`ebay.condition_id`, `ebay.location` — edited in a "Marketplaces" card on the
Settings page, taking effect without a restart and surviving one. `EBAY_CATEGORY`
and friends remain the start-up default, with the same precedence
`PUBLIC_BASE_URL` already has.

**A per-offer override lives with the offer.** A new table:

    offer_categories (offer_id, profile, category)   PRIMARY KEY (offer_id, profile)

`ON DELETE CASCADE` with the offer, because a category for an offer that no
longer exists is not a fact about anything. It is a table rather than a column
because the taxonomies are per marketplace and are not even the same KIND of
value: eBay's category is a number from its own taxonomy, Allegro's is a
**"Main category" and "Subcategory" pair written as names** (checked against
Allegro's own bulk-listing help page, 2026-09-07), and Shopify's is a free-text
product type. A single `category` column would force at least two of them to be
wrong, and `value` is deliberately TEXT so it can hold a number, a name or a path
without the schema having an opinion.

**Resolution order, applied per offer at export time:** the offer's category for
this profile → the profile's default from settings → the environment default →
refuse. The refusal names the SKU *and* both places the value can be set, because
the reported failure was not that a value was missing but that nothing said where
to put one.

What would FAIL this decision: `TestEBayPrefersTheOffersOwnCategory` letting the
default win over an offer that names its own; `TestEBayFallsBackToTheConfiguredDefault`
refusing an offer that has no category while a default exists; or
`TestEBayRefusalNamesWhereToSetTheCategory` producing a message that does not name
the SKU and both locations. All three run on every `go test ./...`.

## Alternatives Considered

- **Document `EBAY_CATEGORY` in the README and stop.** Rejected: it fixes the
  discoverability half and leaves the design half — one category per export run —
  which M's own error message argues against. It also keeps the value behind a
  process restart, which is the failure ADR-014 already rejected once.
- **One `category` column on `offers`.** Rejected: three marketplaces, three
  taxonomies. A single column makes at most one of them right, and silently.
- **Category per cart** (a saved batch carries its category). Rejected as the
  primary mechanism — items outside a cart still need an answer — though it
  remains expressible, because every offer in a cart can carry its own.
- **Infer the category from the item's title or condition.** Rejected outright:
  a guessed category produces a listing nobody finds, and it fails silently. This
  is the same class as a guessed photo origin (ADR-014) and gets the same answer.
- **Make the category required on every offer.** Rejected: it would block the
  intake flow ADR-004 exists to protect — photograph, title, shelve, price
  later — by demanding a marketplace taxonomy number at the shelf.

## Component / Boundary Impact

`internal/core` owns the setting keys, their validation and the resolution order.
`internal/settings` owns storage of the defaults. `internal/offer` owns the
per-offer categories and loads them with the offer. `internal/export` consumes a
resolved value and never reads settings itself — it stays a pure function of what
it is handed, which is what lets its tests assert on exact bytes (ADR-003). One
reason to change: how this business tells a marketplace what an item is.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `settings` keys `ebay.category`, `ebay.condition_id`, `ebay.location` | new rows | `internal/settings` | `cmd/warehouse`, `internal/web` |
| `core.Settings` gains `EbayCategory`, `EbayConditionID`, `EbayLocation` | new fields | `internal/core` | settings, web |
| `offer_categories` table | new | `migrations/00007_offer_categories.sql` | `internal/offer` |
| `core.Offer.Categories map[string]string` | new field, loaded with the offer | `internal/offer` | `internal/export`, `internal/web` |
| `export.Options.Category` | meaning changes: the DEFAULT, not the value | `internal/export` | eBay profile |
| Settings page "Marketplaces" card | new | `internal/web/view` | administrators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `core.Settings` marketplace fields | T1 | T3 | No — additive fields |
| `core.Offer.Categories` (T2) | T2 | T3 | No — a nil map resolves to the default |

## Implementation

Three tasks; see `tasks/README.md`.

## Consequences

- **Positive:** the reported error becomes actionable — the message names where to
  set the value, and the place it names exists.
- **Positive:** mixed stock exports in one file, which is what a second-hand
  warehouse actually has.
- **Positive:** no restart, and the value survives one, matching ADR-014.
- **Negative:** a fourth place configuration can live (offer, settings,
  environment, code default). The resolution order is written down here and in the
  refusal message, but it is more to hold than one value was.
- **Negative:** `offer_categories` is a join on the export read path. At this
  scale it is one indexed lookup per offer; it is named here so nobody is
  surprised by it later.
- **Neutral:** condition and location stay per-run. They are properties of the
  seller and the stock as a whole, not of an individual item, and nothing has
  asked otherwise.

## Out of Scope

- Validating a category number against eBay's live taxonomy (permanent: fact: that needs an authenticated Taxonomy API call per lookup, and this application talks to no marketplace API; citation: url https://developer.ebay.com/api-docs/commerce/taxonomy/overview.html)
- Suggesting a category from the item's title (permanent: boundary: a guessed category produces a listing nobody finds, and fails silently — the same class of failure as a guessed photo origin)
- Allegro's and Shopify's own category settings (deferred: docs/adr/BACKLOG.md)
- Per-offer condition or item location (permanent: boundary: both are properties of the seller and the stock as a whole; nothing has asked for them per item)
- Making a category mandatory on an offer (permanent: boundary: it would block the photograph-title-shelve intake ADR-004 protects)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An operator sets a category that is valid-looking and wrong | **High** | Med | Not detectable here — see Out of Scope. The listing appears in the wrong place, which a person notices; a refusal would need eBay's API |
| Four sources of one value confuse somebody | Med | Med | The refusal message names the order and both places a person can act on |
| The settings row is set and the environment still disagrees | Med | Low | Stored wins whenever present, exactly as `PUBLIC_BASE_URL` does; the Settings card shows which source is in force |
| The join makes exports slow at scale | Low | Low | Indexed by `(offer_id, profile)`; one lookup per offer |

## Rollback

Drop `offer_categories` and delete the three settings rows: `export.Options.Category`
returns to being the single value, fed by the environment. No offer data is lost —
the categories are additive rows, and an offer without one behaves exactly as it
does today.

## Follow-ups

None — nothing was left open when this record was written.
