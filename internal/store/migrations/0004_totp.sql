-- Enrolled TOTP secrets, encrypted with the vault key ring.
CREATE TABLE totp (
    username   TEXT PRIMARY KEY,          -- lower-case login name
    secret     BLOB NOT NULL,             -- encrypted, bound to 'totp:<username>'
    last_step  INTEGER NOT NULL,          -- last accepted time step (replay protection)
    created_at INTEGER NOT NULL           -- Unix seconds
) STRICT;

-- Single-use recovery codes. Only their SHA-256 hash is stored.
CREATE TABLE recovery_codes (
    username  TEXT NOT NULL REFERENCES totp (username) ON DELETE CASCADE,
    code_hash BLOB NOT NULL,
    used_at   INTEGER,                    -- Unix seconds; NULL while unused
    PRIMARY KEY (username, code_hash)
) STRICT;

-- A TOTP secret during enrollment, encrypted, until the user confirms it with a code.
ALTER TABLE sessions ADD COLUMN pending_totp BLOB;
