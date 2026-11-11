-- Run as the PostgreSQL administrator after migrating the schema.
\getenv runtime_password RUNTIME_DB_PASSWORD
BEGIN;

SELECT 'CREATE ROLE antifraud_api'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifraud_api')
\gexec
ALTER ROLE antifraud_api LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS PASSWORD :'runtime_password';

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM antifraud_api;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM antifraud_api;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM antifraud_api;

GRANT USAGE ON SCHEMA public TO antifraud_api;
GRANT SELECT ON purchases, decisions, feature_counts, rulesets, ruleset_rules,
    blocklist_entries, review_resolutions TO antifraud_api;
GRANT INSERT ON purchases, decisions, feature_counts, rulesets, ruleset_rules,
    blocklist_entries, review_resolutions, configuration_audit_events TO antifraud_api;
GRANT UPDATE (purchases, players) ON feature_counts TO antifraud_api;
GRANT UPDATE (is_active) ON rulesets TO antifraud_api;
GRANT DELETE ON blocklist_entries TO antifraud_api;

COMMIT;
