-- Four providers shipped as keyless that are not.
--
-- Their presets declared auth_style 'optional', which puts a provider in the
-- console's No auth group and tells the operator that adding it is the whole of
-- the setup. A completion with no credential says otherwise — all four answer
-- 401, verified 2026-08-29 — so the release now ships them as 'bearer'.
--
-- A provider row copies its preset's style at creation and is authoritative
-- afterwards, which is deliberate: an operator who overrode the style is the
-- authority on how their own endpoint is reached. That also means a corrected
-- preset does not reach a provider somebody already added, and it would sit in
-- No auth failing every request until they noticed. This realigns exactly the
-- rows that hold the old copy.
--
-- Narrow on purpose. Only these four ids, and only where the stored style is
-- still the one the old preset wrote: a row an operator deliberately set to
-- something else is left alone, and a provider they had already given a key to
-- is already 'bearer' and matches nothing here.
UPDATE providers
   SET auth_style = 'bearer'
 WHERE auth_style = 'optional'
   AND preset IN ('hackclub', 'kilo-gateway', 'naga-ac', 'pollinations');
