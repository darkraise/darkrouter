-- Phase 6 schema, per spec sections 5.1 and 6.
--
-- models is rebuilt rather than altered. Two of its phase 2 defaults are wrong
-- and SQLite cannot change a default in place:
--
--   state defaulted to 'active', which is not in spec 5.1's vocabulary
--   (live | stale | removed_upstream). internal/provider filters on this
--   column, so a row keeping the old value silently stops being routable.
--
--   surfaces defaulted to '["chat"]', which ir.ParseSurface rejects — the
--   surface is spelled 'llm'. Nothing read the column before phase 6, which is
--   why the mismatch survived unnoticed.
--
-- Nothing references models (model_overrides references providers), so the
-- rebuild has no dependents to fix up.

CREATE TABLE models_new (
  provider_id                      TEXT    NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model_id                         TEXT    NOT NULL,
  publisher                        TEXT    NOT NULL DEFAULT '',
  surfaces                         TEXT    NOT NULL DEFAULT '["llm"]',
  capabilities                     TEXT    NOT NULL DEFAULT '{}',
  capabilities_source              TEXT    NOT NULL DEFAULT 'inferred',
  context_window                   INTEGER,
  max_output_tokens                INTEGER,
  input_price_micros_per_mtok      INTEGER,
  output_price_micros_per_mtok     INTEGER,
  cache_read_price_micros_per_mtok INTEGER,
  discovered_at                    INTEGER,
  state                            TEXT    NOT NULL DEFAULT 'live',

  -- missing_streak counts consecutive *successful* listings that omitted this
  -- model. A failed probe never touches it: spec 5.1 makes that asymmetry the
  -- whole point, because a provider timing out must not empty its catalog.
  missing_streak                   INTEGER NOT NULL DEFAULT 0,
  last_seen_at                     INTEGER,

  PRIMARY KEY (provider_id, model_id)
) STRICT;

INSERT INTO models_new (
  provider_id, model_id, publisher, surfaces, capabilities, capabilities_source,
  context_window, max_output_tokens, input_price_micros_per_mtok,
  output_price_micros_per_mtok, cache_read_price_micros_per_mtok,
  discovered_at, state
)
SELECT provider_id, model_id, publisher,
       CASE WHEN surfaces = '["chat"]' THEN '["llm"]' ELSE surfaces END,
       capabilities, capabilities_source,
       context_window, max_output_tokens, input_price_micros_per_mtok,
       output_price_micros_per_mtok, cache_read_price_micros_per_mtok,
       discovered_at,
       CASE WHEN state = 'active' THEN 'live' ELSE state END
  FROM models;

DROP TABLE models;
ALTER TABLE models_new RENAME TO models;

CREATE INDEX idx_models_state ON models(state);

-- Per-provider discovery bookkeeping. Separate from providers because it is
-- worker state overwritten on every tick, and mixing it into the row an
-- operator edits invites a write conflict on every save.
CREATE TABLE provider_discovery (
  provider_id          TEXT    PRIMARY KEY REFERENCES providers(id) ON DELETE CASCADE,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_attempt_at      INTEGER,
  last_success_at      INTEGER,
  last_error           TEXT    NOT NULL DEFAULT ''
) STRICT;
