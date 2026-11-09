-- Serves the dashboard statistics, which count decisions made in a recent period.
CREATE INDEX decisions_decided_at ON decisions (decided_at);
