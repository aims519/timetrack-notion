-- +goose Up
CREATE TABLE entries (
    id TEXT NOT NULL PRIMARY KEY,
    context_id TEXT NOT NULL REFERENCES contexts(id),
    start_utc TEXT NOT NULL,
    end_utc TEXT NULL,
    description TEXT NULL CHECK (description IS NULL OR length(description) <= 2000),
    task_page_id TEXT NULL,
    entry_page_id TEXT NULL,
    create_attempted_at TEXT NULL,
    sync_state TEXT NOT NULL DEFAULT 'pending' CHECK (sync_state IN ('pending', 'synced', 'failed', 'blocked')),
    sync_error TEXT NULL,
    updated_at TEXT NOT NULL,
    synced_at TEXT NULL,
    deleted_at TEXT NULL,
    CONSTRAINT end_after_start CHECK (end_utc IS NULL OR end_utc > start_utc)
) STRICT;

CREATE INDEX idx_entries_context_start ON entries(context_id, start_utc);

CREATE UNIQUE INDEX one_running ON entries ((1))
  WHERE end_utc IS NULL AND deleted_at IS NULL;
