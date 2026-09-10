# ADR-021: Make a category a tree that carries its own fields, inherited downward

**Status:** Accepted
**Date:** 2026-09-07
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-004, ADR-005, ADR-010, ADR-016, ADR-019, `internal/location`, `internal/core/offer.go`
**Governs:** `internal/core/taxonomy.go`, `internal/core/offer.go`, `internal/taxonomy/`, `migrations/00009_categories.sql`
**Enforced-by:** `internal/taxonomy/repo_test.go::TestFieldsInheritFromEveryAncestor`
**Invalidates:** Nothing. ⚠ It sits BESIDE ADR-016's `Offer.Categories`, which is a different thing wearing the same word — see the Decision.
**Served-path change:** An operator defines their own tree of part types — "Car parts › Engine › Turbocharger", "PC parts › GPU" — attaches fields to any level, and an offer filed under a node is asked for every field from the root down. Today the application has no such concept at all.

## Context

Reported by M on 2026-09-07: *"need to do custom field matrix so i end user could
enter their own fields for car parts, pc parts etc. like in eshops. each
'category' should have its own taxonomies"*, with screenshots of `app.recar.lt`,
and then, decisively: *"dont copy/paste we just need taxonomies with aditional
fields"* and *"which we dont have already"*.

⚠ **We genuinely do not have it, and the check matters** because this repository
already contains two things called "category". `offer_categories` (migration
00007) is the MARKETPLACE category — eBay's taxonomy number, Allegro's category
NAME, Shopify's free-text product type — keyed by export profile, existing so
each marketplace can be told where to list an item. It is per-profile, its values
are not comparable to each other, and it carries no fields. It answers "where
does this go on eBay", never "what kind of thing is this".

So there is no internal taxonomy, nothing an operator can define, and nowhere for
a VIN or an engine code to live. A warehouse of second-hand car parts and PC
parts needs different questions answered about a turbocharger than about a
graphics card, and the set of questions is the operator's business knowledge, not
ours.

⚠ **The screenshots are a REFERENCE, not a specification.** M said so directly.
What is taken from them is the SHAPE of the problem — fields differ by kind of
thing, some are free text, some are a choice from a list, some are a number with
a unit. What is deliberately NOT taken: the numbered wizard sections, the A/B/C
condition grades, the searchable canned-phrase quality list, the parts catalogue
lookup, and the OEM-code fields. Several of those duplicate what this
application already does (price, SKU, location, description, condition), and
copying a competitor's form is how you inherit their decisions without their
reasons.

## Existing Primitives Audit

- **ADR-005's recursive tree** — **reused as a PATTERN, not as a table.**
  `internal/location` is an adjacency list plus a materialised path where every
  level is optional and values are inherited downward at read time. That is this
  problem exactly, one domain over. The schema, the `Ancestors` walk and the
  subtree-rewrite discipline are copied deliberately and cited.
- **`location.Repo.Ancestors`** — **the shape field inheritance needs.** Resolving
  an offer's fields is "walk from the root to my node, collect fields in order".
- **The materialised-path traps, already paid for once** — recorded in
  `wing_craft/gotchas` from ADR-005's implementation and applied here without
  rediscovery: a subtree rewrite must use `substr(path,1,n)` rather than a
  concatenated `LIKE` (where `_` is a wildcard and silently matches a sibling
  subtree); `substr`/`length` count CHARACTERS while Go's `len` counts bytes, so
  every offset comes from `utf8.RuneCountInString`; and `UNIQUE(parent_id, code)`
  is INERT at the root because SQL treats every NULL as distinct, so `UNIQUE(path)`
  is what catches duplicate roots.
- **`core.Offer.Validate`** — **extended, not replaced.** A required field is
  checked the way ADR-004 and ADR-019 check a price and a title: not at intake.
- **`internal/settings`** — **not reused.** It holds single configured values, not
  a user-defined tree with rows per node.

## Decision

**1. A category is a node in one tree, and the tree is the operator's.**
`categories(id, parent_id, code, path, name, position)`, an adjacency list with a
materialised slash-separated `path`, exactly as `locations` is. Any depth, every
level optional, so "PC parts" alone is as legal as "Car parts › Engine ›
Turbocharger".

**2. Fields hang off any node and are INHERITED by every descendant.**
`category_fields(id, category_id, code, label, kind, unit, options, required,
position)`. An offer filed under "Turbocharger" is asked for the fields defined
on "Car parts", then "Engine", then "Turbocharger" — root first, so the general
questions come before the specific ones, which is also the order a person thinks
in. Inheritance is resolved at READ time from the ancestor walk; nothing is
copied down, so adding a field to "Car parts" reaches every part already filed.

**3. Five field kinds, and no more in this record.** `text`, `longtext`,
`number` (with an optional `unit`), `choice` (from a list the operator writes),
and `bool`. M chose these against the alternative of text-and-choice only. They
cover every field in the screenshots, and `number` is a separate kind precisely
so a mileage can later be range-filtered and a unit converted — which a string
can never be.

**4. Values live in their own table, keyed by FIELD id.**
`offer_field_values(offer_id, field_id, value)`. Keyed by field id rather than by
code, so renaming a field's label cannot orphan its values, and TEXT for the same
reason ADR-016's category column is TEXT: the column holds five kinds of thing
and the schema has no opinion about which.

⚠ **`Offer.CategoryID` (this record) and `Offer.Categories` (ADR-016) are
DIFFERENT THINGS.** The first is what the item IS; the second is where to list it
on each marketplace. Both doc comments now point at each other. They are not
merged, because a marketplace's taxonomy is that marketplace's and changes when
it says so, while this tree is the operator's and changes when they say so.

**5. A category is optional on an offer**, and a required field is enforced only
where a price and a title already are — never at intake. ADR-004 and ADR-019 both
exist because demanding a value at the shelf produces a placeholder, and a
required "VIN" would produce exactly that.

**6. A field says whether it leaves the building, and the CSV's columns are a
function of the rows.** Each field carries `export` — off by default, because a
private note about where a part came from is not something to publish. M asked
for this directly: *"in export i need to choose which taxonomies from category to
export to csv somehow"*.

⚠ **A CSV HAS ONE HEADER ROW AND THE OFFERS IN AN EXPORT DO NOT SHARE A
CATEGORY.** A turbocharger and a graphics card in the same batch inherit
different fields, so there is no fixed column set to declare in advance. The
exporter therefore computes the UNION of exported fields over the offers it is
actually writing, orders it by category path then field position so the columns
are stable for a given batch, and leaves the cell empty where an offer has no
such field. Two exports of different batches can legitimately have different
headers; that is a property of the data, not a defect, and both marketplaces
tolerate columns they do not recognise.

eBay's item specifics are `C:<Label>` columns, so an exported field becomes one.
Shopify gets a plain column named for the label. ⚠ The LABEL is what reaches the
marketplace, so renaming a field changes a published column name — which is why
the field's `code` is the stable key everything internal uses.

## Alternatives Considered

- **A flat list of categories, each with its own complete field set** — simpler
  schema, no ancestor walk, no inheritance. **Rejected by M**, and it is the right
  call: "VIN" and "Year" would be re-created on every car-part category and drift
  apart the first time one is edited. A flat list is also a one-level case of the
  tree, so the tree closes no door the flat version leaves open.
- **A JSON blob of attributes on `offers`** — one column, no new tables, trivially
  flexible. **Rejected**: nothing can then say what fields a kind of thing HAS, so
  the operator gets no form, only a free-for-all, and no field can ever be
  filtered, required, or given a unit. It also puts a second query language
  inside a database that already has one.
- **Reuse `offer_categories` for the taxonomy** — the table exists and holds a
  category. **Rejected**: it is keyed by export PROFILE, its values come from
  marketplaces rather than from us, and rows are absent for the ordinary offer.
  Overloading it would make "which eBay category" and "what kind of thing is
  this" the same column, and they diverge the moment eBay reorganises.
- **Copy recar.lt's form wholesale** — the screenshots are complete and it plainly
  works. **Rejected on M's instruction** and on merit: its numbered wizard, A/B/C
  grades and canned quality phrases are decisions with reasons we do not have,
  and several of its sections duplicate fields this application already owns.
- **Fields inherited by COPYING them down at write time** — resolution becomes a
  single-row read. **Rejected**: adding a field to a parent would then not reach
  the offers already filed beneath it, which is the case that matters most, and
  every edit would need a subtree rewrite.

## Component / Boundary Impact

| Component | Impact |
|-----------|--------|
| `internal/core` | New `Category`, `CategoryField`, `FieldKind`, `FieldValue`; `Offer` gains `CategoryID`. |
| `internal/taxonomy` | New package: the tree, its fields, and value persistence. Mirrors `internal/location`'s shape. |
| `migrations` | One migration, three tables plus a column on `offers`. |
| `internal/web` | An admin screen to shape the tree and its fields, and a card on the offer editor. |
| `internal/export` | Both profiles gain a variable tail of columns, one per exported field present in the batch. Whether a SHARED marketplace field (brand, GTIN) instead becomes a real column on `offers` is deferred to `BACKLOG.md`. |

## Wiring & Contract Changes

- `offers` gains a nullable `category_id`.
- Three new tables: `categories`, `category_fields`, `offer_field_values`.
- `core.Offer` gains `CategoryID string`.

## Inter-task Contracts

T2 and T3 both consume the ports T1 produces. T1 is independently shippable and
shows an operator nothing: a tree nobody can edit and no offer references is
inert, which is the correct half-landed state.

## Implementation

Four tasks. See `tasks/`.

## Consequences

- The operator can describe their own stock without us shipping a schema per
  trade. That is the whole point, and it is also the risk: a tree nobody prunes
  becomes a tree nobody can navigate.
- An offer's shape now depends on data, so a screen that renders it must handle a
  field kind it does not recognise rather than assuming five.
- Deleting a field deletes its values. That is deliberate — a value whose
  question no longer exists cannot be interpreted — and it is why deletion has to
  say how many values it will take with it.

## Out of Scope

- Deciding whether a SHARED marketplace field (brand, GTIN/EAN, MPN, weight) becomes a real column on `offers` rather than a custom field (deferred: docs/adr/BACKLOG.md)
- Filtering or searching the offers listing by a custom field's value (deferred: docs/adr/BACKLOG.md)
- A field whose options depend on another field's answer, as recar's quality list depends on its A/B/C grade (deferred: docs/adr/BACKLOG.md)
- Renaming `Offer.Categories` to `Offer.MarketplaceCategories` to end the word collision (deferred: docs/adr/BACKLOG.md)
- Per-category REQUIRED-ness blocking publication the way a missing price does (permanent: boundary: ADR-004 and ADR-019 both establish that the intake flow must not demand values the operator does not have yet, and a required custom field is exactly that demand under a new name)
- Importing a ready-made taxonomy from a marketplace (permanent: boundary: this tree is the operator's own vocabulary, and seeding it from eBay's would make it eBay's — the thing ADR-016 keeps separate)

## Risks

- **The tree and the marketplace category are both called "category".** Mitigated
  by naming (`CategoryID` versus `Categories`), by doc comments that point at each
  other, and by a BACKLOG entry for the rename. It remains the likeliest source of
  a wrong-field bug in this record.
- **Inheritance is resolved on every render.** It is an ancestor walk over a tree
  that is a handful of levels deep, and `location` already does the same thing on
  every warehouse page.
- **A field kind is a string in the database.** A row carrying an unknown kind is
  possible if a future version writes one and an older binary reads it; the
  renderer treats an unrecognised kind as text rather than failing the page.
- **An export's header depends on which offers are in it.** Two runs a minute
  apart can differ if the batch differs. Stated in the Decision rather than
  designed away, because the alternative — a fixed column set — means either
  every field in the whole tree as columns on every row, or a per-category export
  that cannot mix. Both are worse for the operator than a wide, mostly-empty row.
- **A renamed field changes a published column name.** The label is what the
  marketplace sees, so this is visible outside the building. The internal `code`
  never moves, so nothing here breaks; a listing tool on the far side might.

## Rollback

Drop the three tables and the column. No existing behaviour depends on them —
`Offer.CategoryID` is additive and every current screen renders without it.

## Follow-ups

- Whether a custom field can reach a marketplace as an item specific is decided
  HERE, in Decision 6. What is NOT decided is whether the fields every
  marketplace asks for — brand, GTIN/EAN, MPN, weight — should be custom fields
  at all rather than columns on `offers`; `BACKLOG.md` carries that question.
