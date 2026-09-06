-- +goose Up

-- Full-text search over the text an operator would actually type.
--
-- This is an EXTERNAL CONTENT table: it stores only the index and reads the
-- columns back from `offers`, so there is no second copy of every title and
-- description to keep honest. The price of that is that the index does not
-- update itself — the triggers below are what keep it in step, and without them
-- the search would go quietly stale rather than fail.
--
-- unicode61 with diacritic folding: "dėžutė" has to be findable by typing
-- "dezute", because that is what someone actually types on a keyboard that is
-- not set to Lithuanian.

-- +goose StatementBegin
CREATE VIRTUAL TABLE offers_fts USING fts5(
    sku,
    title,
    description,
    condition,
    content='offers',
    content_rowid='rowid',
    tokenize="unicode61 remove_diacritics 2"
);
-- +goose StatementEnd

-- Backfill whatever is already there, so search works on an existing database
-- and not only on rows written after this migration.
-- +goose StatementBegin
INSERT INTO offers_fts (rowid, sku, title, description, condition)
SELECT rowid, sku, title, description, condition FROM offers;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER offers_fts_ai AFTER INSERT ON offers BEGIN
    INSERT INTO offers_fts (rowid, sku, title, description, condition)
    VALUES (new.rowid, new.sku, new.title, new.description, new.condition);
END;
-- +goose StatementEnd

-- An external-content index is deleted from by INSERTING a 'delete' command row
-- carrying the OLD values. They must match what was indexed or the index is left
-- corrupt, which is why the trigger names every column rather than just the key.
-- +goose StatementBegin
CREATE TRIGGER offers_fts_ad AFTER DELETE ON offers BEGIN
    INSERT INTO offers_fts (offers_fts, rowid, sku, title, description, condition)
    VALUES ('delete', old.rowid, old.sku, old.title, old.description, old.condition);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER offers_fts_au AFTER UPDATE ON offers BEGIN
    INSERT INTO offers_fts (offers_fts, rowid, sku, title, description, condition)
    VALUES ('delete', old.rowid, old.sku, old.title, old.description, old.condition);
    INSERT INTO offers_fts (rowid, sku, title, description, condition)
    VALUES (new.rowid, new.sku, new.title, new.description, new.condition);
END;
-- +goose StatementEnd

-- Price-range filtering reads "shop price between X and Y", so the index leads
-- with the currency: comparing minor units across currencies is meaningless, and
-- every such query is therefore scoped to one.
-- +goose StatementBegin
CREATE INDEX offers_shop_price_idx ON offers (shop_currency, shop_minor);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS offers_shop_price_idx;
DROP TRIGGER IF EXISTS offers_fts_au;
DROP TRIGGER IF EXISTS offers_fts_ad;
DROP TRIGGER IF EXISTS offers_fts_ai;
DROP TABLE IF EXISTS offers_fts;
-- +goose StatementEnd
