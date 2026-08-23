-- Phase 5 schema, per spec section 9.
--
-- Additive only: every column carries a non-null default, which is what SQLite
-- requires when extending a STRICT table and what keeps the rows already in the
-- table valid. Nothing is dropped or rebuilt, so a failed run leaves the phase
-- 2 schema exactly as it was.
--
-- surface_meta_json is one column rather than nine because no two surfaces
-- share a field: embeddings record an input count and a dimension count,
-- images a count and a size, audio a duration and a voice, rerank a document
-- count. Nine mostly-NULL columns would also mean a tenth migration for a
-- tenth surface. candidates_json is the precedent, and json_extract keeps the
-- column queryable for phase 7's trace view.
ALTER TABLE requests ADD COLUMN surface_meta_json TEXT NOT NULL DEFAULT '{}';

-- These two are real columns because they apply to every surface, and because
-- spec section 7 makes the byte count load-bearing: a provider that returns a
-- fast 200 and then truncates a binary body cannot be failed over and there is
-- no in-stream error vocabulary to warn the client, so this is the only place
-- the truncation is recorded. A field inside a JSON blob is not where an
-- operator finds that.
ALTER TABLE requests ADD COLUMN response_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE requests ADD COLUMN response_content_type TEXT NOT NULL DEFAULT '';
