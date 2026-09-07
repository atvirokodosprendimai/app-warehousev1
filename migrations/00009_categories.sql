-- +goose Up
-- +goose StatementBegin

-- ADR-021. The operator's OWN taxonomy of what a thing IS, and the questions
-- each kind of thing answers.
--
-- ⚠ THIS IS NOT `offer_categories` (migration 00007), THOUGH BOTH SAY
-- "category". That table is the MARKETPLACE category — eBay's taxonomy number,
-- Allegro's category NAME, Shopify's free-text product type — keyed by export
-- profile, and it answers "where does this go on eBay". These tables answer
-- "what kind of thing is this", which is the operator's own vocabulary and
-- changes when they say so rather than when a marketplace does. They are
-- deliberately not merged; BACKLOG.md carries the rename that would end the
-- word collision.

-- One tree, the operator's. An adjacency list plus a materialised
-- slash-separated path, exactly as `locations` is (ADR-005) — the same shape
-- solved the same problem one domain over, so the discipline is copied rather
-- than reinvented.
--
-- Every level is optional and any depth is legal: "PC parts" alone is as valid
-- as "Car parts / Engine / Turbocharger".
CREATE TABLE categories (
    id        TEXT PRIMARY KEY,
    -- RESTRICT rather than CASCADE: deleting a node that still has children is
    -- almost always a mistake, and the tree is small enough that moving them
    -- first is no hardship. NULL is a root.
    parent_id TEXT REFERENCES categories (id) ON DELETE RESTRICT,
    -- The operator's short handle for this node, unique among its siblings and
    -- used in the path. It cannot contain "/" — the domain refuses it, because
    -- the path is slash-separated and a "/" in a code would split it.
    code      TEXT NOT NULL,
    -- The materialised address, e.g. "CAR/ENGINE/TURBO". Unique across the whole
    -- tree, and path order IS tree order, so a picker can render an indented
    -- tree straight from `ORDER BY path` without grouping it again.
    path      TEXT NOT NULL,
    -- What a human reads, e.g. "Turbocharger".
    name      TEXT NOT NULL,
    -- Sibling order, so an operator can put the common things first.
    position  INTEGER NOT NULL DEFAULT 0,
    -- ⚠ BOTH CONSTRAINTS ARE NEEDED AND THEY ARE NOT REDUNDANT. UNIQUE(path)
    -- is what actually catches a duplicate ROOT: SQL treats every NULL as
    -- distinct from every other, so UNIQUE(parent_id, code) is INERT where
    -- parent_id IS NULL and two roots called "CAR" would both insert. The pair
    -- is kept anyway because it produces the clearer error for the ordinary
    -- deeper case, and because `location` learned this the hard way already.
    UNIQUE (path),
    UNIQUE (parent_id, code)
) STRICT;

-- Children of a node, in the order the operator arranged them.
CREATE INDEX categories_parent_idx ON categories (parent_id, position);

-- The questions a node asks. INHERITED by every descendant, resolved at READ
-- time from the ancestor walk — nothing is copied down, so a field added to
-- "Car parts" reaches every part already filed beneath it. That is the case
-- that matters most and it is exactly what copying down would lose.
CREATE TABLE category_fields (
    id          TEXT PRIMARY KEY,
    -- CASCADE: a question attached to a category that no longer exists is not a
    -- fact about anything.
    category_id TEXT NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    -- The stable internal key. ⚠ It is `code`, never `label`, that everything
    -- internal uses, because the LABEL is what reaches a marketplace as a column
    -- name and an operator may rename it whenever they like.
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    -- One of: text, longtext, number, choice, bool. TEXT rather than an enum
    -- because SQLite has none; core.ParseFieldKind refuses anything else on the
    -- way in, and a renderer treats an unrecognised kind as text rather than
    -- failing the page (ADR-021, Risks).
    kind        TEXT NOT NULL,
    -- Optional, and only meaningful for `number`, e.g. "km" or "kW". A separate
    -- column and not part of the value, because a mileage stored as "180000 km"
    -- can never be range-filtered or converted.
    unit        TEXT NOT NULL DEFAULT '',
    -- Newline-separated options, and only meaningful for `choice`.
    options     TEXT NOT NULL DEFAULT '',
    -- A hint to the operator, NOT a gate. ADR-004 and ADR-019 both exist because
    -- demanding a value at the shelf is what makes somebody type a placeholder,
    -- and a required "VIN" would produce exactly that. Nothing blocks a save or
    -- a publication on this.
    required    INTEGER NOT NULL DEFAULT 0,
    -- Whether this field leaves the building in a CSV. ⚠ OFF BY DEFAULT, and
    -- that is the decision rather than a convenience: a private note about where
    -- a part came from is not something to publish, and a default of true would
    -- publish every field an operator ever created without them choosing to.
    export      INTEGER NOT NULL DEFAULT 0,
    position    INTEGER NOT NULL DEFAULT 0,
    UNIQUE (category_id, code)
) STRICT;

-- The fields of a node, in the operator's order. Read on every ancestor walk.
CREATE INDEX category_fields_category_idx ON category_fields (category_id, position);

-- One answer to one question about one offer.
--
-- ⚠ KEYED BY FIELD ID, NEVER BY CODE. Renaming a field's label or its code must
-- not orphan the answers already given, and an id is the only thing here that
-- never moves.
CREATE TABLE offer_field_values (
    -- CASCADE: an answer about an offer that no longer exists is not a fact.
    offer_id TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
    -- CASCADE: an answer whose QUESTION no longer exists cannot be interpreted.
    -- Deliberate, and it is why deleting a field has to say how many values it
    -- will take with it (ADR-021, Consequences).
    field_id TEXT NOT NULL REFERENCES category_fields (id) ON DELETE CASCADE,
    -- TEXT for the same reason offer_categories.category is TEXT: this column
    -- holds five kinds of thing and the schema has no opinion about which.
    -- `bool` is stored as "1" or "", `number` as the digits the operator typed.
    value    TEXT NOT NULL,
    -- The read is always "this offer's answers", so the primary key IS the index
    -- that serves it.
    PRIMARY KEY (offer_id, field_id)
) STRICT;

-- What this offer IS. Nullable, because a category is OPTIONAL on an offer:
-- ADR-004's photograph-title-shelve intake must not acquire a new thing to
-- demand at the shelf.
--
-- ⚠ Not to be confused with `offer_categories`, which is per-marketplace.
-- SET NULL rather than RESTRICT: an operator reorganising their vocabulary
-- should not be blocked by stock, and an offer that has lost its filing is
-- visible and fixable, whereas a refused delete is a dead end.
ALTER TABLE offers ADD COLUMN category_id TEXT REFERENCES categories (id) ON DELETE SET NULL;

-- Everything filed under a node, which is what a later "show me the
-- turbochargers" read needs.
CREATE INDEX offers_category_idx ON offers (category_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The column goes first: while it exists it references `categories`, and with
-- foreign keys enabled (both application handles set foreign_keys(1)) dropping
-- the table underneath it would be refused.
DROP INDEX offers_category_idx;
ALTER TABLE offers DROP COLUMN category_id;

DROP TABLE offer_field_values;
DROP TABLE category_fields;
DROP TABLE categories;

-- +goose StatementEnd
