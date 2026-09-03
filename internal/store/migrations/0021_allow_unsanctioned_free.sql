-- An `avoid` terms verdict means access the vendor has not sanctioned. Those
-- models stay catalogued and visible, but nothing routes to them automatically
-- until the operator says so for that provider. Default 0: the opt-in is the
-- operator's, and a migration must not grant it on their behalf.
ALTER TABLE providers ADD COLUMN allow_unsanctioned_free INTEGER NOT NULL DEFAULT 0;
