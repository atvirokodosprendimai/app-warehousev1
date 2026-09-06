-- +goose Up
-- +goose StatementBegin

-- Accounts. There is no public registration: the only self-service path is the
-- zero-user bootstrap that mints the first admin, and every later row is
-- inserted by an admin.
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    TEXT NOT NULL,
    disabled_at   TEXT
) STRICT;

-- Session records for alexedwards/scs. The column names and types are the
-- store's, not ours.
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- The storage tree. One recursive table rather than a table per level, because
-- the levels are optional and a tiny warehouse is a site holding bins directly.
--
-- custodian/city/country are set on whichever node they first become true of and
-- are inherited downward when read, so a site in another city held by another
-- person needs no copy of those values on each of its shelves.
CREATE TABLE locations (
    id                TEXT PRIMARY KEY,
    parent_id         TEXT REFERENCES locations (id) ON DELETE RESTRICT,
    kind              TEXT NOT NULL CHECK (kind IN
                          ('site','building','room','aisle','shelf','segment','bin')),
    code              TEXT NOT NULL,
    -- Materialised full address, e.g. 'KAUNAS-GARAGE/R1/S3/A000005'. Unique so a
    -- pasted path resolves to exactly one place.
    path              TEXT NOT NULL UNIQUE,
    label             TEXT NOT NULL DEFAULT '',
    custodian         TEXT NOT NULL DEFAULT '',
    custodian_contact TEXT NOT NULL DEFAULT '',
    city              TEXT NOT NULL DEFAULT '',
    country           TEXT NOT NULL DEFAULT '',
    notes             TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    -- A code is the operator's own name for a node and only has to be unique
    -- among its siblings; two different sites may both have a shelf 'S3'.
    UNIQUE (parent_id, code)
) STRICT;
CREATE INDEX locations_parent_idx ON locations (parent_id);
CREATE INDEX locations_path_idx ON locations (path);

-- The sellable unit.
--
-- Money is stored as integer minor units plus an ISO currency code. The asking
-- price is what an export publishes; the sold price and date are kept separately
-- so a report can show what was actually realised against what was asked.
CREATE TABLE offers (
    id            TEXT PRIMARY KEY,
    sku           TEXT NOT NULL UNIQUE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    condition     TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'draft' CHECK (status IN
                      ('draft','listed','pending','sold','archived')),
    quantity      INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    ask_minor     INTEGER NOT NULL DEFAULT 0,
    ask_currency  TEXT NOT NULL DEFAULT 'EUR',
    sold_minor    INTEGER NOT NULL DEFAULT 0,
    sold_currency TEXT NOT NULL DEFAULT '',
    sold_at       TEXT,
    -- ON DELETE RESTRICT: emptying a shelf must be a deliberate act of moving
    -- the stock, never a side effect of tidying the tree.
    location_id   TEXT REFERENCES locations (id) ON DELETE RESTRICT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    -- A sold offer must carry its date, or the revenue cannot be attributed to a
    -- period. Enforced here as well as in the domain because a report is only as
    -- trustworthy as the weakest write path.
    CHECK (status <> 'sold' OR sold_at IS NOT NULL)
) STRICT;
CREATE INDEX offers_status_idx ON offers (status);
CREATE INDEX offers_location_idx ON offers (location_id);
CREATE INDEX offers_sold_at_idx ON offers (sold_at);

-- Photos. The id is a UUID and is also the public URL's path segment, so the URL
-- handed to Shopify or eBay is stable and public without being enumerable.
CREATE TABLE offer_photos (
    id           TEXT PRIMARY KEY,
    offer_id     TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
    position     INTEGER NOT NULL DEFAULT 0,
    filename     TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL,
    byte_size    INTEGER NOT NULL DEFAULT 0,
    sha256       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
) STRICT;
CREATE INDEX offer_photos_offer_idx ON offer_photos (offer_id, position);

-- Daily reference rates, base EUR, as published by the ECB.
--
-- The rate is TEXT: it is persisted, re-read and multiplied against money, and a
-- float would round-trip differently between platforms, giving a report that
-- disagrees with itself between runs.
CREATE TABLE fx_rates (
    as_of      TEXT NOT NULL,
    quote      TEXT NOT NULL,
    rate       TEXT NOT NULL,
    fetched_at TEXT NOT NULL,
    PRIMARY KEY (as_of, quote)
) STRICT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE fx_rates;
DROP TABLE offer_photos;
DROP TABLE offers;
DROP TABLE locations;
DROP INDEX IF EXISTS sessions_expiry_idx;
DROP TABLE sessions;
DROP TABLE users;
-- +goose StatementEnd
