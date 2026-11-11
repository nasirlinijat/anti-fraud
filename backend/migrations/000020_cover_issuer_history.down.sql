DROP INDEX purchases_issuer_event_time_wide;
DROP INDEX purchases_issuer_event_time;
DROP FUNCTION issuer_history_fits_index(text, text, text, text, text);

CREATE INDEX purchases_issuer_event_time ON purchases ((coalesce(card_issuer, '')), event_time, transaction_id);
