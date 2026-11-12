-- The IP intelligence lookup made when the purchase was scored: the provider
-- name, whether it answered in time, and the kinds of network it reported.
-- NULL means no lookup was made. It is JSON because reputation providers
-- report different attributes, and the scoring queries never filter on it.
ALTER TABLE decisions
    ADD COLUMN ip_intel jsonb CHECK (ip_intel IS NULL OR jsonb_typeof(ip_intel) = 'object');
