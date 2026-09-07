-- +goose Up
-- +goose StatementBegin

-- The marketplace category one offer should be listed under, per export profile.
--
-- A TABLE rather than a column on `offers`, because the taxonomies are per
-- marketplace and are not even the same KIND of value: eBay's category is a
-- number from its own taxonomy, Allegro's is a "Main category" and
-- "Subcategory" pair written as NAMES, and Shopify's is a free-text product
-- type. One column would force at least two of them to be wrong, silently.
--
-- `category` is TEXT for the same reason: the schema has no opinion about
-- whether it holds a number, a name or a path, because it holds all three
-- depending on the profile.
--
-- ⚠ There is deliberately NO NOT NULL default and no row for every offer. An
-- offer without a category here is the ordinary case — it exports on the
-- configured default — and requiring one would block the photograph-title-shelve
-- intake that ADR-004 exists to protect.
CREATE TABLE offer_categories (
    -- CASCADE: a category for an offer that no longer exists is not a fact about
    -- anything, unlike a sale record or a submission's history.
    offer_id TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
    -- The export profile this is for, e.g. 'ebay' — the name the profile is
    -- registered under, lower case.
    profile  TEXT NOT NULL,
    category TEXT NOT NULL,
    -- The read is always "this offer's category for this profile", so the
    -- primary key IS the index that serves it; a separate one would be dead
    -- weight on every write.
    PRIMARY KEY (offer_id, profile)
) STRICT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE offer_categories;
-- +goose StatementEnd
