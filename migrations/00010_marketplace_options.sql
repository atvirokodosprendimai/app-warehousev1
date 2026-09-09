-- +goose Up
-- +goose StatementBegin

-- ADR-022. The values a marketplace refuses a listing without, pre-entered once
-- so an operator picks from a list instead of typing eBay's taxonomy from
-- memory.
--
-- `value` is what reaches the CSV; `label` is what a person reads in the
-- dropdown. Two columns because both are needed and neither can be derived from
-- the other: `20081` is meaningless to the operator and `Antiques` is
-- meaningless to eBay.
--
-- `field` is one of the closed vocabulary in [core.MarketplaceField] —
-- 'category', 'condition', 'location'. Closed because each member has somewhere
-- to go in the exporter's options; an open one would let this table hold a field
-- no export reads, which the operator would fill in and never see published.
--
-- ⚠ NOT `category_fields` (migration 00009). That table holds questions a
-- CATEGORY asks about an item — what the thing IS. This one holds what a
-- MARKETPLACE demands about where it is LISTED. ADR-022's whole first task
-- exists because those two were one word apart.
CREATE TABLE marketplace_options (
    id       TEXT PRIMARY KEY,
    -- The export profile, lower case, as the export package registers it.
    profile  TEXT NOT NULL,
    field    TEXT NOT NULL,
    value    TEXT NOT NULL,
    label    TEXT NOT NULL,
    -- Orders the entries within one profile's field. Ties are broken by label in
    -- the read, so the list is stable even when two rows share a position.
    position INTEGER NOT NULL DEFAULT 0,
    -- ⚠ THE SAME VALUE TWICE IS A DROPDOWN WITH TWO ENTRIES THE OPERATOR CANNOT
    -- TELL APART, and it is exactly what a double-submit produces. The
    -- constraint is also the index that serves "every option for this profile
    -- and field", which is the only read this table has.
    UNIQUE (profile, field, value)
) STRICT;

-- What ONE offer says about where it is listed, per profile and per field.
--
-- This replaces `offer_categories` (migration 00007, ADR-016). That table was
-- right about its shape and too narrow about its subject: it held a per-offer
-- marketplace VALUE, and category was simply the only one wired. ADR-016's own
-- reasoning for a table rather than a column — the taxonomies are per
-- marketplace and not even the same KIND of value — applies unchanged, one
-- dimension wider.
--
-- ⚠ There is deliberately NO row for every offer. An offer that says nothing
-- here is the ordinary case and exports on the configured default; demanding a
-- value at intake would block the photograph-title-shelve flow ADR-004 protects.
CREATE TABLE offer_marketplace_values (
    -- CASCADE: a marketplace value for an offer that no longer exists is not a
    -- fact about anything, unlike a sale record or a submission's history.
    offer_id TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
    profile  TEXT NOT NULL,
    field    TEXT NOT NULL,
    value    TEXT NOT NULL,
    -- The read is always "this offer's values", so the primary key IS the index
    -- that serves it.
    PRIMARY KEY (offer_id, profile, field)
) STRICT;

-- ⚠ COPY BEFORE DROP, AND IN THIS ORDER. Every existing row is a category, so it
-- carries forward under that field. Reversing these two statements loses every
-- per-offer category an operator has already set, silently, and the loss would
-- only surface as listings quietly moving to the default category.
INSERT INTO offer_marketplace_values (offer_id, profile, field, value)
SELECT offer_id, profile, 'category', category FROM offer_categories;

DROP TABLE offer_categories;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- ⚠ THIS DIRECTION LOSES DATA, AND IT IS RECORDED RATHER THAN HIDDEN. The table
-- being restored has one column for one field, so per-offer CONDITION and
-- LOCATION values have nowhere to go and are dropped. ADR-022's Rollback section
-- says so; `TestEveryMigrationCanBeRolledBack` executes this, so the loss is
-- exercised rather than assumed.
CREATE TABLE offer_categories (
    offer_id TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
    profile  TEXT NOT NULL,
    category TEXT NOT NULL,
    PRIMARY KEY (offer_id, profile)
) STRICT;

INSERT INTO offer_categories (offer_id, profile, category)
SELECT offer_id, profile, value FROM offer_marketplace_values WHERE field = 'category';

DROP TABLE offer_marketplace_values;
DROP TABLE marketplace_options;

-- +goose StatementEnd
