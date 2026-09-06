-- +goose Up
-- +goose StatementBegin

-- Monotonic counters, so a reference can be short enough to write on a label and
-- read back off a shelf.
--
-- The generated reference used to be "WH-<date>-<8 hex>". It was unique and
-- unguessable and completely unusable for its actual job: somebody copies it
-- onto a box by hand and later types it into a search field. Eight hex digits
-- are not memorable, not dictatable over a phone, and easy to mistranscribe
-- (0/O, 8/B) with no check that would notice.
--
-- A counter is a separate table rather than MAX(sku)+1 over offers because the
-- reference is a STRING there, so "MAX" would order lexically, and because a
-- deleted offer must not hand its number to the next one -- a reference that has
-- been written on a physical label is spent whether or not the row survives.
CREATE TABLE counters (
    name  TEXT PRIMARY KEY,
    value INTEGER NOT NULL
) STRICT;

-- Seeded at zero, so the first allocation is 1 and the first reference reads
-- WH0000001. Existing offers keep whatever reference they already have: a SKU is
-- the Shopify handle and the eBay custom label, so rewriting a published one
-- breaks the link between a live listing and this warehouse.
INSERT INTO counters (name, value) VALUES ('offer_sku', 0);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE counters;
-- +goose StatementEnd
