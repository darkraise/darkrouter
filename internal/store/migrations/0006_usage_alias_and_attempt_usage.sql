-- Alias is the routing unit, and usage_daily could not report it: the table
-- keys on (day, provider_id, model) and never carried the alias a request
-- resolved through. The operator console's routing-flow graph reads its whole
-- left-hand column from this dimension.
--
-- SQLite cannot widen a primary key in place, so the table is rebuilt. The
-- copy sets alias = '' for every existing row, which is what a request with no
-- alias already means: a direct model call, not a missing value.
CREATE TABLE usage_daily_new (
  day         TEXT    NOT NULL,
  provider_id TEXT    NOT NULL,
  model       TEXT    NOT NULL,
  alias       TEXT    NOT NULL DEFAULT '',
  requests    INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  cost_micros INTEGER,
  PRIMARY KEY (day, provider_id, model, alias)
) STRICT;

INSERT INTO usage_daily_new (day, provider_id, model, alias, requests, tokens_in, tokens_out, cost_micros)
SELECT day, provider_id, model, '', requests, tokens_in, tokens_out, cost_micros FROM usage_daily;

DROP TABLE usage_daily;
ALTER TABLE usage_daily_new RENAME TO usage_daily;

-- Tokens burned by an attempt that failed before commit never reached
-- usage_daily, so spend understated reality exactly when failover fired --
-- which is when an operator most wants the number. Defaults are 0 because
-- every row written before this migration has no usage to report, and
-- cost_micros stays nullable for the same reason it is nullable on requests:
-- unpriced is not free.
ALTER TABLE request_attempts ADD COLUMN tokens_in   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_attempts ADD COLUMN tokens_out  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_attempts ADD COLUMN cost_micros INTEGER;
