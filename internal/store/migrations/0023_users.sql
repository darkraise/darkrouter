-- The console authenticated one shared password. It now authenticates named
-- accounts, and a session records which account it belongs to.
--
-- sessions is dropped and recreated rather than altered: SQLite cannot add a
-- NOT NULL column without a constant default to a non-empty table, and a
-- nullable owner would invent a second "session with nobody behind it" state
-- to guard at every read. Live sessions end here, as they did in 0017 --
-- operators log in again once.
CREATE TABLE users (
  id            TEXT    PRIMARY KEY,
  username      TEXT    NOT NULL,
  username_lc   TEXT    NOT NULL,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL DEFAULT 'member',
  created_at    INTEGER NOT NULL
) STRICT;

-- Case-insensitive uniqueness through a column the application lowercases,
-- not COLLATE NOCASE: the collation folds ASCII only, and a rule the reader
-- can see beats one hidden in an index definition.
CREATE UNIQUE INDEX idx_users_username_lc ON users(username_lc);

DROP TABLE sessions;

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,
  user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_sessions_user ON sessions(user_id);

-- The shared password and the environment fingerprint that tracked it are
-- both retired. Nothing reads them after this release.
DELETE FROM settings
 WHERE key IN ('admin.password_hash', 'admin.password_env_fingerprint');
