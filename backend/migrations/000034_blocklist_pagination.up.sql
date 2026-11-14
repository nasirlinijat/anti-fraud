CREATE INDEX blocklist_entries_newest_idx
    ON blocklist_entries (created_at DESC, id DESC);
CREATE INDEX blocklist_entries_type_newest_idx
    ON blocklist_entries (entry_type, created_at DESC, id DESC);
