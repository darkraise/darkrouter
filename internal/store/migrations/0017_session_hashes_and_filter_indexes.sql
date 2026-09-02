-- Session ids are now stored as SHA-256 digests of the cookie value. Existing
-- rows hold raw ids that would never match a digest lookup, so they are
-- dropped rather than rewritten: operators log in again once.
DELETE FROM sessions;

-- The request log's remaining filter columns, each with the keyset order
-- appended so a filtered page is one index walk rather than a filtered scan
-- followed by a sort. The provider index replaces idx_requests_provider,
-- which covered the filter column alone.
DROP INDEX idx_requests_provider;
CREATE INDEX idx_requests_provider_keyset ON requests(final_provider_id, ts DESC, id DESC);
CREATE INDEX idx_requests_alias_keyset    ON requests(resolved_alias, ts DESC, id DESC);
CREATE INDEX idx_requests_error_keyset    ON requests(error_code, ts DESC, id DESC);

-- The failover graph joins attempts on outcome and pairs them by provider.
CREATE INDEX idx_request_attempts_outcome_provider ON request_attempts(outcome, provider_id);
