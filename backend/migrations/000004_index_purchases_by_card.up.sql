-- Serves the card velocity count: earlier purchases with the same card in the last hour.
CREATE INDEX purchases_card_event_time ON purchases (card_token, event_time);
