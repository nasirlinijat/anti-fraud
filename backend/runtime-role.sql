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
GRANT SELECT, INSERT, UPDATE ON simulation_runs TO antifraud_api;
GRANT SELECT ON purchases, decisions, feature_counts, rulesets, ruleset_rules,
    blocklist_entries, review_resolutions TO antifraud_api;
GRANT INSERT ON purchases, decisions, feature_counts, rulesets, ruleset_rules,
    blocklist_entries, review_resolutions, configuration_audit_events TO antifraud_api;
GRANT UPDATE (purchases, players) ON feature_counts TO antifraud_api;
GRANT UPDATE (is_active) ON rulesets TO antifraud_api;
GRANT DELETE ON blocklist_entries TO antifraud_api;
-- Passwords are set only when an account is created, so the service cannot
-- rewrite a password hash or extend an existing session.
GRANT SELECT, INSERT ON accounts, sessions TO antifraud_api;
GRANT UPDATE (disabled_at) ON accounts TO antifraud_api;
GRANT DELETE ON sessions TO antifraud_api;
-- Admins release open claims; a claim is never moved to another account.
GRANT SELECT, INSERT, DELETE ON review_claims TO antifraud_api;

COMMIT;

-- Evaluation credentials are separate from scoring credentials. Empty password
-- keeps the role disabled for installations that do not run simulations.
\getenv labels_password LABELS_DB_PASSWORD
BEGIN;
SELECT 'CREATE ROLE antifraud_labels' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='antifraud_labels')
\gexec
ALTER ROLE antifraud_labels NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
\if :{?labels_password}
SELECT length(:'labels_password') > 0 AS labels_enabled \gset
\if :labels_enabled
ALTER ROLE antifraud_labels LOGIN PASSWORD :'labels_password';
\endif
\endif
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM antifraud_labels;
GRANT USAGE ON SCHEMA public TO antifraud_labels;
GRANT SELECT, INSERT ON purchase_labels TO antifraud_labels;
GRANT UPDATE(rejection_reason) ON pending_purchase_labels TO antifraud_labels;
GRANT SELECT, INSERT, DELETE ON pending_purchase_labels TO antifraud_labels;
-- Full purchase reads are needed to verify pending snapshots; scoring writes remain forbidden.
GRANT SELECT ON purchases TO antifraud_labels;
GRANT SELECT(purchase_id,outcome) ON decisions TO antifraud_labels;
GRANT SELECT, UPDATE ON simulation_runs TO antifraud_labels;
COMMIT;
