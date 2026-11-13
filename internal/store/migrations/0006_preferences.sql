-- Per-user preferences of the web interface.
CREATE TABLE user_preferences (
    username   TEXT PRIMARY KEY,          -- lower-case login name
    language   TEXT NOT NULL,             -- '' (browser language), 'en' or 'de'
    theme      TEXT NOT NULL,             -- 'system', 'light' or 'dark'
    updated_at INTEGER NOT NULL           -- Unix seconds
) STRICT;
