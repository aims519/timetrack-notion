# timetrack-notion — Design Notes

A small Go CLI for tracking time against tasks. It works fully offline, stores tracked times along with a read-only cache of associated Notion tasks/projects in a local SQLite database, and syncs completed entries to Notion.

Status: design draft, v1 scope. Binary name `tt` is a placeholder. Deferred features are in [DESIGN-LATER.md](DESIGN-LATER.md); open questions and past decisions are in §11.

## Contents

- [1. Scope](#1-scope)
- [2. Concepts](#2-concepts)
- [3. Commands](#3-commands)
  - [3.1 Timer](#31-timer)
  - [3.2 Entries](#32-entries)
  - [3.3 Sync](#33-sync)
  - [3.4 Setup and tasks](#34-setup-and-tasks)
- [4. Data model](#4-data-model)
- [5. Notion setup and `init`](#5-notion-setup-and-init)
- [6. Sync](#6-sync)
  - [6.1 What is pushed](#61-what-is-pushed)
  - [6.2 Idempotency](#62-idempotency)
  - [6.3 Failure handling](#63-failure-handling)
  - [6.4 Output and exit codes](#64-output-and-exit-codes)
  - [6.5 Sync lock and scheduled sync](#65-sync-lock-and-scheduled-sync)
- [7. Command errors vs sync failures](#7-command-errors-vs-sync-failures)
- [8. Agent-friendly interface](#8-agent-friendly-interface)
- [9. Secrets](#9-secrets)
- [10. Implementation notes](#10-implementation-notes)
- [11. Decision log and open questions](#11-decision-log-and-open-questions)

## 1. Scope

**In v1:**

- Start/stop a single timer, with or without a task; log time after the fact (`add`, backdated `start`/`stop`).
- Attach tasks to entries later; edit and delete entries.
- Choose a task by page ID, short ID or order number, or from an interactive picker.
- Inspect the running timer (`status`) and past entries (`list`).
- Time from the first run; `init` connects to Notion later without losing anything (§4).
- Push stopped, task-attached entries to Notion (`sync`) idempotently, with retries, one sync at a time.

**Not in v1:** pulling Notion edits back down (entries are push-only; tasks are a read-only cache); multiple users, reports, invoicing, a TUI or web UI, a daemon; syncing a running timer.

**Deferred** ([DESIGN-LATER.md](DESIGN-LATER.md)): automatic sync after changes; multiple contexts and the OS keychain; `tt config`; `HH:MM` meaning yesterday and full DST handling; task name matching; `sync --dry-run` and the schema check; `sync --strict`; the rollup offer in `init`; an option to indent JSON output.

**Stretch:** `tt import <toggl.csv>` (backfill Toggl history as unattached entries); `sync --push-unattached`; an opt-in semantic task matcher.

## 2. Concepts

| Term | Meaning |
|---|---|
| **Entry** | A time span: `start`, `end` (null while running), optional `description`, optional `task`. Identified by a locally generated UUID. |
| **Task** | A page in the Notion tasks database, identified by its page ID. Names and order numbers are display hints and not unique. |
| **Project** | A page in the Notion projects database. Tasks relate to it; it scopes task selection. |
| **Context** | The Notion configuration (databases, property mapping, default project), stored with the entries. v1 has exactly one. It is **local-only** until `init` **connects** it. |
| **Sync state** | Per entry: `pending`, `synced`, `failed` (temporary Notion error, retried next run, counted as pending by `status`) or `blocked` (permanent problem, needs fixing). A stopped entry with no task is **waiting**; this is derived, not stored. |

Only **one timer runs at a time**.

## 3. Commands

Global flags: `--output table|json` (default `table`), `--no-input` (never prompt), `--yes` (confirm destructive actions). Exit codes: §8.

### 3.1 Timer

| Command | Behaviour |
|---|---|
| `tt start [description] [-t, --task SELECTOR \| --pick] [--switch] [--at TIME \| --ago DURATION]` | Needs a description, a task, or both. Fails with `timer_running` if a timer is running, unless `--switch`, which stops it and starts the new one at the same instant in one transaction (with `--at`/`--ago`, that instant must be after the old entry's start). The start time is captured before any lookup or picker, so time spent choosing isn't lost; the task is resolved before the transaction, so a failed lookup or cancelled picker leaves the old timer running. |
| `tt stop [--at TIME \| --ago DURATION]` | Stops the running timer. Prints the closed entry. |
| `tt status` | Running entry, elapsed time, today's total, and pending / blocked / waiting counts. Warns if the timer has run over 12 hours, and says so if the context isn't connected. Always exits 0. |

**Time arguments.** `--at`, `--start`, `--end` take `HH:MM` or RFC 3339; `--ago`, `--duration` take durations (`10m`, `1h30m`). No time may be in the future and an end must follow its start, else `invalid_time` (exit 2). Overlapping entries are allowed, with an `overlap` warning.

`HH:MM` always means **today** in the local zone: a time not yet reached is `invalid_time`, so anything earlier (a timer left overnight, an entry across midnight) needs RFC 3339. On DST change days a non-existent or repeated `HH:MM` is also `invalid_time`, with a hint to pass an offset.

**Time zone.** Stored in UTC; JSON is always UTC. Input, table output and the `list` day/week boundaries use the system zone, or `TT_TIMEZONE` (an IANA name; unknown names are `usage`, exit 2). `--week` is the ISO week, Monday to Sunday.

**Task selector** (`--task` on `start`, `add`, `edit`; the argument of `attach`). The first rule that matches anything decides:

1. **Full page ID**, with or without dashes.
2. **Short ID**: a suffix of the page ID, at least 6 characters (pages created together share prefixes, not suffixes). Displayed as the shortest unique suffix in the cache.
3. **Order number** such as `4.1`, among the current project's open tasks.

IDs match the whole cache, done tasks included. Order numbers and the picker cover only the current project's open tasks; `--all` adds done tasks. Anything else is `not_found` (exit 3); there is no name matching in v1. Several matches prompt (interactive) or fail with `ambiguous_selector` (exit 3) listing candidates. Tasks marked gone (§4) never match.

`--pick` opens a picker over the current project's open tasks, sorted by Order, then name, unordered last. It is mutually exclusive with `--task` and a usage error without a terminal; `--task` always needs a value. **Cancelling the picker changes nothing** and exits 2 (`cancelled`).

The resolved task is always echoed (name and short ID), so a wrong match is seen at once. Scripts and agents should use full page IDs from `tt task list --output json`.

**Task refresh.** Resolving a selector refreshes the task cache if it is older than 10 minutes: incremental, blocking, capped at 2s. On timeout or error it uses the cache, says so, and skips blocking refreshes for a minute, so working offline stays fast.

In a local-only context there is no task cache: selectors and `--pick` fail with `not_initialised` (exit 5).

### 3.2 Entries

| Command | Behaviour |
|---|---|
| `tt list [--week \| --since/--until DATE] [--task S] [--project P] [--unattached] [--unsynced] [--limit N] [--wide]` | Today's entries by default, chronological. An entry belongs to the day it started. `--wide` stops truncation. |
| `tt attach [selector] [--entry ID]` | Links a task to the running entry, else the most recent one, or `--entry`. With no selector it opens the picker (so a selector is required without a terminal). Needs a connected context. |
| `tt edit <entry> [--description] [--start] [--end] [--task S \| --pick]` | Any change makes the entry `pending`, including a `blocked` one; a synced entry's Notion row is updated next sync. `--end` can't be set on a running entry (use `stop --at`). No detaching a task in v1. |
| `tt delete <entry> [--yes] [--dry-run]` | Removed outright only if no Notion create was ever attempted (`create_attempted_at` null); otherwise tombstoned, and the next sync archives the row. `--dry-run` says which, changing nothing. Deleting the running entry frees the timer. |
| `tt add [description] [-t S \| --pick] <span>` | Logs a finished entry without touching the timer. Span: `--start`+`--end`, `--start`+`--duration`, `--end`+`--duration`, or `--duration` or `--start` alone (ending now). Needs a description, a task, or both. |

Entries display their description, else the task name. Descriptions are at most 2000 characters (a Notion limit), else `usage`. An entry is selected by a unique UUID prefix of at least 7 characters.

### 3.3 Sync

`tt sync [--retry-blocked]`: see §6. Needs a connected context. Nothing syncs automatically in v1; run it by hand or on a schedule (§6.5).

### 3.4 Setup and tasks

| Command | Behaviour |
|---|---|
| `tt init [--tasks-db ID] [--projects-db ID] [--parent-page ID] [--project NAME]` | Connects the context to Notion (§5). Every prompt has a flag. Safe to re-run; also how the default project is changed. |
| `tt task list [--project NAME] [--all] [--limit N]` | Cached tasks with order, short ID and name; open tasks in the current project by default, `--all` adds done tasks. How scripts discover task IDs. |
| `tt task refresh [--full]` | Refreshes now, ignoring TTL and back-off. `--full` also detects deleted or archived tasks. |

## 4. Data model

One **SQLite database** (pure-Go driver) in the user data directory, overridden by `TT_DB`. There is no config file: the context lives in the same database as the entries, so they can't drift apart and one backup covers both. Tokens are never stored (§9).

Tables: `contexts`, `entries`, `tasks`, `projects`, `cache_state`, `schema_migrations`. Context-owned tables carry `context_id` (a foreign key to `contexts.id`), so adding contexts later needs no migration of them. Foreign keys are on; WAL mode with a busy timeout lets a command run while a sync does. **Every command commits locally before any network call, and no transaction spans one.** If the database is lost, `init` rebuilds the context from Notion; only unsynced entries are lost.

Each table is listed as **Column | Notes**. A `*` after a column name marks it **nullable**; every other column is `NOT NULL`. PK is primary key, FK is foreign key. Timestamps are UTC.

### `contexts`

Exactly one row in v1.

| Column | Notes |
|---|---|
| `id` | PK. UUID, never changed. |
| `tasks_db_id`* | Notion data-source ID of the tasks database. Null until `init`. |
| `projects_db_id`* | Notion data-source ID of the projects database. Null until `init`. |
| `entries_db_id`* | Notion data-source ID of the Time entries database. Null means **local-only**; there's no separate flag. |
| `property_map`* | JSON: the names of the task name, project, order and done properties. Null until `init`. |
| `default_project_page_id`* | Null if no default project. |
| `created_at` | |

The first migration inserts the single row, local-only. Each migration re-checks the schema version under `BEGIN IMMEDIATE`, so concurrent first runs create one row.

While local-only, `start`, `stop`, `add`, `edit`, `delete`, `list` and `status` work with descriptions; `--task`, `--pick`, `attach`, `task` and `sync` fail with `not_initialised` (exit 5). `init` keeps the context's ID, so earlier entries simply remain, waiting to be attached.

### `entries`

| Column | Notes |
|---|---|
| `id` | PK. UUID; sent to Notion as `Local ID`, the idempotency key. |
| `context_id` | FK to `contexts.id`. |
| `start_utc` | Full precision; truncated to the minute only when sent (§5). |
| `end_utc`* | Null while running. Full precision, as for `start_utc`. |
| `description`* | At most 2000 characters. |
| `task_page_id`* | The task this time is for; null while unattached. Deliberately not an FK: a task can vanish from Notion and its entries must survive (they become `blocked`). |
| `entry_page_id`* | The entry's own Notion row; null until first pushed. |
| `create_attempted_at`* | Committed just before the first create request. Set with no `entry_page_id` means a row may exist unrecorded: sync looks it up by `Local ID` (§6.2), and `delete` tombstones. |
| `sync_state` | `pending`, `synced`, `failed` or `blocked` (§2). Every change sets `pending`. |
| `sync_error`* | The last sync error. |
| `updated_at` | Bumped by every change. Sync marks an entry `synced` only if this hasn't changed since it read the entry (§6.3). |
| `synced_at`* | When the last push succeeded. |
| `deleted_at`* | Tombstone. |

One running timer is enforced by a partial unique index on a constant (indexing a nullable column wouldn't work, as SQLite treats NULLs as distinct):

```sql
CREATE UNIQUE INDEX one_running ON entries ((1))
  WHERE end_utc IS NULL AND deleted_at IS NULL;
```

### `tasks`

A read-only cache of the tasks database.

| Column | Notes |
|---|---|
| `context_id` | PK with `page_id`. FK to `contexts.id`. |
| `page_id` | PK with `context_id`. Notion page ID, stored without dashes. |
| `name` | |
| `project_page_id`* | Null if the task has no project. |
| `sort_order`* | The Order property as a real number (such as `4.1`). Not unique. |
| `done` | Boolean. |
| `last_edited_time` | From Notion; drives incremental refresh. |
| `gone_at`* | Set when a full refresh no longer finds the task; cleared if it reappears. |

Tasks with `gone_at` set keep their names for display but are excluded from matching, the picker and `task list`. Short IDs are derived from the cached page IDs, not stored.

### `projects`

A read-only cache of the projects database.

| Column | Notes |
|---|---|
| `context_id` | PK with `page_id`. FK to `contexts.id`. |
| `page_id` | PK with `context_id`. |
| `name` | |
| `gone_at`* | As for `tasks`. |

### `cache_state`

One row per context. Its timestamps are null until the first refresh of each kind.

| Column | Notes |
|---|---|
| `context_id` | PK. FK to `contexts.id`. |
| `tasks_refreshed_at`* | Last successful refresh. Incremental refreshes ask for edits since then, minus a small overlap for clock skew. |
| `tasks_refresh_failed_at`* | Last failed or timed-out refresh; drives the one-minute back-off (§3.1). |
| `tasks_full_refreshed_at`* | Last full refresh. |
| `projects_refreshed_at`* | Last projects refresh. |

### `schema_migrations`

| Column | Notes |
|---|---|
| `version` | PK. |
| `applied_at` | |

Migrations are forward-only, run at startup, one transaction each. A database from a newer binary is never touched (`database_too_new`, exit 5); an unreadable one is `database_unusable` (exit 5).

## 5. Notion setup and `init`

Needs a Notion internal integration shared on a parent page above the projects and tasks databases (an integration sees only pages shared with it, and a relation doesn't grant access).

```text
Parent page (shared with the integration)
├── Projects       (existing database)
├── Tasks          (existing database)
└── Time entries   (created by init)
```

`init` checks before creating anything, so re-running reuses what exists:

1. Validate `NOTION_TOKEN` with a "who am I" call; if unset, `no_token`.
2. Find the tasks and projects databases; if none are visible, say which page to share.
3. Map the task properties (name, project, order, done), detecting defaults.
4. Create the **Time entries** database under the parent page:

   | Property | Type |
   |---|---|
   | Title | title (description, else task name, else "(no description)") |
   | Task | relation to the tasks database (two-way) |
   | Start, End | date with time |
   | Description | text |
   | Local ID | text |
   | Minutes | formula: whole minutes from Start to End |

5. Choose a default project, store the Notion IDs in one transaction, and do a full task refresh. Report how many earlier entries are waiting and suggest `tt attach --entry`.
6. Print the steps for adding a `Tracked (min)` rollup to the tasks database by hand. Nothing depends on it.

Notion computes all totals; the tool never writes a duration, and never touches existing hand-maintained fields.

**Minutes.** Notion drops seconds from dates (verified in the spike), so the CLI truncates `start` and `end` to the minute itself before sending, and an entry's `minutes` is the difference of the truncated values. That makes the CLI and Notion's formula agree exactly. The trade-off is that an entry's `minutes` can be up to a minute more or less than its real length (`seconds`): 09:01:50 to 09:02:10 is 20 seconds but counts as 1 minute, and 09:01:00 to 09:01:59 is 59 seconds but counts as 0. The overs and unders cancel out across many entries, so totals don't drift. (Rounding each entry's length down instead would lose about 30 seconds per entry, every time.) An entry that counts as 0 minutes is still kept. Totals in `status` and `list` sum per-entry minutes, so they match the Notion rollup.

## 6. Sync

### 6.1 What is pushed

Stopped entries with a task attached, even if that task is marked done in Notion. Running and unattached entries are held back (`waiting`). Tombstoned entries archive their row; one with no stored page ID is looked up by `Local ID` first and simply dropped if no row exists. Archiving an already-archived row counts as success (to confirm, question 19).

`Start` and `End` are sent as UTC instants truncated to the minute, **never with `time_zone`**: Notion keeps the wall-clock digits and swaps in the named zone's offset, which changes the instant.

### 6.2 Idempotency

Before creating a row for an entry with no `entry_page_id`, query by `Local ID`, on every attempt, not just the first (a timed-out create may have succeeded). If found, adopt its page ID and update; otherwise set `create_attempted_at`, create, and record the page ID at once. Entries with a page ID are updated directly. This makes retries after a crash, timeout or cancel safe, given a single writer (the sync lock, §6.5) and a query that sees just-created rows (confirmed in the spike, question 17). Trashed rows don't appear in the query; §6.3 handles them.

### 6.3 Failure handling

**Network-level** failures (no response) never mark an entry `failed`: it stays `pending`. **HTTP-level** temporary errors (429, 5xx) mark it `failed` after enough attempts.

| Failure | Handling |
|---|---|
| First connection of the run fails | Abort at once: `offline`, exit 6. |
| Network-level, mid-run | Retry with backoff and jitter. 3 consecutive failures (counted across entries, reset by any success) stop the run: remaining entries stay `pending`, exit 6 with synced/pending counts. |
| 429, 5xx | Backoff with jitter, honouring `Retry-After`; 5 attempts, then `failed` (`rate_limited` / `server_error`). A 429 pauses the whole run. Stay under ~3 req/s. |
| 401 | Abort: token invalid. |
| 403/404 on the entries database | Abort: say which page to re-share. |
| 404 on an entry's task | `blocked` (`task_not_found`); continue. |
| Entry's own row deleted or trashed | `blocked` (`row_missing`), never silently recreated. Updating a trashed page returns 400, not 404, so on a 400 for an entry with a page ID, retrieve the page: trashed is `row_missing`, otherwise `bad_request`. |
| Other 400 | `blocked` (`bad_request`); continue. |

`--retry-blocked` makes blocked entries pending for this run. For `row_missing` it restores and updates the row in one request (`in_trash: false` plus the properties) by stored page ID; if the page is gone for good, it falls back to the `Local ID` lookup and creates. Otherwise blocked entries wait until edited or re-attached.

Ctrl-C lets the in-flight request finish, saves state and exits cleanly.

**Edits during a sync** aren't lost: an entry is marked `synced` only if its `updated_at` is unchanged since the sync read it; otherwise it stays `pending`.

### 6.4 Output and exit codes

```text
$ tt sync
synced   Local store for time entries   1h 12m
synced   Timer commands                 45m
synced   Code review with Claude        20m
failed   Add retry logic                429 rate limited, will retry next run
failed   Task cache refresh             503 server error, will retry next run
blocked  Old task                       task not found in Notion; run 'tt attach --entry a1b2c3d'
waiting  2 entries without a task

synced 3 · failed 2 · blocked 1 · waiting 2
2 earlier entries still blocked (see 'tt status')
```

One line per entry pushed or not pushed (waiting entries grouped), a summary, and a reminder of entries still blocked from earlier runs.

**Exit 0** if nothing failed or became blocked *in this run*; **exit 1** if something did (including a retried entry that blocks again). Waiting entries and old blocked entries don't count, so a scheduled sync reports a problem once rather than looking broken until it's fixed; `still_blocked` and `tt status` keep it visible. Other codes (4, 5, 6) as in §8.

The JSON form of the same result is the `sync` document in §8.

### 6.5 Sync lock and scheduled sync

Every sync takes an advisory file lock in the data directory before reading any entry and holds it for the run (the OS releases it if the process dies). If it's held, `tt sync` fails with `sync_busy` (exit 4). Only sync takes the lock.

A cron job or launchd agent running `tt sync --no-input` works as is: it exits 1 once per new problem, and an overlap with a manual sync just exits 4. The tool doesn't install the job.

## 7. Command errors vs sync failures

Command errors are local, immediate and never retried: nothing running, a timer already running, no description or task, an impossible time, a selector that matches nothing or too much, a cancelled picker, a context not yet connected, an unusable database. Notion being unreachable is never an error for `start`, `stop`, `status` or `list`; only `init` and `sync` need it. Codes: §8.

## 8. Agent-friendly interface

Table output is for people and may change. **JSON output (`--output json`) is the contract.**

### Conventions

- One JSON document on stdout, with a top-level `schema_version`, printed compactly on a single line (pipe through `jq` to read it). The examples below are indented for reading only.
- Mutating commands return the resulting entry under `entry`, so no follow-up `list` is needed.
- Each command has a fixed set of keys; inapplicable values are `null`, never missing.
- Timestamps are RFC 3339 UTC; durations are integer seconds.
- A plural key holds an array (`entries`, `tasks`); a singular key holds one object. Collections are always wrapped in an object, never a bare array.
- `warnings` is an array in every success document, `[]` when empty. Each warning is `{"code", "message", "details"}`; codes are `overlap` (`details.entry_ids`), `stale_timer` and `task_cache_stale`.
- `list` and `task list` accept `--limit N` and include `truncated`: `true` when more rows matched than were returned, so a script knows it isn't seeing everything. Totals still cover every matching row: `list`'s `total_minutes` is the same with or without `--limit`.
- Order is part of the contract: `list` chronological; `task list` by Order, then name, unordered last.

### Entry object

```json
{
  "id": "a1b2c3d4-…",
  "start": "2026-10-12T09:00:00Z",
  "end": "2026-10-12T10:12:00Z",
  "seconds": 4320,
  "minutes": 72,
  "description": null,
  "task": {
    "page_id": "…",
    "short_id": "c8d9e0",
    "name": "…"
  },
  "sync_state": "pending",
  "sync_error": null,
  "deleted": false
}
```

`task` is `null` when unattached; `end`, `seconds` and `minutes` are `null` while running. `minutes` follows §5.

### Task item

Used in `task list` and in `ambiguous_selector` candidates.

```json
{
  "page_id": "3f63b23e…",
  "short_id": "837922",
  "order": 2.3,
  "name": "Write the sync engine",
  "project": {
    "page_id": "3e73b23e…",
    "name": "Time tracker"
  },
  "done": false
}
```

### `sync` document

`results` has one object per entry this run touched: `entry_id`, `state` (`synced`, `failed` or `blocked`), and `error` (`{"code", "message"}`, `null` when synced). Waiting entries aren't touched, so they appear only in the `summary` count. `still_blocked` counts entries blocked by earlier runs that this run left alone. An entry's own outcome goes only in its `error`; `warnings` holds notices about the run as a whole, such as a pause for rate limiting.

```json
{
  "schema_version": 1,
  "results": [
    {"entry_id": "a1b2c3d4-…", "state": "synced", "error": null},
    {"entry_id": "b2c3d4e5-…", "state": "synced", "error": null},
    {"entry_id": "c3d4e5f6-…", "state": "synced", "error": null},
    {"entry_id": "d4e5f6a7-…", "state": "failed", "error": {"code": "rate_limited", "message": "429, 5 attempts, will retry next run"}},
    {"entry_id": "e5f6a7b8-…", "state": "failed", "error": {"code": "server_error", "message": "503, 5 attempts, will retry next run"}},
    {"entry_id": "f6a7b8c9-…", "state": "blocked", "error": {"code": "task_not_found", "message": "task not found in Notion"}}
  ],
  "summary": {
    "synced": 3,
    "failed": 2,
    "blocked": 1,
    "waiting": 2,
    "still_blocked": 2
  },
  "warnings": []
}
```

This is the run shown in §6.4, and it exits 1. Per-entry codes are `rate_limited` and `server_error` for `failed`, and `task_not_found`, `row_missing` and `bad_request` for `blocked`. They describe one entry's outcome, never a failed command. A run that ends in exit 6 is a failed command and writes an error document instead (Errors, below).

### Documents by command

| Command | Keys (besides `schema_version`) |
|---|---|
| `start` | `entry`, `stopped` (the entry `--switch` stopped, else `null`), `warnings` |
| `stop`, `add`, `attach`, `edit` | `entry`, `warnings` |
| `delete` | `dry_run`, `action` (`remove` or `tombstone`), `entry`, `removed` (true if removed outright, `null` with `--dry-run`), `warnings` |
| `status` | `running` (entry or `null`), `elapsed_seconds`, `today` (`entries` count, `minutes`), `counts` (`pending`, `blocked`, `waiting`), `connected`, `warnings` |
| `list` | `entries`, `total_minutes`, `truncated`, `warnings` |
| `task list` | `tasks`, `truncated`, `warnings` |
| `sync` | `results`, `summary`, `warnings` (`sync` document, above) |
| `init`, `task refresh` | fixed when built |

In `status` and `list`, today's minutes include the running entry's elapsed minutes (same clock reading as `elapsed_seconds`), so the two agree. A timer left running from yesterday isn't part of today; `stale_timer` flags it.

Example, `tt status` before `init`:

```json
{
  "schema_version": 1,
  "running": null,
  "elapsed_seconds": null,
  "today": {
    "entries": 0,
    "minutes": 0
  },
  "counts": {
    "pending": 0,
    "blocked": 0,
    "waiting": 0
  },
  "connected": false,
  "warnings": []
}
```

### Streams, prompts and errors

**Streams.** Data to stdout; diagnostics, warnings and errors to stderr. No colour or spinners when stdout isn't a terminal or `NO_COLOR` is set.

**No prompts** unless stdin and stdout are terminals and `--no-input` isn't set. Otherwise nothing blocks: `--pick` is a usage error, an ambiguous selector fails with its candidates, `delete` needs `--yes`, and every `init` prompt has a flag.

**Errors.** A failed command (exit 2 or above) writes `{"schema_version": 1, "error": {"code", "message", "details"}}` to stderr and nothing to stdout. `code` is stable; `message` isn't. `sync` with exit 1 hasn't failed: it writes its normal document to stdout.

| Exit | Meaning | Codes |
|---|---|---|
| 0 | Success | |
| 1 | `sync` finished, but an entry failed or became blocked in this run | `sync_partial` (not an error document) |
| 2 | Usage | `usage`, `invalid_time`, `confirmation_required`, `cancelled` |
| 3 | Not found or ambiguous | `not_found`, `ambiguous_selector` |
| 4 | State conflict | `timer_running`, `no_timer_running`, `sync_busy` |
| 5 | Config, auth, local store, or not connected | `not_initialised`, `no_token`, `access_denied`, `database_too_new`, `database_unusable` |
| 6 | Notion unreachable (`sync` only) | `offline` |
| 10 | Internal error (no stack traces) | `internal` |

**`details`** is `null` except for:

| Code | `details` |
|---|---|
| `timer_running` | `{"entry": <entry>}` |
| `offline` | `{"synced": n, "pending": n}` |
| `ambiguous_selector` | `{"selector": "...", "candidates": [<task items>]}` |
| `not_found` | `{"selector": "..."}` |
| `invalid_time` | `{"input": "..."}` |
| `confirmation_required` | `{"flag": "--yes"}` |
| `database_too_new` | `{"database_version": n, "supported_version": n}` |
| `database_unusable` | `{"path": "..."}` |

Every command has `--help` with examples.

## 9. Secrets

The Notion token comes only from `NOTION_TOKEN`. It is never a flag (shell history) and never stored in the database or any file. If unset, commands that need it fail with `no_token` (exit 5). Keychain storage is deferred.

## 10. Implementation notes

### Constraints

- Single static Go binary; pure-Go SQLite driver (no cgo); embedded timezone database (`time/tzdata`) so `TT_TIMEZONE` works in minimal containers.
- No optional-value flags (pflag can't take the next argument as their value), so either Cobra or stdlib `flag` works.
- Notion API pinned to `Notion-Version: 2025-09-03`, using data-source IDs and `POST /v1/data_sources/{id}/query` (the old database query endpoint returns 400).
- Behind interfaces, for testing: the Notion client, and a `Clock` (no `time.Now` directly).
- Classify every HTTP error as network-level or HTTP-level in one place; keep the consecutive-failure counter in the sync engine.
- Truncate to the minute in one place: the function that builds the Notion payload.
- `time.Date` doesn't flag non-existent or repeated local times; detect them by converting back and comparing.
- Have the sync engine return a per-entry outcome and count untouched blocked entries separately, so the exit code is a pure function of the counts.

### Tests that must exist

- Two running rows: the second insert fails.
- First run: timing before `init`, then `init` connecting with entries intact.
- Drop mid-create (fake client creates the row, then fails): the next run finds it by `Local ID` and doesn't duplicate.
- Minute truncation: 09:01:50 → 09:01:00; whole minutes unchanged; 31 s, 59 s, 90 s spans; 09:01:59 → 09:02:00 is 1 minute. The fake client rejects any payload with `time_zone`.
- Archived rows: a 400 on a trashed page is `row_missing`; `--retry-blocked` restores and updates in one request; archiving an archived row succeeds.
- `HH:MM` on both DST change days.
- A second `tt sync` while one holds the lock exits 4 without reading entries.

## 11. Decision log and open questions

Numbers are stable IDs (Notion task comments cite them). 10, 11 and 13 moved to [DESIGN-LATER.md](DESIGN-LATER.md).

### Open questions

- **2.** A selector matching nothing is an error even if the task was created seconds ago and the refresh timed out. Revisit if annoying.
- **6.** How often to run a full task refresh, to notice deleted tasks.
- **7.** What bumps `schema_version`, and whether to publish JSON schemas per command.
- **8.** Short IDs are relative to the cache and may lengthen; scripts should store full page IDs.
- **9.** Overlaps are allowed with a warning; rejecting them without `--force` would stop double counting at the cost of friction. Revisit after real use.
- **19.** Probe still needed: archiving an already-archived page, and retrieving a trashed page reporting `in_trash` (§6.1, §6.3).

### Resolved

- **1.** `init` can create the `Minutes` formula and a rollup via the API (spike, 13 Oct). v1 creates the formula and prints rollup steps.
- **3.** Stale-timer threshold fixed at 12h in v1.
- **4.** Notion truncates dates to the minute, so the CLI truncates first and derives `minutes` from the truncated ends (spike). Rejected: the CLI writing minutes (Notion should compute totals) and nearest-minute rounding (the two would disagree). Never send `time_zone`.
- **5.** UTC storage; system zone or `TT_TIMEZONE`; `HH:MM` is today only. Still to test: `list` boundaries on DST days.
- **12.** `--task` always takes a value and the picker is a separate `--pick`, because pflag optional values don't consume the next argument (`--task 4.1` would make `4.1` the description).
- **14.** The first migration creates the context local-only; `init` connects it in place.
- **15.** `sync` exits 1 only for problems new in that run, so scheduled syncs don't stay red.
- **16.** The context lives in SQLite with the entries; no config file.
- **17.** Query-after-create is consistent (40 creates, no misses or duplicates in the spike), so there's no post-sync duplicate check. Editing a trashed page returns 400; trashed pages don't appear in `Local ID` queries.
- **18.** Network failures are counted per attempt; 3 in a row stop the run with entries left `pending` (exit 6); `failed` is only for persistent 429/5xx.
