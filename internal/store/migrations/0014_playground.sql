-- Named request configurations for the playground.
--
-- config is an opaque JSON object: the console's own settings, stored as the
-- console sent them. It is deliberately not a set of columns. The sampling
-- parameters it holds track three providers' wire formats and have changed
-- twice already, and a column per parameter would mean a migration every time
-- the console learns a new one.
--
-- The unique index on name is what lets the save dialog offer to overwrite:
-- the clash is detected before the insert, so the operator is asked rather
-- than shown a constraint error.
--
-- Guarded with IF NOT EXISTS because rewinding a database's recorded version
-- replays every migration above it, this one included, and an unguarded CREATE
-- would turn that replay into a hard error.
CREATE TABLE IF NOT EXISTS playground_presets (
  id         TEXT PRIMARY KEY,
  name       TEXT    NOT NULL,
  dialect    TEXT    NOT NULL,
  model      TEXT    NOT NULL,
  config     TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_playground_presets_name ON playground_presets(name);
