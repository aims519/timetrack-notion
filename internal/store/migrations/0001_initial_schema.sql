-- +goose Up
CREATE TABLE contexts (
    id TEXT NOT NULL PRIMARY KEY,
    tasks_db_id TEXT NULL,
    projects_db_id TEXT NULL,
    entries_db_id TEXT NULL,
    property_map TEXT NULL,
    default_project_page_id TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

INSERT INTO contexts(id)
VALUES ('00000000-0000-0000-0000-000000000000');
