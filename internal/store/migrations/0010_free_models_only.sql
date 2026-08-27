-- Per-provider import filter. When set, a discovery sweep keeps only the
-- models it can show are free, which is the one thing an operator on a
-- free tier wants the catalogue to hold.
--
-- A column rather than the settings_json blob beside it: every other
-- provider property -- region, project, location -- is a column, and the
-- blob has been declared and unused since 0001.
ALTER TABLE providers ADD COLUMN free_models_only INTEGER NOT NULL DEFAULT 0;
