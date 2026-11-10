DELETE FROM feature_counts WHERE kind IN ('os', 'browser', 'screen');
ALTER TABLE feature_counts DROP CONSTRAINT feature_counts_kind_check;
ALTER TABLE feature_counts ADD CONSTRAINT feature_counts_kind_check
    CHECK (kind IN ('all', 'email', 'device'));

ALTER TABLE purchases
    DROP COLUMN screen_resolution,
    DROP COLUMN client_browser,
    DROP COLUMN client_os;
