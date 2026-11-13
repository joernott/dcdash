-- Audit log: logins and every state-changing action.
CREATE TABLE audit_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    time         INTEGER NOT NULL,              -- Unix milliseconds
    actor        TEXT NOT NULL,                 -- user name (for failed logins: the name given)
    actor_source TEXT NOT NULL,                 -- 'ldap', 'local', 'cli', 'system' or 'anonymous'
    client_ip    TEXT NOT NULL DEFAULT '',
    action       TEXT NOT NULL,                 -- e.g. 'auth.login', 'dns.record.update'
    target       TEXT NOT NULL DEFAULT '',      -- the object acted on
    result       TEXT NOT NULL,                 -- 'success', 'failure' or 'denied'
    details      TEXT NOT NULL DEFAULT '{}',    -- JSON object
    request_id   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX audit_log_time ON audit_log (time);
CREATE INDEX audit_log_actor ON audit_log (actor COLLATE NOCASE, id);
CREATE INDEX audit_log_action ON audit_log (action, id);
