# timetrack-notion — Design Notes

A small Go CLI for tracking time against tasks. It works fully offline, stores everything in a local SQLite database, and syncs completed entries to Notion.

Status: design draft, v1 scope. Binary name `tt` is a placeholder. Revised after a design review on 12 Oct 2026, and again on 13 Oct 2026 with the results of the Notion spike (open questions 1, 4 and 17). No design decisions are open; one small follow-up probe remains (open question 19).

## 1. Scope

**In v1**

- Start/stop a single timer, with or without a task.
- Log time after the fact: `add` for a finished entry, and backdated `start` / `stop` via `--at` / `--ago`.
- Attach tasks to entries after the fact; edit and delete entries.
- Inspect the running timer (`status`) and past entries (`list`).
- Use it from the first run: timing works before any setup, and `init` later connects it to Notion without losing what was recorded (§4).
- Push stopped, task-attached entries to Notion (`sync`) idempotently, with retries.
- Best-effort automatic sync after commands that change entries, time-limited and switchable (§6.5). Manual `sync` always remains.
- First-run setup (`init`) and kubectl-style contexts, each pointing at a Notion workspace/project set.

**Not in v1**

- Pulling edits made in Notion back down to local entries. (Task names/status are pulled read-only into a cache; entries are push-only.)
- Multiple users, reports, invoicing, a TUI or web UI, a background daemon (the post-command sync in §6.5 runs in the same process and is not one).
- Syncing a running timer.

**Post-v1**

- Typo-tolerant "did you mean" suggestions on `not_found`: suggestions only, never auto-selecting. See open question 10.

**Stretch**

- `tt import <toggl.csv>`: backfill Toggl history as unattached entries, to be attached later.
- `sync --push-unattached`.
- An opt-in semantic task matcher (local embedding model, suggestions only). See open question 10.

## 2. Concepts

| Term | Meaning |
|---|---|
| **Entry** | A time span: `start`, `end` (null while running), optional `description`, optional `task`. Identified by a locally generated UUID. |
| **Task** | A page in the Notion tasks database. Identified by its Notion page ID; names and order numbers are display hints only and are not unique. |
| **Context** | A named configuration, stored in the same SQLite database as the entries it owns: Notion token reference, tasks/projects/entries databases, property mapping, default project. Identified by a stable generated ID; the name is only a display label, so renaming never orphans data. A context is either **connected** to Notion or **local-only**; the first run creates a local-only one called `default` (§4). |
| **Project** | A page in the Notion projects database; tasks relate to it. Used to scope task selection. |

Only **one timer runs at a time, across all contexts**.

## 3. Commands

Global flags: `--context <name-or-id>` overrides the current context; `--output table|json` selects the output format (default `table`); `--no-input` disables all prompts; `--yes` confirms destructive actions without asking. Exit codes are global and stable; see §8.

### 3.1 Timer

| Command | Behaviour |
|---|---|
| `tt start [description] [-t, --task SELECTOR \| --pick] [--switch] [--at TIME \| --ago DURATION]` | Starts a timer. Requires a description, a task (`--task` or `--pick`), or both; bare `tt start` is an error. The start timestamp is captured first, before any lookup or picker; with `--at` or `--ago` it is the given time instead (never in the future; see Time arguments below). Errors if a timer is already running in any context, naming it. With `--switch`, stops the running timer and starts the new one using the same timestamp, in one transaction (with `--at` / `--ago`, the old entry ends at that time, which must be after the old entry's start). The task is resolved before the transaction opens, so a failed lookup or a cancelled picker leaves the old timer running. Prints the new entry's ID. A plain `start` never syncs; `start --switch` makes the automatic sync attempt for the entry it stopped (§6.5). In a local-only context, `--task` and `--pick` fail with `not_initialised`; a description works. |
| `tt stop [--at TIME \| --ago DURATION]` | Stops the running timer. `--at` and `--ago` are mutually exclusive; the end must be after the entry's start and not in the future. Prints the closed entry, including its ID and duration. |
| `tt status` | Shows the running entry (label, elapsed, start time, description), today's total, and counts of pending / blocked / waiting entries. Warns if the timer has run implausibly long (threshold set by the `stale_timer_threshold` setting, default 12h). In a local-only context it adds one line, `not connected to Notion yet; run 'tt init'`. Always exits 0, including before `init`. |

**Time arguments** (`--at`, `--ago`, `--start`, `--end`, `--duration`): `--at`, `--start` and `--end` accept `HH:MM` or an RFC 3339 timestamp; `--ago` and `--duration` accept durations such as `10m` or `1h30m`. No time may be in the future, and an end must be after its start; otherwise the command fails with `invalid_time` (exit 2). Entries that overlap existing entries are allowed, but the overlap is reported as a warning (see §8).

An `HH:MM` is read in the configured timezone and means **the most recent such time that is not in the future**: today if that time has already been reached, otherwise yesterday. So at 09:00, `stop --at 18:00` closes a timer left running overnight at 18:00 yesterday, and at 00:10, `start --at 23:50` means 23:50 the previous evening. It never reaches back more than 24 hours; for anything earlier, use an RFC 3339 timestamp. Whenever an `HH:MM` resolves to yesterday, the command says so (a line in table mode, an entry in `warnings` in JSON) and the echoed entry shows the full date, so a mistyped time is visible straight away.

An `--end` given as `HH:MM` together with `--start` is read relative to the start instead: it means the first such time after the start. So `add --start 23:00 --end 01:00` logs a two-hour entry across midnight, and `add --start 09:00 --end 10:00` an hour the same day. If that moment is in the future (for example `--start 16:00 --end 09:00` at 16:55, which would end tomorrow), the command fails with `invalid_time`.

**Time zone.** Times are stored in UTC. Local-time input (`HH:MM`) is read in the configured `timezone` (default: the system zone; see §4). Table output and the "today" and `--week` boundaries of `list` also use that zone; `--week` is the current ISO week, Monday to Sunday. JSON timestamps are always UTC. Around daylight saving changes, a local time that does not exist (such as 01:30 on the spring-forward night) fails with `invalid_time`; it does not fall back to another day. A local time that occurs twice (autumn) resolves to its first occurrence and adds a warning; pass an RFC 3339 timestamp with an offset to mean the other one.

**Task selector** (the value of `--task` in `start`, `add` and `edit`, and the argument of `attach`; `--all` is accepted wherever `--task` or `--pick` is). A selector is tried against the cached tasks in this order, and the first rule that matches at least one task decides the result:

1. The **full page ID**, with or without dashes.
2. A **short ID**: a suffix of the Notion page ID, at least 6 characters, as shown in the picker and in `tt task list`. (Pages created around the same time share long ID prefixes, so prefixes don't distinguish tasks; suffixes do. The displayed short ID is the shortest suffix of at least 6 characters that is unique within the cache.) ID matching runs against the whole task cache, not just the current project: page IDs are globally unique, so there is no ambiguity, and it still works after switching the default project.
3. An **order number** such as `4.1`, accepted only if exactly one open task in the current project has that order. If several do, it is ambiguous. (Short IDs are at least 6 characters, so they are not mistaken for order numbers.)
4. **Words from the task name**, matched within the current project's open tasks (see the matching rules below).

If several tasks match, ask which one (interactive), or fail with `ambiguous_selector` (exit 3) listing the candidates with short IDs (non-interactive; see §8). `--task` always takes a value; a bare `--task` is a usage error. **`--pick`** is a separate flag, accepted by `start`, `add` and `edit`, with no short form, that opens an interactive picker over the current project's open tasks, sorted by Order, then name. `--pick` and `--task` are mutually exclusive, and without a terminal `--pick` is a usage error (exit 2). The picker, like any lookup, runs before anything is written, and the start timestamp of `start` is captured before it opens, so time spent choosing is not lost. **Cancelling the picker (Esc or Ctrl-C) changes no data**: no entry is created, edited or started, a running timer (including the one `--switch` would have stopped) is left exactly as it was, and the command exits 2 with `cancelled`. `--all` includes done tasks in the picker. A selector must resolve to a cached task; otherwise it fails with `not_found` (exit 3), whose `details` list matches from other projects in the context, if any, with their project names. In a local-only context there is no task cache at all, so every selector, and the picker, fails with `not_initialised` (exit 5).

**Name matching (rule 4).** Lexical only; v1 has no fuzzy or semantic matching.

- The query and each task name are lower-cased, stripped of accents, and split into words at every non-alphanumeric character. So `agent-friendly` is the words `agent` and `friendly`, and `--json` is `json`. A query with no words left after this is a usage error (it would otherwise match every task).
- A task is a candidate if **every query word matches the start of some word** in its name, in any order. `agent json` finds "Design CLI to be agent-friendly (--json, …)", and `back` finds "Background sync".
- Matching is by word start, not substring, so `test` does not match "httptest".
- If any candidate matches every query word as a **whole word**, only those candidates are kept. This stops `cli` from also returning "client" when real CLI tasks exist, while `back` still finds "Background sync" when nothing matches whole.
- A task whose normalised name equals the normalised query wins outright.
- One remaining candidate resolves; several are ambiguous; none is `not_found`.
- Done tasks are excluded from order and name matching unless `--all` is given. A full or short ID always resolves, whether or not the task is done.

**The resolved task is always echoed** (name and short ID) in the output of `start`, `attach`, `add` and `edit`, and in their JSON, so a wrong match is visible immediately and can be undone with `--switch` or `edit`.

**Agents.** Order and name matching exist for people. Scripts and agents should resolve tasks with `tt task list --output json` and pass full page IDs; the agent documentation and the planned Claude skill say so.

**Task refresh when resolving a selector** (`start`, `add`, `edit`, `attach`): if the task cache is older than its TTL (default 10 min), do a blocking incremental refresh (tasks edited since the last refresh), capped at 2s. On timeout or error, fall back to the cache, print one line saying so, and record the failure (`cache_state.tasks_refresh_failed_at`); further blocking refreshes are skipped for the next minute, so an offline `start` is not delayed by 2s every time. A fresh cache makes no network call.

### 3.2 Entries

| Command | Behaviour |
|---|---|
| `tt list [filters] [--limit N] [--wide]` | Defaults to today's entries (local time), chronological. Filters: `--week`, `--since/--until YYYY-MM-DD`, `--task`, `--project`, `--unattached`, `--unsynced`. An entry spanning midnight belongs to the day it started. Long names are truncated to terminal width unless `--wide`. |
| `tt attach [selector] [--entry ID]` | Links a task to an entry: the running entry, else the most recent one in the current context, unless `--entry` is given. With no selector it opens the picker (interactive only; without a terminal a selector is required; cancelling changes nothing, as for `--pick`). Bare `tt attach` is the everyday form; `--entry` and an explicit selector are for older entries and for scripts and agents. Needs a connected context (`not_initialised` otherwise). |
| `tt edit <entry> [--description] [--start] [--end] [--task SELECTOR \| --pick]` | Edits an entry. If it was already synced, it becomes pending again and the existing Notion row is updated on the next sync. Editing or re-attaching a `blocked` entry makes it pending again. `--end` cannot be set on a running entry (use `stop --at`). `--task` and `--pick` change the task; v1 has no way to detach one (delete the entry instead). |
| `tt delete <entry> [--yes] [--dry-run]` | Marks the entry deleted locally (tombstone); the next `sync` archives its Notion row. An entry is removed outright only if no create was ever attempted for it (`create_attempted_at` is null, §4). Otherwise, including after a cancelled or timed-out create, it is tombstoned, because a Notion row may exist without the local page ID having been recorded. Deleting the running entry removes it and frees the timer. |
| `tt add [description] [-t, --task SELECTOR \| --pick] (--start T [--end T \| --duration D] \| --end T --duration D \| --duration D)` | Logs a finished entry directly, without touching the running timer. The span is given as any of: `--start` and `--end`; `--start` and `--duration`; `--end` and `--duration`; or `--duration` alone or `--start` alone (the entry then ends now). Like `start`, it needs a description, a task (`--task` or `--pick`), or both. With a task it is pending sync immediately; without one it is "waiting". Prints the new entry, including its ID. |

Entries are displayed with the description if present, otherwise the task name; unattached entries are flagged. Descriptions are limited to 2000 characters (a Notion text limit); longer ones fail with `usage` (exit 2).

**Entry IDs.** An entry is selected by a unique prefix of its UUID, at least 7 characters (entry UUIDs are random, so prefixes work, unlike task page IDs). Entry IDs are shown in `list`, printed by `start` and `stop`, and included in every JSON response.

### 3.3 Sync

`tt sync [--retry-blocked] [--dry-run] [--strict]`. See §6. `--dry-run` reports what would be pushed, updated, archived or skipped, without writing to Notion or changing local state. `--strict` changes only the exit code (§6.4): it exits 1 whenever any entry is blocked, not just entries that failed or became blocked in this run. In a local-only context `sync` fails with `not_initialised`.

The commands that change entries (`stop`, `add`, `edit`, `delete`, `attach`, and `start --switch`) also make a short best-effort sync attempt afterwards unless it is turned off; `--sync` / `--no-sync` override the setting for one command. See §6.5.

### 3.4 Setup, contexts, projects, tasks, settings

| Command | Behaviour |
|---|---|
| `tt init [--context <name>] [--tasks-db ID] [--projects-db ID] [--parent-page ID] [--project NAME] [--add-rollup \| --no-add-rollup] [--store-token]` | Connects a context to Notion; see §5. It is optional for timing, which works from the first run. It configures the context named by `--context`, else the current one, and **adopts the local-only `default` context**, with every entry already recorded in it, rather than creating a new one. Every prompt has a flag equivalent, so it runs with `--no-input`. The token is never accepted as a flag (it would end up in shell history): supply it via `NOTION_TOKEN`, and `--store-token` saves it to the keychain. Safe to re-run: existing resources are detected and reused, never duplicated. |
| `tt context list \| current \| use <name>` | List (current marked `*`, with each context's ID and whether it is connected), print, or switch the current context. Wherever a context is named (`--context`, `use`, `set`, `delete`), the name or the ID is accepted. |
| `tt context set <name> [--project <name>] [--rename <new-name>]` | Creates or edits a context, found by name or ID. `--rename` changes the display label only; the ID, entries and cache are untouched. Names must be unique. |
| `tt context delete <name> [--yes]` | Removes the context and everything that belongs to it (entries, cached tasks and projects) in one transaction, then its keychain token. Confirms first (or requires `--yes`); warns about unsynced entries; refuses if the running timer belongs to it, and refuses to delete the current context (switch first), so there is always a current one. |
| `tt project list \| current \| use <name>` | List cached projects, show, or set the current context's default project. Shortcut for `context set --project`. |
| `tt task list [--project NAME] [--all] [--limit N]` | Lists cached tasks (default: the current project's open tasks, sorted by Order, then name) with order, short ID and name. This is how scripts and agents discover task IDs. `--all` includes done tasks. |
| `tt task refresh [--full]` | Refreshes the task cache now, ignoring the TTL, the 2s cap and any back-off. `--full` fetches everything, which also detects deleted or archived tasks. |
| `tt config list \| get <key> \| set <key> <value>` | Reads and writes the global settings (§4). `set` validates the value: an unknown key, a malformed duration, or an unknown timezone fails with `usage` (exit 2). Supports `--output json`. |

## 4. Data model

Everything lives in **one SQLite database** (pure-Go driver, no cgo) in the user data directory (for example `~/.local/share/tt/` on Linux). `TT_DB` overrides the path, which suits tests, containers and agents. There is deliberately no separate config file: contexts, settings and entries are in the same file, so they cannot drift apart, losing or replacing a config file cannot orphan entries from the context that owns them, and one backup covers everything. Tokens are never stored in the database (§9).

There are seven tables: `contexts`, `settings`, `entries`, `tasks`, `projects`, `cache_state` and `schema_migrations`. Every table that belongs to a context carries a `context_id` that references `contexts(id)`, the context's stable ID, never its name. Foreign keys are enforced (`PRAGMA foreign_keys = ON` on every connection), and a context's data is deleted explicitly, in one transaction, by `context delete`. If the database is lost, `init` can rebuild a context, since it detects the Notion resources that already exist; only entries that had not yet synced are gone.

Several `tt` processes can touch the file at once (for example an automatic sync in one terminal while a command runs in another), so the database is opened in WAL mode with a busy timeout, and no transaction is ever held across a network call.

**contexts**

| Column | Notes |
|---|---|
| `id` | UUID generated when the context is created, never changed. Primary key; also the name of the context's keychain entry (§9). |
| `name` | Unique display label; can be renamed. |
| `tasks_db_id`, `projects_db_id`, `entries_db_id` | The Notion data-source IDs. Null until `init` connects the context. |
| `property_map` | JSON: the names of the task name, project relation, order and done properties. Null until `init`. |
| `default_project_page_id` | The default project; null if none. |
| `is_current` | 1 for the current context. A partial unique index (`ON contexts (is_current) WHERE is_current = 1`) allows at most one, and `context use` switches it in a single transaction. The first context is created current, and `context delete` refuses to delete the current one, so there is always exactly one. |
| `created_at` | |

**The default context.** The first command to run against a database with no contexts creates one named `default` and makes it current. This happens after the migrations, inside a `BEGIN IMMEDIATE` transaction that re-checks the table, so two processes starting at the same time create one. Its Notion columns are null (`entries_db_id IS NULL`), and that is what **local-only** means; there is no separate flag to fall out of step. So `tt start "reading"` works the first time the binary runs.

What works while a context is local-only: `start` and `stop`, `add`, `edit`, `delete` and `list` with descriptions, and `status`, which exits 0 and says so. What needs Notion fails with `not_initialised` (exit 5) and a hint to run `tt init`: `--task`, `--pick`, `attach`, `task`, `project` and `sync`. Entries recorded meanwhile have no task, so they are `waiting`, and auto-sync does nothing for them. When `init` connects the context (§5) it keeps the context's ID, so nothing is migrated or copied: the earlier entries are simply there, and `attach --entry` links them to tasks afterwards.

**settings** (global, not per context), managed with `tt config`. One row per key (`key` primary key, `value` text); a key without a row has its default. `tt config set` validates before it writes.

- `stale_timer_threshold`: a duration, default `12h`. `status` warns when the running timer is older than this.
- `timezone`: an IANA zone name such as `Europe/London`, defaulting to the system zone. The `TT_TIMEZONE` environment variable overrides it for a single run, which suits agents and containers (usually UTC).
- `auto_sync`: boolean, default `true`. Whether commands that change entries make a best-effort sync attempt afterwards (§6.5). `TT_AUTO_SYNC` overrides it for a single run; `--sync` / `--no-sync` override it for one command.
- `auto_sync_timeout`: a duration, default `5s`. The time limit for that attempt.

**entries**

| Column | Notes |
|---|---|
| `id` | UUID, generated when the entry is created; stored in Notion as `Local ID`, which is the idempotency key. Not a Notion page ID. |
| `context_id` | The owning context's stable ID (not its name); foreign key to `contexts(id)`. |
| `start_utc`, `end_utc` | `end_utc` null while running; display in local time. Stored with full precision (seconds); truncated to the minute only when pushed to Notion (§5) |
| `description` | nullable |
| `task_page_id` | The task this time is *for*: a page in the tasks database. Becomes the `Task` relation on the entry's Notion row. Null while the entry is unattached. |
| `entry_page_id` | This entry's *own* row in the Time entries database. Used to update or archive the row on later syncs. Null until the first successful push. |
| `create_attempted_at` | Set, and committed, just before the first request to create the entry's Notion row. If it is set while `entry_page_id` is null, a row may exist that the tool has no record of: sync resolves this by looking up `Local ID` (§6.2), and `delete` tombstones the entry instead of removing it. |
| `sync_state` | `pending`, `synced`, `failed`, `blocked`. The single source of truth for what a sync does next. Every change a command makes sets it to `pending` (including editing or re-attaching a `blocked` entry). `failed` entries are retried on the next sync and counted as pending by `status`. |
| `sync_error` | last error message |
| `updated_at`, `synced_at` | `updated_at` is bumped by every change; `synced_at` is when the last push succeeded. A sync marks an entry `synced` only if its `updated_at` is unchanged since the sync read it (§6.3). |
| `deleted_at` | tombstone |

A **partial unique index on a constant expression** makes two running timers unrepresentable:

```
CREATE UNIQUE INDEX one_running ON entries ((1))
  WHERE end_utc IS NULL AND deleted_at IS NULL;
```

Indexing a nullable column (`end_utc` or `deleted_at`) would not work: SQLite treats NULLs as distinct in a unique index, so every running row would be allowed. A test inserts two running rows and expects the second to fail.

"Waiting" (stopped but unattached) is derived, not stored.

**tasks** (a read-only cache of the tasks database)

| Column | Notes |
|---|---|
| `context_id`, `page_id` | Primary key together. `page_id` is the Notion page ID, stored without dashes. |
| `name` | The task title. |
| `project_page_id` | The project the task belongs to; null if none. |
| `sort_order` | The Order property as a real number (such as `4.1`); null if unset. Not unique. |
| `done` | Boolean. |
| `last_edited_time` | From Notion; drives incremental refresh. |
| `gone_at` | Set when a full refresh no longer finds the task in Notion (deleted or archived); cleared if it reappears. Null while the task exists. |

Short IDs are derived from the set of cached `page_id` values, not stored. Rows with `gone_at` set are kept, so existing entries can still show their task's name, but they are excluded from the picker, `task list` and name matching.

`entries.task_page_id` is deliberately **not** a foreign key to `tasks`. A task can vanish from Notion while its entries still exist, and entries must survive that: at sync they become `blocked`, never deleted.

**projects** (a read-only cache of the projects database)

| Column | Notes |
|---|---|
| `context_id`, `page_id` | Primary key together. |
| `name` | The project title. |
| `gone_at` | As for `tasks`. |

**cache_state** (one row per context)

| Column | Notes |
|---|---|
| `context_id` | Primary key. |
| `tasks_refreshed_at` | Time of the last successful refresh. An incremental refresh asks Notion for tasks edited since this time, minus a small overlap to tolerate clock skew. |
| `tasks_refresh_failed_at` | Time of the last failed or timed-out refresh; blocking refreshes are skipped for a minute after it (§3.1). |
| `tasks_full_refreshed_at` | Time of the last full refresh (`tt task refresh --full`, or the occasional automatic one). |
| `projects_refreshed_at` | Time of the last projects refresh. |

**schema_migrations**

| Column | Notes |
|---|---|
| `version` | Integer primary key: the migrations applied so far. |
| `applied_at` | When it was applied. |

Migrations are forward-only and run automatically at startup, each in a transaction. A database written by a newer version of the tool than the running binary is never modified: the command fails with exit 5 (`database_too_new`) and says to upgrade. A database that cannot be opened or is corrupt fails with exit 5 (`database_unusable`).

## 5. Notion setup and `init`

Prerequisites: a Notion internal integration, connected to a parent page that contains (or will contain) the projects and tasks databases. An integration only sees pages explicitly shared with it, so sharing the parent page covers everything beneath it. A relation to another database does not grant access to it.

Example layout:

```
Sabbatical 2026 (page)
├── Projects         (existing database)
├── Sabbatical Tasks (existing database)
└── Time entries     (created by init)
```

**Which context `init` configures.** In order: the context named by `--context`, if it exists; otherwise the local-only `default` context, which is adopted and renamed to the `--context` name if one was given (it keeps its ID and entries); otherwise a new context is created. Without `--context`, it configures the current context. Running `init` again on a connected context is safe: it re-detects and reuses what exists.

`init` does the following, checking before creating anything:

1. Validate the token (cheap "who am I" call). If none is available, fail with `set NOTION_TOKEN` or the keychain hint.
2. List databases the integration can see; pick the tasks and projects databases. If nothing is visible, explain which page to share.
3. Map task properties (name, project relation, order, done), detecting defaults.
4. Pick the parent page and **create the Time entries database** there with a tool-owned schema:

   | Property | Type |
   |---|---|
   | Title | title (description, else task name, else "(no description)") |
   | Task | relation to the tasks database (two-way) |
   | Start, End | date (with time) |
   | Description | text |
   | Local ID | text |
   | Minutes | formula: `End − Start` in whole minutes. Start and End are always whole minutes (see Minutes below), so the formula needs no rounding |

5. Offer to add a **rollup** on the tasks database (e.g. `Tracked (min)`: sum of `Minutes` via the entries relation). The API can create the rollup, as it can the `Minutes` formula (verified in the spike); because the rollup is added to the user's own tasks database, it is offered and never added without asking. Detection is structural (a rollup through that relation, over `Minutes`, with `sum`), not by name. An existing property is never modified; a name clash with a different shape offers a different name or skipping. Declining prints manual steps, and nothing in the tool depends on the rollup.
6. Choose a default project, fill in the context's Notion columns (§4) in one transaction, and do a first full task refresh. Entries recorded before `init` stay as they are, unattached; `init` reports how many there are and suggests `tt attach --entry`.

Notion computes all totals. The tool never writes a duration or total. An existing hand-maintained number such as `Time Logged (min)` is never touched; combining legacy and tracked totals is the user's Notion setup.

**Minutes.** Notion drops the seconds (and milliseconds) from `Start` and `End`: a time sent as 09:01:30 is read back as 09:01:00 (verified in the spike). So the CLI truncates both timestamps to the minute, in UTC, before sending them, and defines an entry's `minutes` as the difference between its minute-truncated start and end. The `Minutes` formula in Notion computes exactly that, so per-entry minutes agree on both sides, and Notion still computes the totals. The exact span is kept locally and reported in JSON as `seconds`; `minutes` can differ from `seconds ÷ 60` by up to a minute either way (an entry from 09:01:50 to 09:02:10 is 20 seconds and 1 minute; one from 09:01:00 to 09:01:59 is 59 seconds and 0 minutes). The error is unbiased, because truncating both ends cancels out on average. An entry that starts and ends within the same minute counts as 0 minutes but is kept. Totals shown by `status` and `list` are the sum of the per-entry minutes, which matches the rollup in Notion. The CLI does the truncation itself rather than leaving it to Notion, so the rule does not depend on Notion's behaviour.

`sync` re-validates the entries database schema before pushing, so renamed or deleted properties produce a clear error instead of a cryptic API failure.

## 6. Sync

### 6.1 What is pushed

A stopped entry **with a task attached**, regardless of whether the task is marked done. Running timers and unattached entries are held back and reported as `waiting`. Tombstoned entries archive their Notion row. A tombstoned entry with no stored page ID is looked up by `Local ID` first: if a row is found it is archived, otherwise the tombstone is dropped locally. Archiving a row that is already archived counts as success (to be confirmed; open question 19).

**What is sent.** `Start` and `End` go as UTC instants (`Z` or `+00:00`), truncated to the minute (§5), and never with a `time_zone` field. Notion keeps the wall-clock digits of a timestamp and swaps its offset for the named zone's, so `08:00:15+00:00` sent with `time_zone: Europe/London` comes back as `08:00:00+01:00`, a different instant; with a non-UTC offset it rejects the request.

### 6.2 Idempotency

Before every create request for an entry without a stored `entry_page_id`, query the entries database by `Local ID`. If a row exists, adopt its page ID and update it; otherwise set `create_attempted_at`, create the row, and immediately record the returned page ID. The lookup is repeated on every retry of a failed create, not only the first attempt: a request that timed out may have succeeded on Notion's side, and creating again without looking would duplicate the row. Entries with a page ID are updated directly. This makes a retry after a crash, a timeout or a cancelled request safe. Notion has no unique constraints, so this guarantee holds for a single writer, which is the supported case (see the sync lock in §6.5). It also relies on a filtered query seeing a row that was just created, which the Notion spike confirmed (40 creates, each followed at once by a query on `Local ID`: no misses, no duplicates; open question 17). The query does not return trashed pages, so a row archived in Notion is invisible to this lookup; §6.3 covers that case.

### 6.3 Failure handling

Failures come in two kinds, and they are handled differently.

- **Network-level failures**: the tool cannot reach Notion, or loses the connection part-way (cannot connect, connection reset or dropped, request timed out). Nothing is known to be wrong with the entry, so it is never marked `failed`: it stays `pending`.
- **HTTP-level temporary errors** (429, 5xx): Notion answered, but could not do the work. After enough attempts the entry is marked `failed`.

| Failure | Handling |
|---|---|
| Network-level failure | Retry the request with exponential backoff and jitter. Failures are counted per request attempt, across entries, and any successful request resets the count. After **3 consecutive network-level failures** the run stops and treats Notion as unreachable: the current entry and all remaining entries stay `pending`, entries already pushed stay `synced`, and the run exits 6 (`offline`). The error's `details` say how many entries were synced before it stopped and how many are still pending. |
| Offline (the run's first connection attempt fails) | Abort at once, without retries, so being offline is quick: `offline: N entries pending`, exit 6. |
| 429, 5xx | Retry with exponential backoff and jitter, honouring `Retry-After`; 5 attempts per entry, then the entry is recorded `failed` (`rate_limited` or `server_error`) and retried on the next run. A 429 pauses the whole run, not just that entry. Requests respect Notion's rate limit (~3 req/s). |
| 401 | Abort the run: token invalid or revoked. |
| 403/404 on the entries database | Abort: integration lost access or the database is gone; say which page to re-share. |
| Schema mismatch | Abort before pushing anything. |
| 404 on one entry's task page | Mark that entry `blocked` (`task_not_found`) with the error; continue with the rest. |
| 404 on an entry's own row, or the row is archived (deleted or archived by hand in Notion) | Mark that entry `blocked` (`row_missing`) rather than silently recreating or restoring a row the user may have removed on purpose. Updating an archived page does not return 404: Notion answers 400 `validation_error` ("Can't edit block that is archived"). So on a 400 for an entry that has a stored page ID, the engine retrieves the page and treats it as `row_missing` if it is in the trash and as `bad_request` otherwise; the error text is not relied on (open question 19). `sync --retry-blocked` restores the row and applies the update in one request (`in_trash: false` together with the properties, which Notion accepts, unlike editing the archived page). It uses the stored page ID, because a `Local ID` query cannot find a trashed row. If the page is gone for good (404), it falls back to the `Local ID` lookup and creates the row. |
| 400 on one entry | Mark `blocked` (`bad_request`); continue. |

Blocked entries are not retried on every run. They become pending again when edited or re-attached, or with `sync --retry-blocked`; if one of those retries blocks again, that counts as blocked in that run (§6.4). Ctrl-C cancels the context, lets the in-flight request finish, saves state, and exits cleanly. (The automatic sync's time limit in §6.5, by contrast, cancels the in-flight request; that is safe because of `create_attempted_at` and the lookup in §6.2.)

When a connection drops part-way through a create, the request may or may not have reached Notion. That is exactly the case `create_attempted_at` and the `Local ID` lookup cover: the next sync finds the row if it was created, adopts its page ID, and updates it, and creates it only if it is not there.

**Edits during a sync.** A sync records the `updated_at` of each entry it reads and marks the entry `synced` only if `updated_at` is unchanged when the push succeeds. Otherwise the entry stays `pending` and is pushed again on the next run, so an edit made while a sync is running is never lost.

### 6.4 Output and exit codes

**Table output** (the default). `tt sync` prints one line per entry it pushed or could not push, then a blank line and a summary. Each line is a status word, the entry's label, and a detail column: the duration for entries that were pushed, or the reason for anything else. If entries blocked by earlier runs are still blocked, a last line reminds you of them.

```
$ tt sync
synced   Local store for time entries   1h 12m
failed   Add retry logic                429 rate limited, will retry next run
blocked  Old task                       task not found in Notion; run 'tt attach --entry a1b2c3d'
waiting  2 entries without a task

synced 1 · failed 1 · blocked 1 · waiting 2
2 earlier entries still blocked (see 'tt status')
```

| Status | Meaning | Tried again on the next run? |
|---|---|---|
| `synced` | Pushed to Notion (created, updated or archived). | Not needed |
| `failed` | Notion answered with a temporary error (rate limit or server error) that persisted after 5 attempts. Network failures never produce `failed`; they leave entries `pending` and end the run with exit 6. | Yes |
| `blocked` | A permanent problem with this entry, such as its task being deleted in Notion. | No, until you fix it (`attach`, `edit`) or run `sync --retry-blocked` |
| `waiting` | Stopped, but not attached to a task, so it was not sent. This is not a failure. Shown as one grouped line with a count, not one line per entry. | Yes, once attached |

With `--dry-run`, the status words become `would push`, `would update` and `would archive`, and nothing is sent.

**Exit codes** for `sync` come from the global scheme in §8:

| Code | When |
|---|---|
| 0 | Nothing failed and nothing became blocked in this run. Waiting entries do not count. Entries that were already blocked by an earlier run, and that this run did not touch, do not count either. `--dry-run` always exits 0. |
| 1 | The run happened, and at least one entry `failed` or became `blocked` in it (a retry that blocks again counts). With `--strict`, also whenever any entry is blocked, however long ago. |
| 5 | The run could not start because of a config, auth or schema problem, or the context is not connected (`not_initialised`). |
| 6 | Notion is unreachable: the first connection failed, or 3 consecutive network-level failures stopped the run (§6.3). Affected entries stay `pending`; the error's `details` give the counts synced and still pending. |

So a permanent problem is reported with exit 1 once, when it happens, and a scheduled `tt sync` then goes back to exit 0 rather than looking broken until someone fixes the entry. The blocked entry is not forgotten: it stays in the `still_blocked` count, in the reminder line above, and in `tt status` until it is fixed. A monitoring check or CI job that wants the persistent signal uses `--strict`.

**JSON output** (`--output json`) carries the same information: a `results` array with one object per entry touched in this run (its ID, its state, and an `error` with a stable `code` if it did not sync), plus a `summary` of the counts. `still_blocked` counts entries blocked by earlier runs that this run left alone. With `--dry-run` the document has `"dry_run": true` and the states are `would_push`, `would_update` and `would_archive`.

```
{
  "schema_version": 1,
  "results": [
    {"entry_id": "a1b2c3d4-…", "state": "synced"},
    {"entry_id": "b7c8d9e0-…", "state": "failed",
     "error": {"code": "rate_limited", "message": "429, 5 attempts, will retry next run"}}
  ],
  "summary": {"synced": 1, "failed": 1, "blocked": 0, "waiting": 0, "still_blocked": 2}
}
```

This example exits with code 1 (an entry failed in this run). The per-entry `error.code` values are: for `failed`, `rate_limited` and `server_error`; for `blocked`, `task_not_found`, `row_missing` and `bad_request`. A run that ends with exit 6 is a failed command (§8), so it writes the error document to stderr, with the counts in `details`, rather than a `results` array.

### 6.5 Automatic sync after changes

By default, a command that leaves entries waiting to be pushed makes one short, best-effort sync attempt after it has saved its change locally. This is what "background sync" means here: no daemon and no separate process, just the same process finishing its work. Manual `tt sync` stays available and works exactly as in §6.1 to §6.4.

**Which commands.** `stop`, `add`, `edit`, `delete` and `attach`, and `start --switch` when the entry it stopped has a task: the commands that create or change a pushable entry. A plain `start`, `status`, `list` and the read-only commands never sync. `start --switch` accepts `--sync` / `--no-sync` and returns the `sync` object for the entry it stopped; a plain `start` has neither.

**What it syncs.** The entry the command just changed goes first, then any other pending entries, until the time limit runs out. Anything left stays pending for the next sync, automatic or manual.

**Local first, then sync.** The change is committed to SQLite before any network call. The sync attempt cannot lose it, fail it or change the command's exit code.

**Time limit.** The whole attempt is bounded by `auto_sync_timeout` (default 5s). When the limit is reached, the in-flight request is cancelled (unlike Ctrl-C in §6.3, which lets it finish) and the remaining entries stay pending. A cancelled create is safe: `create_attempted_at` is already set, so the next sync finds the row by `Local ID` (§6.2), and `delete` tombstones rather than removes the entry. A `Retry-After` longer than the time left ends the attempt. The normal 5-attempt backoff (§6.3) only applies within the limit; a manual `tt sync` has no such limit.

**When it does not run, or stops early.** All of these leave the entries pending, exit with the command's own code, and report a short note:
- Auto-sync is turned off (below).
- The entry has no task, so it is `waiting`. No attempt is made and nothing is reported. This is always the case in a local-only context.
- Offline or a dropped connection: connection setup is limited to 1s so an offline `stop` is not noticeably slower, and network-level failures end the attempt as in §6.3 (the 3-failure rule, or the time limit, whichever comes first). The note is `pending sync (offline)`; the command's exit code is unchanged, never 6.
- The run aborts for a reason that would also abort `tt sync` (401, lost access, schema mismatch). The note carries the error code and says to run `tt sync`.
- Another sync holds the sync lock (below).
- The entry is marked `blocked` (400, or its task is gone from Notion). The note says to run `tt sync` for the details.

**Reporting.** The command's normal output comes first. In table mode, a single line follows on stderr, such as `synced 1 entry` or `pending sync (offline)`, and is omitted when there is nothing to say. With `--output json`, the entry-returning commands include a `sync` object, so an agent knows the entry's state without asking again:

```
"sync": {"attempted": true, "result": "synced", "pending": 0}
```

`result` is one of `synced`, `pending` (offline, timed out or stopped early), `failed`, `blocked`, `waiting` (no task attached), or `disabled`. `pending` in the object is the number of entries still waiting to be pushed after the attempt. Details of any problem go in `warnings`. Delete's `sync` object reports the archive of the Notion row.

**Control.**
- Setting `auto_sync` (boolean, default `true`) and `auto_sync_timeout` (duration, default `5s`).
- Per command: `--sync` and `--no-sync` on the commands listed above override the setting.
- Per environment: `TT_AUTO_SYNC=false` (or `true`) overrides the setting for a run, which suits scripts and containers. Precedence: flag, then environment variable, then setting.

**One writer at a time.** The guarantee in §6.2 holds for a single writer, so every sync, automatic or manual, takes a lock before it reads any entry and holds it for the whole run (an advisory lock on a file in the data directory, released by the OS if the process dies). Automatic sync that finds the lock held skips quietly. A manual `tt sync` that finds it held fails with `sync_busy` (exit 4), because the user asked for it explicitly.

**Scheduled sync.** For a periodic catch-up (for example after a long offline spell), a cron job or launchd agent running `tt sync --no-input` works as is. It exits 1 once when an entry fails or becomes blocked and 0 afterwards (§6.4), so it does not look permanently broken; add `--strict` if the job should keep flagging blocked entries. The tool does not install the job.

## 7. Command errors vs sync failures

Command errors are local, immediate, and never retried: `stop` with nothing running; `start` while a timer runs without `--switch`; `start` with no description and no task; `--at`, `--ago`, `--start`, `--end` or `--duration` giving an impossible span (an end before its start, or a time in the future); a selector that matches nothing or too much; a cancelled picker (`cancelled`, exit 2, nothing changed); a command that needs Notion run in a context that is not connected yet (`not_initialised`, exit 5, with a hint to run `tt init`); an unusable SQLite file (`database_unusable`, exit 5). Notion being unreachable is never an error for `start`, `stop`, `status`, or `list`, and none of them needs `init`. Only `init` and `sync` require Notion to be reachable. The same goes for the automatic sync after a change (§6.5): when it cannot complete, the entries stay pending and the command still succeeds. Each error maps to a stable exit code (§8).

## 8. Agent-friendly interface

The CLI is designed to be driven by scripts and AI agents as well as people. The rules below apply to every command.

**Output format.** `--output table|json` (default `table`). Table output is for humans and may change between releases. JSON output is the contract.

**JSON conventions**

- One JSON document on stdout per invocation, with a top-level `schema_version` integer.
- Mutating commands return the resulting object under `entry` (`start`, `stop`, `add`, `attach`, `edit`, `delete`), or per-entry results plus a summary (`sync`), so no follow-up `list` is needed. Commands that can trigger an automatic sync (§6.5) also include a `sync` object saying what happened to it.
- Fields are never omitted: absent values are `null`. IDs are always included.
- Timestamps are RFC 3339 in UTC; durations are integer seconds.
- Notices that would otherwise be a line of text (e.g. "using task cache from 3h ago", or an `HH:MM` that resolved to yesterday) appear in a `warnings` array. Overlaps with existing entries (from `start --at/--ago`, `add`, or `edit`) are reported there too, with the IDs of the overlapping entries.
- `--wide` affects table output only.
- Discovery: agents find task IDs with `tt task list --output json` and entry IDs with `tt list --output json` (or from the response to `start` and `stop`). Every selector accepts the IDs those commands return.

**Entry object.** Wherever an entry is returned it has these fields:

```
{"id": "a1b2c3d4-…", "start": "2026-10-12T09:00:00Z", "end": "2026-10-12T10:12:00Z",
 "seconds": 4320, "minutes": 72, "description": null,
 "task": {"page_id": "…", "short_id": "c8d9e0", "name": "…"},
 "sync_state": "pending", "sync_error": null, "deleted": false}
```

`task` is `null` for an unattached entry. For a running entry, `end`, `seconds` and `minutes` are `null`. `seconds` is the exact span; `minutes` is the difference between the minute-truncated start and end (§5), so it can differ from `seconds ÷ 60` by up to a minute.

**Responses by command**

| Command | Document (besides `schema_version`) |
|---|---|
| `start` | `entry`, `stopped` (`null`, or the entry that `--switch` stopped), `sync` (only with `--switch`), `warnings` |
| `stop`, `add`, `attach`, `edit` | `entry`, `sync`, `warnings` |
| `delete` | `entry`, `removed` (true if removed outright rather than tombstoned), `sync`, `warnings`. With `--dry-run`: `dry_run: true`, `action` (`remove` or `tombstone`), `entry`. |
| `status` | `running` (an entry or `null`), `today` (`entries`, `minutes`), `counts` (`pending`, `blocked`, `waiting`), `context` (`id`, `name`, `connected`), `warnings` (including the stale-timer warning) |
| `list` | `entries` (an array), `total_minutes`, `warnings` |
| `task list` | `tasks` (an array of `page_id`, `short_id`, `order`, `name`, `project`, `done`), `warnings` |
| `sync` | `results` and `summary` (§6.4) |
| `config`, `context`, `project`, `init` | the affected objects under `settings`, `contexts`, `projects` or `context` |

A collection is always an object holding an array, never a bare array, so it can carry `warnings` and totals.

**Streams.** Data goes to stdout; diagnostics, warnings and errors go to stderr. No colour, spinners or progress output when stdout is not a terminal or `NO_COLOR` is set.

**Errors.** With `--output json`, a failed command (exit code 2 or above) writes `{"schema_version": 1, "error": {"code": "...", "message": "...", "details": {...}}}` to stderr and nothing to stdout. The `code` string is stable and documented; messages are not. For example, `timer_running` carries the running entry in `details`, and `offline` from `sync` carries how many entries were synced before it stopped and how many are still pending. A `sync` that ran but finished with failed or blocked entries (exit 1) has not failed as a command: it writes its normal result document to stdout.

**No prompts.** Prompts (task picker, confirmations, `init` wizard) appear only when both stdin and stdout are terminals and `--no-input` is not set. Otherwise the command never blocks:

- `--pick` (the picker) without a terminal is a usage error. A bare `--task` is also a usage error: it always needs a value.
- An ambiguous selector fails with the candidate list, in `details` for JSON output.
- Destructive commands (`delete`, `context delete`) require `--yes`.
- Every `init` prompt has a flag equivalent (§3.4).

**Exit codes** are global and stable:

| Code | Meaning | Example error codes |
|---|---|---|
| 0 | Success | |
| 1 | Partial failure: `sync` finished, but an entry failed or became blocked in the run (with `--strict`, or any entry is blocked) | `sync_partial` |
| 2 | Usage error: bad flags or arguments, invalid time, cancelled picker | `usage`, `invalid_time`, `confirmation_required`, `cancelled` |
| 3 | Not found or ambiguous selector | `not_found`, `ambiguous_selector` |
| 4 | State conflict | `timer_running`, `no_timer_running`, `sync_busy` |
| 5 | Config, auth or local-store problem, or context not connected to Notion | `not_initialised`, `no_token`, `access_denied`, `schema_mismatch`, `database_too_new`, `database_unusable` |
| 6 | Notion unreachable (manual `sync` only; automatic sync never changes another command's exit code) | `offline` |
| 10 | Unexpected internal error | `internal` |

`status` exits 0 even when nothing is running or the context is not connected; "nothing running" is a valid answer, not an error.

**Previewing and bounding output.** `sync --dry-run` and `delete --dry-run` report what would happen without changing anything. `list --limit N` bounds output size. Every command has `--help` text with examples.

## 9. Secrets

Token lookup order: `NOTION_TOKEN` environment variable, then the OS keychain, behind a small `SecretStore` interface (keychain implementation plus an in-memory fake for tests). The keychain entry is named by the context's stable ID, so renaming a context never loses its token. No plaintext file fallback, and no token in the SQLite database: if neither source is available, commands that need the token fail with a clear message. On Linux the keychain needs a running Secret Service, so headless use relies on the environment variable. `NOTION_TOKEN` applies to whichever context is current, so with contexts in different Notion workspaces, store each token in the keychain instead.

## 10. Implementation notes

- Go, single static binary; pure-Go SQLite driver.
- CLI framework (Cobra vs stdlib `flag`) is an implementation choice, not a design decision. The design avoids optional-value flags, which pflag does not let take the next argument as their value: `--task` always takes a value and the picker is the separate `--pick` flag (open question 12, resolved). That also works with the stdlib `flag` package.
- Use the Notion API's data-source IDs for schemas and queries, and pin the API version: `Notion-Version: 2025-09-03`. Query with `POST /v1/data_sources/{id}/query`; the older `POST /v1/databases/{id}/query` returns 400 `invalid_request_url`. Store the data-source ID, not only the database ID.
- Keep the Notion client behind an interface so sync logic can be tested against a fake.
- Task name matching sits behind a small `Matcher` interface (lexical in v1), so fuzzy suggestions or an opt-in semantic matcher can be added later without touching the commands.
- Embed the timezone database (`import _ "time/tzdata"`, about 450 KB) so named zones resolve in minimal containers and the binary stays self-contained.
- Take time from an injectable `Clock` interface, not `time.Now` directly: "no future times", the most-recent-`HH:MM` rule, the stale-timer warning, cache TTLs and the DST tests all need a controllable clock.
- `time.Date` does not guarantee what it returns for a local time that does not exist or occurs twice; detect both by converting the result back and comparing, and cover this with tests on both DST change days, including `HH:MM` resolving to yesterday across a change.
- Open SQLite in WAL mode with a busy timeout and foreign keys on, and keep transactions short and local (§4). The contexts and settings tables belong to the first migration, and a small `store.Open` step creates the default context after the migrations, so every command can assume a current context exists.
- Test the first-run path explicitly: a fresh database, two processes starting at once (one `default` context), timing before `init`, then `init` adopting the context with its entries intact.
- The sync result needs to tell "blocked in this run" from "was already blocked": have the engine return a per-entry outcome for each entry it touched, and count the untouched blocked entries separately for `still_blocked`. The exit code is then a pure function of those counts and `--strict`, which makes it easy to table-test.
- Have the HTTP client classify every error as network-level (no response) or HTTP-level (a status code) in one place, and keep the consecutive-network-failure counter in the sync engine, not the client. Test the drop-mid-create case with a fake Notion client that creates the row and then fails the response: the next run must find the row by `Local ID` and not duplicate it.
- Truncate timestamps to the minute in one place, the function that builds the Notion payload, and test it: 09:01:50 becomes 09:01:00, a time already on a whole minute is unchanged, and `minutes` comes out right for 31 s, 59 s and 90 s spans and for a pair that straddles a minute boundary (09:01:59 to 09:02:00 is 1 minute). The fake Notion client should reject a payload that contains `time_zone`.
- Test the archived-row paths against the fake client: an update answered with 400 for a trashed page becomes `row_missing`, `--retry-blocked` restores and updates in one request, and archiving an already-archived row counts as success.

## 11. Open questions

1. **Can `init` create the rollup and the `Minutes` formula through the API?** Resolved (spike, 13 Oct). Yes to both: the formula can be set in the create-database call or added later with `PATCH /v1/data_sources/{id}`, and a `sum` rollup of the formula works the same way (it matched the sum of the entries' minutes in the spike). `init` prints manual steps only if the user declines the rollup (§5).
2. **Selector that matches nothing in the cache** is currently an error, even if the task was created seconds ago and the refresh timed out. Revisit if this proves annoying.
3. **Stale-timer threshold:** resolved. Configurable via `stale_timer_threshold`, default 12h (§3.1, §4).
4. **Rounding and date precision:** resolved (spike, 13 Oct). Notion truncates `Start` and `End` to the minute, so the CLI truncates before sending and defines `minutes` from the truncated endpoints (§5). The alternatives were the CLI writing `Minutes` itself, which would end Notion computing the totals, or keeping the nearest-minute rule, which would leave the CLI and Notion disagreeing on some entries. Notion's `round()` does round halves up (0.5 to 1, 2.5 to 3), but the formula no longer needs it. Never send `time_zone` (§6.1).
5. **Time zones:** resolved. UTC storage, a `timezone` setting with a system-zone default, and the DST rules in §3.1. Still to test: `list` day boundaries on the two DST change days.
6. **Full task refresh cadence** for detecting deleted or archived tasks.
7. **JSON schema stability:** what bumps `schema_version`, and whether to publish JSON schemas for each command's output (useful for the Claude skill and for contract tests). The shapes themselves are now defined in §8.
8. **Short ID stability:** a displayed task short ID is relative to the cache, so it may need to lengthen as tasks are added. Scripts should store the full page ID from JSON output, which every selector accepts.
9. **Overlapping entries:** currently allowed with a warning, since the user is the authority on what they did. The alternative is to reject overlaps unless `--force` is given, which prevents double-counted time at the cost of friction. Revisit after real use.
10. **Beyond lexical matching:** typo-tolerant "did you mean" suggestions on `not_found` (suggest only, never auto-select) are cheap in pure Go and are planned for after v1 (§1). Semantic matching would need a local embedding model (breaking the pure-Go static binary) or a remote API (breaking offline-first), and agents already do this job themselves from `tt task list`, so it stays a stretch experiment.
11. **Automatic sync details:** is 5s the right default limit, and 1s for connecting? Should `start` also flush older pending entries opportunistically (it would add latency to the command that should be fastest)? Should the sync lock be a file lock or a row in SQLite? Test that a cancelled create followed by a sync never duplicates a row.
12. **Task picker flag:** resolved. `--task SELECTOR` always takes a value, and the interactive picker is the separate boolean `--pick` (no short form, mutually exclusive with `--task`, a usage error without a terminal). Reason: pflag optional-value flags do not consume the next argument, so `tt start --task 4.1` would have taken `4.1` as the description. Cancelling the picker changes no data, never leaves a half-started timer, and exits 2 as `cancelled` (§3.1).
13. **`HH:MM` resolution:** resolved. `HH:MM` means the most recent such time that is not in the future (today, else yesterday, never further back), echoed with a warning when it lands on yesterday. An `--end` given with `--start` means the first such time after the start, so entries across midnight work. The same DST rules apply (§3.1).
14. **Using `tt` before `init`:** resolved. The first command creates a local-only `default` context as a row in `contexts` and makes it current; timing, `add`, `edit`, `delete`, `list` and `status` work without Notion, and anything that needs Notion fails with `not_initialised`. `init` adopts that context, keeping its ID and entries (§4, §5).
15. **Cron and blocked entries:** resolved. `sync` exits 1 only when an entry failed or became blocked in that run (a retried entry that blocks again counts); entries already blocked by earlier runs don't change the exit code, but stay visible as `still_blocked`, in a reminder line, and in `tt status`. `sync --strict` exits 1 whenever any entry is blocked, for monitoring and CI (§6.4).
16. **Contexts and settings storage:** resolved. They live in the SQLite database with the entries (the `contexts` and `settings` tables, §4), with foreign keys, so there is no separate config file to lose or fall out of step. Tokens stay out of the database (§9).
17. **Query-after-create consistency:** resolved (spike, 13 Oct). 40 creates, each followed at once by a filtered query on `Local ID`, gave no misses and no duplicates, so no post-sync duplicate check is added. This is one sequential client against a small database, so it is evidence rather than a guarantee; the failure-matrix tests (task 6) repeat it under realistic sync load. Editing an archived page fails with 400 `validation_error`, and a trashed page does not appear in a `Local ID` query (§6.3).
18. **Network-failure handling:** resolved. Network-level failures are counted per request attempt; 3 in a row stop the run with the affected entries left `pending` and exit 6, while `failed` is reserved for 429 and 5xx errors that persist after 5 attempts. The first connection failure of a run aborts at once (§6.3).
19. **Archived rows, remaining probe.** The spike did not cover two things that §6.1 and §6.3 rely on. (a) Archiving a page that is already archived (`in_trash: true` on a trashed page). This happens when a `delete`'s archive reached Notion but the local update was lost, and the next sync repeats it. The design treats it as success; confirm that Notion does not answer with an error that needs different handling. (b) Retrieving a trashed page reports it as trashed (`in_trash`), which is how a 400 on update is classified as `row_missing` rather than `bad_request`. A scratch probe against a throwaway entries database settles both.
