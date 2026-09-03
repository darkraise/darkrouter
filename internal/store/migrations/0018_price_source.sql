-- A price and the authority behind it are different facts, and the models row
-- recorded only the number.
--
-- capabilities_source has separated a read capability from a guessed one since
-- 0002; prices had no equivalent, so a figure a provider quoted itself and one
-- a third-party directory estimated read identically. Defaulting to 'inferred'
-- keeps an existing row honest: nothing yet knows where its price came from.
ALTER TABLE models ADD COLUMN price_source TEXT NOT NULL DEFAULT 'inferred';
