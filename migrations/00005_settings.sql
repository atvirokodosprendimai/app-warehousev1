-- +goose Up
-- +goose StatementBegin

-- Deployment values an administrator can change without a restart.
--
-- Key/value rather than a column per setting, because the alternative is a
-- migration every time somebody needs one more knob, and these are read as a
-- handful of named strings rather than queried or joined.
--
-- The first and most consequential is the public base URL: the origin a
-- marketplace fetches photographs from. It was environment-only, which meant a
-- deployment that got it wrong produced listings with no pictures and no error
-- from anyone -- and the only fix was a restart by whoever owns the process.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE settings;
-- +goose StatementEnd
