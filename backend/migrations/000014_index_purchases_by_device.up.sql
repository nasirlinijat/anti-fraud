CREATE INDEX purchases_device_event_time ON purchases (device_id, event_time)
    INCLUDE (card_token) WHERE device_id IS NOT NULL;
