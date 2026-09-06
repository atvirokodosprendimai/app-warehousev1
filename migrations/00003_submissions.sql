-- +goose Up
-- +goose StatementBegin

-- Something a staff user is offering to the warehouse, waiting for an
-- administrator to decide.
--
-- A submission is deliberately NOT an offer. A staff member has a photograph, a
-- name for the thing and an idea of what they want for it, and nothing else an
-- export would need. Making them fill in an offer would either block the
-- submission behind fields they cannot answer, or admit half-built offers into
-- the catalogue where an export could pick one up. This becomes an offer only
-- when an administrator says so.
CREATE TABLE submissions (
    id              TEXT PRIMARY KEY,
    -- RESTRICT rather than CASCADE: deleting a person must not silently erase
    -- the record of what they handed over and what was agreed for it.
    submitted_by    TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    title           TEXT NOT NULL,
    note            TEXT NOT NULL DEFAULT '',
    -- What the submitter hopes to receive. Optional -- "what will you give me
    -- for this" is a legitimate submission, and demanding a number makes people
    -- invent one. On acceptance this seeds the offer's OWNER price, because the
    -- submitter is the item's owner and that is exactly what owner_* means.
    asking_minor    INTEGER NOT NULL DEFAULT 0,
    asking_currency TEXT NOT NULL DEFAULT 'EUR',
    status          TEXT NOT NULL DEFAULT 'new' CHECK (status IN
                        ('new','reviewing','accepted','declined')),
    review_note  TEXT NOT NULL DEFAULT '',
    -- SET NULL, not CASCADE: if the offer is later deleted the submission's
    -- history should survive as a record that it was accepted, rather than
    -- vanishing along with it.
    offer_id        TEXT REFERENCES offers (id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL,
    reviewed_at     TEXT,
    reviewed_by     TEXT REFERENCES users (id) ON DELETE SET NULL,
    -- A decline without a reason produces the same submission again next week.
    CHECK (status <> 'declined' OR review_note <> ''),
    -- An accepted submission must name what it became, or the audit trail from
    -- "we agreed a price on the phone" to "this is the listing" is broken.
    CHECK (status <> 'accepted' OR offer_id IS NOT NULL)
) STRICT;

-- The inbox reads "everything still open, newest first".
CREATE INDEX submissions_status_idx ON submissions (status, created_at);
-- And a staff user reads "the things I sent".
CREATE INDEX submissions_by_idx ON submissions (submitted_by, created_at);

-- Photographs attached to a submission.
--
-- Same shape as offer_photos on purpose. Accepting a submission MOVES these rows
-- to offer_photos keeping the SAME id, so the blob on disk is untouched and the
-- public URL an admin may already have shared stays valid. Re-uploading would
-- mint a new UUID and break any link that had gone out.
CREATE TABLE submission_photos (
    id            TEXT PRIMARY KEY,
    submission_id TEXT NOT NULL REFERENCES submissions (id) ON DELETE CASCADE,
    position      INTEGER NOT NULL DEFAULT 0,
    filename      TEXT NOT NULL DEFAULT '',
    content_type  TEXT NOT NULL,
    byte_size     INTEGER NOT NULL DEFAULT 0,
    sha256        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
) STRICT;
CREATE INDEX submission_photos_idx ON submission_photos (submission_id, position);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE submission_photos;
DROP TABLE submissions;
-- +goose StatementEnd
