-- Phase 2 schema, per master design section 11.
--
-- STRICT is used throughout: without it SQLite accepts a string into an INTEGER
-- column, and a token count silently stored as text breaks the rollup rather
-- than the insert.
--
-- Money is INTEGER micro-dollars. Prices are per million tokens because
-- models.dev publishes dollars per million as floats, and micro-dollars per
-- token would truncate a $0.14/M model to zero.

CREATE TABLE providers (
  id            TEXT    PRIMARY KEY,
  name          TEXT    NOT NULL DEFAULT '',
  preset        TEXT    NOT NULL DEFAULT '',
  kind          TEXT    NOT NULL,
  base_url      TEXT    NOT NULL,
  auth_style    TEXT    NOT NULL DEFAULT 'bearer',
  enabled       INTEGER NOT NULL DEFAULT 1,
  priority      INTEGER NOT NULL DEFAULT 0,
  region        TEXT    NOT NULL DEFAULT '',
  project       TEXT    NOT NULL DEFAULT '',
  location      TEXT    NOT NULL DEFAULT '',
  settings_json TEXT    NOT NULL DEFAULT '{}',
  created_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE provider_keys (
  id           TEXT    PRIMARY KEY,
  provider_id  TEXT    NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  label        TEXT    NOT NULL DEFAULT '',
  kind         TEXT    NOT NULL DEFAULT 'static',
  ciphertext   BLOB    NOT NULL,
  nonce        BLOB    NOT NULL,
  expires_at   INTEGER,
  scope        TEXT    NOT NULL DEFAULT '',
  enabled      INTEGER NOT NULL DEFAULT 1,
  last_used_at INTEGER
) STRICT;

CREATE INDEX idx_provider_keys_provider ON provider_keys(provider_id);

CREATE TABLE models (
  provider_id                      TEXT    NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model_id                         TEXT    NOT NULL,
  publisher                        TEXT    NOT NULL DEFAULT '',
  surfaces                         TEXT    NOT NULL DEFAULT '["chat"]',
  capabilities                     TEXT    NOT NULL DEFAULT '{}',
  capabilities_source              TEXT    NOT NULL DEFAULT 'inferred',
  context_window                   INTEGER,
  max_output_tokens                INTEGER,
  input_price_micros_per_mtok      INTEGER,
  output_price_micros_per_mtok     INTEGER,
  cache_read_price_micros_per_mtok INTEGER,
  discovered_at                    INTEGER,
  state                            TEXT    NOT NULL DEFAULT 'active',
  PRIMARY KEY (provider_id, model_id)
) STRICT;

CREATE TABLE model_overrides (
  provider_id    TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model_id       TEXT NOT NULL,
  surfaces       TEXT,
  capabilities   TEXT,
  context_window INTEGER,
  PRIMARY KEY (provider_id, model_id)
) STRICT;

-- requests and request_attempts deliberately carry no foreign key onto
-- providers: deleting a provider in the UI must preserve history rather than
-- cascading it away.
CREATE TABLE requests (
  id                 TEXT    PRIMARY KEY,
  ts                 INTEGER NOT NULL,
  dialect            TEXT    NOT NULL,
  surface            TEXT    NOT NULL,
  requested_model    TEXT    NOT NULL,
  resolved_alias     TEXT    NOT NULL DEFAULT '',
  candidates_json    TEXT    NOT NULL DEFAULT '[]',
  final_provider_id  TEXT    NOT NULL DEFAULT '',
  final_model        TEXT    NOT NULL DEFAULT '',
  status             TEXT    NOT NULL,
  tokens_in          INTEGER NOT NULL DEFAULT 0,
  tokens_out         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens   INTEGER NOT NULL DEFAULT 0,
  -- NULL, never 0, until pricing for the model exists. That arrives in phase 6.
  cost_micros        INTEGER,
  ttft_ms            INTEGER,
  total_ms           INTEGER,
  error_code         TEXT    NOT NULL DEFAULT '',
  warnings_json      TEXT    NOT NULL DEFAULT '[]'
) STRICT;

CREATE INDEX idx_requests_ts ON requests(ts);

CREATE TABLE request_attempts (
  request_id  TEXT    NOT NULL,
  seq         INTEGER NOT NULL,
  provider_id TEXT    NOT NULL,
  key_id      TEXT    NOT NULL DEFAULT '',
  model       TEXT    NOT NULL,
  outcome     TEXT    NOT NULL,
  status_code INTEGER NOT NULL DEFAULT 0,
  latency_ms  INTEGER NOT NULL DEFAULT 0,
  error       TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (request_id, seq)
) STRICT;

CREATE TABLE request_bodies (
  request_id    TEXT    PRIMARY KEY,
  request_json  TEXT    NOT NULL,
  response_json TEXT    NOT NULL DEFAULT '',
  expires_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_request_bodies_expires ON request_bodies(expires_at);

-- Keyed on (provider, credential, model). An empty model is the
-- credential-level entry, used for cooldowns that apply across every model a
-- credential serves.
CREATE TABLE health (
  provider_id          TEXT    NOT NULL,
  key_id               TEXT    NOT NULL DEFAULT '',
  model                TEXT    NOT NULL DEFAULT '',
  cooling_until        INTEGER,
  backoff_level        INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  updated_at           INTEGER NOT NULL,
  PRIMARY KEY (provider_id, key_id, model)
) STRICT;

-- day is a UTC YYYY-MM-DD key on request start. Finalization is idempotent
-- recomputation, so a request spanning midnight lands once, in the day it began.
CREATE TABLE usage_daily (
  day         TEXT    NOT NULL,
  provider_id TEXT    NOT NULL,
  model       TEXT    NOT NULL,
  requests    INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  cost_micros INTEGER,
  PRIMARY KEY (day, provider_id, model)
) STRICT;

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;
