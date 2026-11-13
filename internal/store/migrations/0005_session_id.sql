-- Public session IDs, so that administrators can address sessions without
-- knowing their tokens. Existing sessions are ended (users log in again).
DELETE FROM sessions;
ALTER TABLE sessions ADD COLUMN id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX sessions_id ON sessions (id);
