-- What a request was billed on, recorded beside the cost it was billed at.
--
-- The grade lives on the catalog row, but a catalog row is re-stamped whenever
-- a sync runs: deriving the grade at query time would report last week's spend
-- against a price that arrived yesterday. Stamping it here freezes the
-- authority behind a figure at the moment the figure was computed.
--
-- The empty default is "never recorded", not "guessed": every row written
-- before this column existed has an unknown authority, and reading unknown as
-- estimated would mark every historical total for as long as the log keeps it.
ALTER TABLE requests ADD COLUMN price_grade TEXT NOT NULL DEFAULT '';
ALTER TABLE request_attempts ADD COLUMN price_grade TEXT NOT NULL DEFAULT '';
