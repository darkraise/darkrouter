-- requests counts only the attempt that served, so summing the column across
-- providers still equals the real request count. Without a separate attempts
-- column a provider that only ever failed would read as zero requests against
-- non-zero tokens, which looks like corruption rather than like failover.
ALTER TABLE usage_daily ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
