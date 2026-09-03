-- A price of zero and no price at all are different facts, and the column
-- layout could not tell them apart.
--
-- nullableInt64 writes NULL for a zero rate, so a genuinely free model stored
-- four NULLs and read back through the old `inPrice.Valid || outPrice.Valid`
-- derivation as unpriced. Backfilled from that same derivation, which is
-- correct for every row written before this column existed: nothing could
-- store a known zero, so a NULL rate really did mean "never found out".
ALTER TABLE models ADD COLUMN price_known INTEGER NOT NULL DEFAULT 0;

UPDATE models SET price_known = 1
 WHERE input_price_micros_per_mtok IS NOT NULL
    OR output_price_micros_per_mtok IS NOT NULL;
