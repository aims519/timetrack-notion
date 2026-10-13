# timetrack-notion

A small Go command-line tool for tracking time against tasks. It works fully offline, with all time entries saved locally first in a SQLite database, and syncs finished entries to Notion.

## Status

Design draft. There is no working code yet, and nothing here is installable. The binary name `tt` is a placeholder.

## What's planned

- Start and stop a timer, with or without a task
- Log time after the fact, including backdated starts and stops
- Work from the first run, before any Notion setup, and connect to Notion later without losing anything already recorded
- Push finished entries to Notion safely, so a retry never creates duplicates
- Keep separate contexts for different Notion workspaces
- Give clean JSON output, so scripts and AI agents can drive it

## Design

The full design is in [docs/DESIGN.md](docs/DESIGN.md), which is the source of truth for how this should behave.

## Licence

MIT. See [LICENSE](LICENSE).
