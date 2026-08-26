-- Aliases move out of darkrouter.yaml so the console can edit them, following
-- the rule providers: already follows. The YAML block is imported once on the
-- first run and the database is authoritative thereafter.
--
-- seq is explicit rather than implied by insertion order: the chain order is
-- the fallback order, and a row set that shuffled it would reorder failover
-- without changing any value an operator can see.
CREATE TABLE aliases (
  name   TEXT    NOT NULL,
  seq    INTEGER NOT NULL,
  target TEXT    NOT NULL,
  PRIMARY KEY (name, seq)
) STRICT;
