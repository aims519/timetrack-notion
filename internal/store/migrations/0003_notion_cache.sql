-- +goose Up
CREATE TABLE tasks (
    context_id TEXT NOT NULL REFERENCES contexts(id),
    page_id TEXT NOT NULL, 
    name TEXT NOT NULL,
    project_page_id TEXT NULL,
    sort_order REAL NULL,
    done INTEGER NOT NULL CHECK (done IN (0,1)),
    last_edited_time TEXT NOT NULL,
    gone_at TEXT NULL,
    PRIMARY KEY (context_id, page_id)
) STRICT;

CREATE TABLE projects (
    context_id TEXT NOT NULL REFERENCES contexts(id),
    page_id TEXT NOT NULL,
    name TEXT NOT NULL,
    gone_at TEXT NULL,
    PRIMARY KEY (context_id, page_id)
) STRICT;

CREATE TABLE cache_state (
    context_id TEXT NOT NULL PRIMARY KEY REFERENCES contexts(id),
    tasks_refreshed_at TEXT NULL,
    tasks_refresh_failed_at TEXT NULL,
    tasks_full_refreshed_at TEXT NULL,
    projects_refreshed_at TEXT NULL
) STRICT;

INSERT INTO cache_state(context_id)
VALUES ('00000000-0000-0000-0000-000000000000');