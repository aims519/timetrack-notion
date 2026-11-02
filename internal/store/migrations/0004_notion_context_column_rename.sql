-- +goose Up
-- The Notion IDs stored for a context are data-source IDs, not database IDs
-- (DESIGN.md §4, §10), so name the columns after what they hold.
ALTER TABLE contexts RENAME COLUMN tasks_db_id TO tasks_data_source_id;
ALTER TABLE contexts RENAME COLUMN projects_db_id TO projects_data_source_id;
ALTER TABLE contexts RENAME COLUMN entries_db_id TO entries_data_source_id;
