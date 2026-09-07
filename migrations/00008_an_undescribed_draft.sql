-- +goose Up
-- +goose StatementBegin

-- ADR-019. A draft may have no title yet: one person photographs a thing, a
-- second person names and describes it later. This is the title's version of the
-- rule ADR-004 already applies to the price -- a value the operator does not have
-- yet is not demanded at creation, because demanding it is what makes somebody
-- type a placeholder, and a placeholder that reaches a listed status ships to a
-- marketplace as though it were real.
--
-- The queue of work: drafts that have been photographed and not yet named.
-- Mirrors offers_needs_pricing_idx, which indexes the same shape one column over.
CREATE INDEX offers_needs_describing_idx ON offers (status, title);

-- The publication guard, asserted here as well as in Offer.Validate.
--
-- ADR-004 states the reason for doubling up and it is unchanged: the Go check
-- gives an operator a sentence they can act on, and the database check means no
-- other writer -- a migration, a future importer, a hand-run UPDATE -- can get
-- around it.
--
-- ⚠ A TRIGGER RATHER THAN A `CHECK`, AND THE REASON IS SQLITE, NOT PREFERENCE.
-- SQLite cannot add a CHECK constraint to an existing table; it would mean
-- rebuilding `offers` -- twelve columns, three indexes, two constraints and a
-- foreign key -- and then reversing that rebuild in a down migration, in a
-- project whose down migrations have never once been run. These two triggers
-- enforce the identical invariant, are added without touching the table, and
-- their down is two DROP statements that are obviously correct.
--
-- The WHEN clause reads only the NEW row, so an UPDATE that never touches the
-- title passes as long as the row it leaves behind is valid.
CREATE TRIGGER offers_title_required_when_published_ins
BEFORE INSERT ON offers
FOR EACH ROW WHEN NEW.status IN ('listed','pending') AND trim(NEW.title) = ''
BEGIN
    SELECT RAISE(ABORT, 'an offer in a published status needs a title, because that status is exported to a marketplace');
END;

CREATE TRIGGER offers_title_required_when_published_upd
BEFORE UPDATE ON offers
FOR EACH ROW WHEN NEW.status IN ('listed','pending') AND trim(NEW.title) = ''
BEGIN
    SELECT RAISE(ABORT, 'an offer in a published status needs a title, because that status is exported to a marketplace');
END;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER offers_title_required_when_published_upd;
DROP TRIGGER offers_title_required_when_published_ins;
DROP INDEX offers_needs_describing_idx;
-- +goose StatementEnd
