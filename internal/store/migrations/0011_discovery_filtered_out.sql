-- How many models the last sweep dropped before recording it.
--
-- Only the free-models filter drops anything today. Without the count, a
-- provider whose every model is paid looks identical to one discovery has
-- never visited: both hold zero models, and the health view -- which groups
-- over the models table -- emits no row for either.
ALTER TABLE provider_discovery ADD COLUMN filtered_out INTEGER NOT NULL DEFAULT 0;
