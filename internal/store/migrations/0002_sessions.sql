-- Login sessions. The session token itself is never stored, only its SHA-256 hash.
CREATE TABLE sessions (
    token_hash   BLOB PRIMARY KEY,
    username     TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,              -- 'ldap' or 'local' (break-glass account)
    groups       TEXT NOT NULL DEFAULT '[]', -- JSON array of group DNs at login
    stage        TEXT NOT NULL,              -- 'authenticated' or a pending step (e.g. TOTP)
    csrf_token   TEXT NOT NULL,
    created_at   INTEGER NOT NULL,           -- Unix seconds
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,           -- absolute end of the session
    remote_addr  TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX sessions_username ON sessions (username);
CREATE INDEX sessions_expires_at ON sessions (expires_at);
