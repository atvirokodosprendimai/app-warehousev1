# ADR-012: Make search an external-content FTS5 index the database keeps in step

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-002, ADR-010, ADR-013, `migrations/00004_offer_search.sql`
**Governs:** `migrations/00004_offer_search.sql`, `internal/offer/repo.go`
**Enforced-by:** `internal/offer/repo_test.go::TestSearchIndexFollowsUpdatesAndDeletes`
**Invalidates:** none — checked
**Served-path change:** The search box matches words in any order, across reference, title, description and condition, on half-typed words, and ignores accents — so `dezute` finds `dėžutė`; and a price range filters within one currency.

## Context

Retrospective record. The requirement was *"FTS by title, description, price
range"* — which is three requirements, and the third does not belong with the
first two.

A `LIKE '%term%'` scan answers none of them well: it cannot rank, it cannot match
words in any order, it cannot match a prefix at the *start* of a word without
scanning everything, and it is accent-sensitive. The operator types on a keyboard
that is not always set to Lithuanian, so `dezute` has to find `dėžutė`.

## Existing Primitives Audit

- **FTS5, compiled into `modernc.org/sqlite`** — reused, and *verified rather
  than assumed*: `internal/store/fts_test.go::TestFTS5IsCompiledIntoTheDriver`
  creates a virtual table and runs a prefix match, a body match and a
  non-matching query. A CGO-free driver having FTS5 is exactly the kind of thing a
  README claims and a build does not deliver.
- `core.NormalizeSKUQuery` (ADR-010) — **reused** so a bare number typed in the
  search box is also tried as a reference.
- The existing `LIKE` filter is **kept** alongside for the non-search filters
  (status, location prefix); FTS replaces it only for free text.

## Decision

`offers_fts` is an **external-content** FTS5 table over `offers`, indexing `sku`,
`title`, `description` and `condition`. External content means it stores only the
index and reads the columns back from `offers`, so there is no second copy of
every title to keep honest.

The price of that is that the index does not update itself. **Three triggers** —
after insert, after delete, after update — are what keep it in step, and without
them the search goes quietly stale rather than failing. The delete trigger names
*every* indexed column, not just the key, because an external-content index is
deleted from by inserting a `'delete'` command row carrying the OLD values, and
they must match what was indexed or the index is left corrupt.

Tokenisation is `unicode61 remove_diacritics 2`, so accent folding is the
database's job rather than the query's.

The migration **backfills** from the existing rows, so search works on a database
that already has stock rather than only on rows written afterwards.

Price range is a **separate mechanism**, deliberately: an ordinary index on
`(shop_currency, shop_minor)`. It leads with the currency because comparing minor
units across currencies is meaningless (ADR-002), so every such query is scoped to
one.

What would FAIL this decision: `TestSearchIndexFollowsUpdatesAndDeletes` finding
an edited or deleted offer still matching its old text — which is precisely the
failure a trigger-maintained index has, and precisely the one that is invisible
without a test. It runs on every `go test ./...`.

## Alternatives Considered

- **`LIKE '%term%'`.** Rejected: no ranking, no any-order matching, no prefix
  matching without a full scan, accent-sensitive. It is what "search" means to
  nobody who has used a search box.
- **A contentless or standalone FTS5 table holding its own copy.** Rejected: a
  second copy of every title and description that can drift from the first, in
  exchange for avoiding three triggers.
- **Maintain the index from Go, in the repository layer.** Rejected: it is
  correct only while every writer goes through that layer, and a migration or a
  hand-run `UPDATE` would leave the index wrong with nothing to say so.
- **An external search engine.** Rejected as absurd at this scale — a second
  process and a second consistency problem for a few thousand rows.
- **Put price into the FTS index too.** Rejected: FTS matches text; a range query
  needs an ordered index, and mixing them would make both worse.

## Component / Boundary Impact

`migrations/` owns the index and the triggers, so they are part of the schema
rather than of any package. `internal/offer` owns the query that reads them. One
reason to change: how somebody finds a thing in this warehouse.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `offers_fts` virtual table (external content) | new | `migrations/00004_offer_search.sql` | `internal/offer` |
| `offers_fts_ai` / `_ad` / `_au` triggers | new | `migrations/00004_offer_search.sql` | every writer of `offers` |
| `offers_shop_price_idx (shop_currency, shop_minor)` | new | `migrations/00004_offer_search.sql` | price-range filter |
| `OfferFilter.Query` uses `MATCH`, ranked | changed from `LIKE` | `internal/offer` | `internal/web` search box |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `migrations/00004_offer_search.sql` and the
search path of `internal/offer/repo.go`. No tasks directory: this is a
retrospective record.

## Consequences

- **Positive:** search behaves like a search box — any order, across fields,
  prefix matching, accent-insensitive, ranked by relevance rather than recency.
- **Positive:** the index cannot drift from the rows, because the database
  maintains it for every writer, not just for the application.
- **Negative:** three triggers are schema that has to be right, and the delete
  trigger's requirement to name every indexed column is a genuinely obscure
  detail. It is commented in the migration.
- **Negative:** a query containing punctuation has to be sanitised before it
  reaches `MATCH`, or FTS5 parses it as syntax. `TestSearchSurvivesPunctuationInTheQuery`
  pins that.
- **Neutral:** adding a searchable column means changing the virtual table and
  all three triggers together.

## Out of Scope

- Fuzzy or edit-distance matching (permanent: boundary: prefix matching plus accent folding covers what mistyping actually produces here; a spell-corrector would need a corpus this warehouse does not have)
- Searching photograph contents (permanent: boundary: out of scope for a text index)
- Ranking tuned by field weight (deferred: docs/adr/BACKLOG.md)
- Searching across currencies in one price range (permanent: boundary: minor units are only comparable within one currency — ADR-002)
- Full-text search over submissions (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A trigger is dropped or a column added without updating all three | Med | High | `TestSearchIndexFollowsUpdatesAndDeletes` covers insert, update and delete |
| A driver upgrade ships without FTS5 | Low | High | `TestFTS5IsCompiledIntoTheDriver` fails at once rather than at the first search |
| A raw query breaks `MATCH` syntax | Med | Low | Sanitised; pinned by a test |
| The index and the table disagree after a manual repair | Low | Med | `rebuild` is available; nothing detects the state automatically |

## Rollback

Dropping the virtual table and the triggers reverts search to the `LIKE` filter
that still exists for the other predicates. No offer data is touched — the index
holds no content of its own, which is the point of external content.

## Follow-ups

None — nothing was left open when this record was written.
