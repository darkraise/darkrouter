-- Per-client proxy credentials, replacing the single shared server.proxy_token
-- as the way a client authenticates. One secret for every client means
-- revoking one client rotates all of them.
--
-- hash is a SHA-256 of the token, not a password hash. The token is 256 bits
-- of randomness this process generates, so there is nothing to brute-force and
-- nothing to salt -- and a deliberately slow KDF on the proxy hot path would
-- be a self-inflicted denial of service. Indexing the hash is what keeps the
-- check a single lookup rather than a scan that hashes every row.
CREATE TABLE proxy_tokens (
  id           TEXT PRIMARY KEY,
  name         TEXT    NOT NULL,
  prefix       TEXT    NOT NULL,
  hash         TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER
) STRICT;

CREATE UNIQUE INDEX idx_proxy_tokens_hash ON proxy_tokens(hash);
