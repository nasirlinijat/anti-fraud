-- Bound included values so wide text can keep using the original index keys.
-- The issuer is stored twice, as the index expression and as an included value.
CREATE FUNCTION issuer_history_fits_index(issuer text, txn text, player text, region text, email text)
RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$
    SELECT 2::bigint * octet_length(coalesce(issuer, ''))
           + octet_length(txn) + octet_length(player)
           + octet_length(coalesce(region, '')) + octet_length(coalesce(email, '')) <= 2000
$$;

DROP INDEX purchases_issuer_event_time;

CREATE INDEX purchases_issuer_event_time ON purchases ((coalesce(card_issuer, '')), event_time, transaction_id)
    INCLUDE (card_issuer, player_id, billing_region, email_domain)
    WHERE issuer_history_fits_index(card_issuer, transaction_id, player_id, billing_region, email_domain);

CREATE INDEX purchases_issuer_event_time_wide ON purchases ((coalesce(card_issuer, '')), event_time, transaction_id)
    WHERE NOT issuer_history_fits_index(card_issuer, transaction_id, player_id, billing_region, email_domain);
