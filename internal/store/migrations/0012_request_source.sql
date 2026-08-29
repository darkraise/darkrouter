-- Where a request came from, so console traffic can be told from a client's.
--
-- The provider test drawer and the playground send real requests through the
-- real executor, which is what makes them worth trusting — and it also means
-- they land in the same log as production traffic. Without this column an
-- operator reading a provider's log cannot tell the two apart, and the drawer
-- cannot show only its own.
--
-- 'proxy' is the default because every row written before this migration came
-- through the proxy listener: backfilling them as anything else would invent
-- a fact about history.
ALTER TABLE requests ADD COLUMN source TEXT NOT NULL DEFAULT 'proxy';

-- The drawer's query is (source, provider, newest first).
CREATE INDEX idx_requests_source_ts ON requests(source, ts);
