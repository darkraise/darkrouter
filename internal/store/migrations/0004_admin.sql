-- Phase 7 schema, per spec section 4.2.
--
-- Indexes only. Nothing is created, dropped or rewritten, so a failed run
-- leaves the phase 5 schema exactly as it was.
--
-- The admin request log is read newest-first and paginated by keyset on
-- (ts, id) rather than by offset: offset degrades as the table grows and skips
-- rows when new ones land mid-scroll. That ordering needs its own composite
-- index; idx_requests_ts covers only the leading column, so the tie-break on an
-- identical timestamp falls back to a scan.
CREATE INDEX idx_requests_keyset ON requests(ts DESC, id DESC);

-- The four filter columns spec section 4.2 names. A filtered page without these
-- is a full table scan wearing a LIMIT, which is the failure the keyset design
-- exists to avoid.
CREATE INDEX idx_requests_provider ON requests(final_provider_id);
CREATE INDEX idx_requests_model ON requests(final_model);
CREATE INDEX idx_requests_status ON requests(status);
CREATE INDEX idx_requests_surface ON requests(surface);
