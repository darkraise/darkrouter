-- Which rendering an attempt used: the canonical-IR translation, or the
-- passthrough fast path. Master design §4 makes this a per-attempt decision, so
-- it belongs on the attempt rather than on the request: one request can forward
-- to its first candidate and translate to its second.
--
-- The default is 'ir' because every row written before this migration took it,
-- and so does every caller that has not been taught about the column.
ALTER TABLE request_attempts ADD COLUMN path TEXT NOT NULL DEFAULT 'ir';
