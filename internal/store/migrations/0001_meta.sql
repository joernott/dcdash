-- Generic key/value storage for internal state (e.g. the encryption key check).
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value BLOB NOT NULL
) STRICT;
