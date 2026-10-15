# timetrack-notion

[![CI](https://github.com/aims519/timetrack-notion/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aims519/timetrack-notion/actions/workflows/ci.yml)
[![CodeQL](https://github.com/aims519/timetrack-notion/actions/workflows/dynamic/github-code-scanning/codeql/badge.svg?branch=main)](https://github.com/aims519/timetrack-notion/security/code-scanning)
[![Dependabot](https://github.com/aims519/timetrack-notion/actions/workflows/dynamic/dependabot/dependabot-updates/badge.svg?branch=main)](https://github.com/aims519/timetrack-notion/network/updates)

A small Go command-line tool for tracking time against tasks. It works fully offline, with all time entries saved locally first in a SQLite database, and syncs finished entries to Notion.

## Status

Design draft. There is no working code yet, and nothing here is installable. The binary name `tt` is a placeholder.

## What's planned

- Start and stop a timer, with or without a task
- Log time after the fact, including backdated starts and stops
- Work from the first run, before any Notion setup, and connect to Notion later without losing anything already recorded
- Push finished entries to Notion safely, so a retry never creates duplicates
- Give clean JSON output, so scripts and AI agents can drive it

## Design

The full design is in [docs/DESIGN.md](docs/DESIGN.md), which is the source of truth for how this should behave. Features deferred from the first version, such as automatic sync and multiple Notion workspaces, are in [docs/DESIGN-LATER.md](docs/DESIGN-LATER.md).

## Licence

MIT. See [LICENSE](LICENSE).
