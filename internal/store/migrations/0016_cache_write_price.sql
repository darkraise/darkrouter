-- Cache writes are billed at their own rate, and the models row had nowhere to
-- put it.
--
-- The metadata sync parsed cache_write out of models.dev and then dropped it on
-- the way to SQLite, so every cached write priced at zero. The column is
-- nullable like its three siblings, because an absent price and a zero price
-- are different facts: PriceKnown is derived from input and output alone, and
-- a provider that publishes no cache-write rate must not read as free.
ALTER TABLE models ADD COLUMN cache_write_price_micros_per_mtok INTEGER;
