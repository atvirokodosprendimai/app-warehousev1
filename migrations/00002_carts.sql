-- +goose Up
-- +goose StatementBegin

-- A saved, named selection of offers, assembled over time and exported as one
-- batch to a marketplace.
--
-- This is not the same thing as a saved filter, and the difference is why the
-- table exists. A filter answers "everything that currently matches" and its
-- membership changes underneath you as stock moves; a cart answers "the things I
-- chose", which does not.
CREATE TABLE carts (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- Membership. The cart stores a REFERENCE and never a copy: no title, no price,
-- no photo list. An item repriced after being added therefore exports at its new
-- price, which is what the operator means. Copying the price in would let a cart
-- publish a figure nobody chose to publish, and the divergence would be silent.
CREATE TABLE cart_items (
    cart_id  TEXT NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- CASCADE on the offer too: an offer that no longer exists cannot be in a
    -- batch, and leaving the row would make a cart export fail on a dangling id
    -- rather than simply being one item shorter.
    offer_id TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    added_at TEXT NOT NULL,
    -- An offer is either in a cart or it is not. Adding it twice is not a
    -- quantity -- quantity lives on the offer.
    PRIMARY KEY (cart_id, offer_id)
) STRICT;

-- Reading a cart is "give me this cart's items in order", so position leads.
CREATE INDEX cart_items_order_idx ON cart_items (cart_id, position);
-- And the offer page asks the reverse: which batches is this item already in.
CREATE INDEX cart_items_offer_idx ON cart_items (offer_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE cart_items;
DROP TABLE carts;
-- +goose StatementEnd
