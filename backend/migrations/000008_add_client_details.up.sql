-- Client details for the v4 model features: operating system, browser and
-- screen resolution as the client reports them.
ALTER TABLE purchases
    ADD COLUMN client_os         text,
    ADD COLUMN client_browser    text,
    ADD COLUMN screen_resolution text;

-- Running totals per client OS, browser and screen, like email and device.
ALTER TABLE feature_counts DROP CONSTRAINT feature_counts_kind_check;
ALTER TABLE feature_counts ADD CONSTRAINT feature_counts_kind_check
    CHECK (kind IN ('all', 'email', 'device', 'os', 'browser', 'screen'));
