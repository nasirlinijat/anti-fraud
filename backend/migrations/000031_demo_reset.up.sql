BEGIN;
-- Bind the helper to the migration's schema, including isolated test schemas.
DO $migration$
DECLARE target text := current_schema();
BEGIN
    EXECUTE format($definition$
        CREATE FUNCTION %1$I.reset_demo_data() RETURNS void
        LANGUAGE plpgsql SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp
        AS $body$
        BEGIN
            TRUNCATE TABLE %1$I.review_claims, %1$I.review_resolutions,
                %1$I.purchase_labels, %1$I.pending_purchase_labels,
                %1$I.chargebacks, %1$I.decisions, %1$I.purchases,
                %1$I.feature_counts, %1$I.blocklist_entries,
                %1$I.account_events, %1$I.simulation_runs
                CONTINUE IDENTITY RESTRICT;
        END;
        $body$;
    $definition$, target);
    EXECUTE format('REVOKE ALL ON FUNCTION %I.reset_demo_data() FROM PUBLIC', target);
END;
$migration$;
COMMIT;
