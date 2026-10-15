# timetrack-notion — Design Notes

A small Go CLI for tracking time against tasks. It works fully offline, stores tracked times along with a read-only cache of associated Notion tasks/projects in a local SQLite database, and syncs completed entries to Notion.

Status: design draft, v1 scope. Binary name `tt` is a placeholder. Features deferred from v1 are described in [DESIGN-LATER.md](DESIGN-LATER.md). Open questions are in §11.

## 1. Scope

**In v1**

- Start/stop a single timer, with or without a task.
- Log time after the fact: `add` for a finished entry, and backdated `start` / `stop` via `--at` / `--ago`.
- Attach tasks to entries after the fact; edit and delete entries.
- Choose a task by page ID, short ID or order number, or from an interactive picker.
- Inspect the running timer (`status`) and past entries (`list`).
- Use it from the first run: timing works before any setup, and `init` later connects it to Notion without losing what was recorded (§4).
- Push stopped, task-attached entries to Notion (`sync`) idempotently, with retries, one sync at a time (§6.5).
- First-run setup (`init`).

**Not in v1**

- Pulling edits made in Notion back down to local entries. (Task names/status are pulled read-only into a cache; entries are push-only.)
- Multiple users, reports, invoicing, a TUI or web UI, a background daemon.
- Syncing a running timer.

**Deferred to [DESIGN-LATER.md](DESIGN-LATER.md)**

- Automatic best-effort sync after commands that change entries.
- Multiple contexts (several Notion workspaces) and the OS keychain.
- `tt config` and the settings table (v1 hardcodes the defaults).
- `HH:MM` meaning yesterday, `--end HH:MM` across midnight, and full daylight-saving handling.
- Matching tasks by words from their name, cross-project matches on `not_found`, and "did you mean" suggestions.
- `sync --dry-run` and the entries-database schema check.
- `sync --strict`.
- The rollup offer in `init`.

**Stretch**

- `tt import <toggl.csv>`: backfill Toggl history as unattached entries, to be attached later.
- `sync --push-unattached`.
- An opt-in semantic task matcher (local embedding model, suggestions only). See DESIGN-LATER.md.

## 2. Concepts

| Term | Meaning |
|---|---|
| **Entry** | A time span: `start`, `end` (null while running), optional `description`, optional `task`. Identified by a locally generated UUID. |
| **Task** | A page in the Notion tasks database. Identified by its Notion page ID; names and order numbers are display hints only and are not unique. |
| **Context** | The configuration, stored in the same SQLite database as the entries it owns: tasks/projects/entries databases, property mapping, default project. v1 has exactly one, identified by a stable generated ID. It is either **connected** to Notion or **local-only**; it starts local-only, and `init` connects it (§4). |
| **Project** | A page in the Notion projects database; tasks relate to it. Used to scope task selection. |

Only **one timer runs at a time**.

## 3. Commands

Global flags: `--output table|json` selects the output format (default `table`); `--no-input` disables all prompts; `--yes` confirms destructive actions without asking. Exit codes are global and stable; see §8.

### 3.1 Timer

| Command | Behaviour |
|---|---|
| `tt start [description] [-t, --task SELECTOR \| --pick] [--switch] [--at TIME \| --ago DURATION]` | Starts a timer. Requires a description, a task (`--task` or `--pick`), or both; bare `tt start` is an error. The start timestamp is captured first, before any lookup or picker; with `--at` or `--ago` it is the given time instead (never in the future; see Time arguments below). Errors if a timer is already running, naming it. With `--switch`, stops the running timer and starts the new one using the same timestamp, in one transaction (with `--at` / `--ago`, the old entry ends at that time, which must be after the old entry's start). The task is resolved before the transaction opens, so a failed lookup or a cancelled picker leaves the old timer running. Prints the new entry's ID. In a local-only context, `--task` and `--pick` fail with `not_initialised`; a description works. |
| `tt stop [--at TIME \| --ago DURATION]` | Stops the running timer. `--at` and `--ago` are mutually exclusive; the end must be after the entry's start and not in the future. Prints the closed entry, including its ID and duration. |
| `tt status` | Shows the running entry (label, elapsed, start time, description), today's total, and counts of pending / blocked / waiting entries. Warns if the timer has run implausibly long (more than 12 hours). In a local-only context it adds one line, `not connected to Notion yet; run 'tt init'`. Always exits 0, including before `init`. |

**Time arguments** (`--at`, `--ago`, `--start`, `--end`, `--duration`): `--at`, `--start` and `--end` accept `HH:MM` or an RFC 3339 timestamp; `--ago` and `--duration` accept durations such as `10m` or `1h30m`. No time may be in the future, and an end must be after its start; otherwise the command fails with `invalid_time` (exit 2). Entries that overlap existing entries are allowed, but the overlap is reported as a warning (see §8).

An `HH:MM` always means **today**, in the local zone (below). If that moment has not happened yet, the command fails with `invalid_time`; for any earlier day, including a timer left running overnight, use an RFC 3339 timestamp. This applies to `--end` too, so an entry that crosses midnight is given with RFC 3339 timestamps. On the two daylight-saving change days, an `HH:MM` that does not exist (spring forward) or occurs twice (autumn) also fails with `invalid_time`, and the message says to use an RFC 3339 timestamp with an offset.

**Time zone.** Times are stored in UTC. `HH:MM` input, table output, and the "today" and `--week` boundaries of `list` use the local zone: the system zone, or the zone named by the `TT_TIMEZONE` environment variable (an IANA name such as `Europe/London`), which suits containers, agents and tests. An unknown zone name fails with `usage` (exit 2). `--week` is the current ISO week, Monday to Sunday. JSON timestamps are always UTC.

**Task selector** (the value of `--task` in `start`, `add` and `edit`, and the argument of `attach`; `--all` is accepted wherever `--task` or `--pick` is). A selector is tried against the cached tasks in this order, and the first rule that matches at least one task decides the result:

1. The **full page ID**, with or without dashes.
2. A **short ID**: a suffix of the Notion page ID, at least 6 characters, as shown in the picker and in `tt task list`. (Pages created around the same time share long ID prefixes, so prefixes don't distinguish tasks; suffixes do. The displayed short ID is the shortest suffix of at least 6 characters that is unique within the cache.) ID matching runs against the whole task cache, not just the current project: page IDs are globally unique, so there is no ambiguity, and it still works after the default project changes.
3. An **order number** such as `4.1`, matched within the current project's open tasks (done tasks too, with `--all`). It is accepted only if exactly one of those tasks has that order; if several do, it is ambiguous. (Short IDs are at least 6 characters, so they are not mistaken for order numbers.)

A full or short ID always resolves, whether or not the task is done. v1 does not match on words from the task name; anything that is not an ID or an order number is `not_found`.

If several tasks match, ask which one (interactive), or fail with `ambiguous_selector` (exit 3) listing the candidates with short IDs (non-interactive; see §8). `--task` always takes a value; a bare `--task` is a usage error. **`--pick`** is a separate flag, accepted by `start`, `add` and `edit`, with no short form, that opens an interactive picker over the current project's open tasks, sorted by Order, then name (tasks with no Order last). `--pick` and `--task` are mutually exclusive, and without a terminal `--pick` is a usage error (exit 2). The picker, like any lookup, runs before anything is written, and the start timestamp of `start` is captured before it opens, so time spent choosing is not lost. **Cancelling the picker (Esc or Ctrl-C) changes no data**: no entry is created, edited or started, a running timer (including the one `--switch` would have stopped) is left exactly as it was, and the command exits 2 with `cancelled`. `--all` includes done tasks in the picker. A selector must resolve to a cached task; otherwise it fails with `not_found` (exit 3). In a local-only context there is no task cache at all, so every selector, and the picker, fails with `not_initialised` (exit 5).

**The resolved task is always echoed** (name and short ID) in the output of `start`, `attach`, `add` and `edit`, and in their JSON, so a wrong match is visible immediately and can be undone with `--switch` or `edit`.

**Agents.** Order numbers and the picker are for people. Scripts and agents should resolve tasks with `tt task list --output json` and pass full page IDs; the agent documentation and the planned Claude skill say so.

**Task refresh when resolving a selector** (`start`, `add`, `edit`, `attach`): if the task cache is older than its TTL (default 10 min), do a blocking incremental refresh (tasks edited since the last refresh), capped at 2s. On timeout or error, fall back to the cache, print one line saying so, and record the failure (`cache_state.tasks_refresh_failed_at`); further blocking refreshes are skipped for the next minute, so an offline `start` is not delayed by 2s every time. A fresh cache makes no network call.

### 3.2 Entries

| Command | Behaviour |
|---|---|
| `tt list [filters] [--limit N] [--wide]` | Defaults to today's entries (local time), chronological. Filters: `--week`, `--since/--until YYYY-MM-DD`, `--task`, `--project`, `--unattached`, `--unsynced`. An entry spanning midnight belongs to the day it started. Long names are truncated to terminal width unless `--wide`. |
| `tt attach [selector] [--entry ID]` | Links a task to an entry: the running entry, else the most recent one, unless `--entry` is given. With no selector it opens the picker (interactive only; without a terminal a selector is required; cancelling changes nothing, as for `--pick`). Bare `tt attach` is the everyday form; `--entry` and an explicit selector are for older entries and for scripts and agents. Needs a connected context (`not_initialised` otherwise). |
| `tt edit <entry> [--description] [--start] [--end] [--task SELECTOR \| --pick]` | Edits an entry. If it was already synced, it becomes pending again and the existing Notion row is updated on the next sync. Editing or re-attaching a `blocked` entry makes it pending again. `--end` cannot be set on a running entry (use `stop --at`). `--task` and `--pick` change the task; v1 has no way to detach one (delete the entry instead). |
| `tt delete <entry> [--yes] [--dry-run]` | Marks the entry deleted locally (tombstone); the next `sync` archives its Notion row. An entry is removed outright only if no create was ever attempted for it (`create_attempted_at` is null, §4). Otherwise, including after a cancelled or timed-out create, it is tombstoned, because a Notion row may exist without the local page ID having been recorded. Deleting the running entry removes it and frees the timer. `--dry-run` reports which of the two would happen, without changing anything. |
| `tt add [description] [-t, --task SELECTOR \| --pick] (--start T [--end T \| --duration D] \| --end T --duration D \| --duration D)` | Logs a finished entry directly, without touching the running timer. The span is given as any of: `--start` and `--end`; `--start` and `--duration`; `--end` and `--duration`; or `--duration` alone or `--start` alone (the entry then ends now). Like `start`, it needs a description, a task (`--task` or `--pick`), or both. With a task it is pending sync immediately; without one it is "waiting". Prints the new entry, including its ID. |

Entries are displayed with the description if present, otherwise the task name; unattached entries are flagged. Descriptions are limited to 2000 characters (a Notion text limit); longer ones fail with `usage` (exit 2).

**Entry IDs.** An entry is selected by a unique prefix of its UUID, at least 7 characters (entry UUIDs are random, so prefixes work, unlike task page IDs). Entry IDs are shown in `list`, printed by `start` and `stop`, and included in every JSON response.

### 3.3 Sync

`tt sync [--retry-blocked]`. See §6. It takes the sync lock for the whole run (§6.5). In a local-only context `sync` fails with `not_initialised`.

Nothing syncs automatically in v1: entries are pushed when `tt sync` runs, by hand or from a scheduled job (§6.5).

### 3.4 Setup and tasks

| Command | Behaviour |
|---|---|
| `tt init [--tasks-db ID] [--projects-db ID] [--parent-page ID] [--project NAME]` | Connects the context to Notion; see §5. It is optional for timing, which works from the first run. A local-only context is connected in place, keeping its ID and every entry already recorded. Every prompt has a flag equivalent, so it runs with `--no-input`. The token is never accepted as a flag (it would end up in shell history): supply it via `NOTION_TOKEN` (§9). Safe to re-run: existing resources are detected and reused, never duplicated. |
| `tt task list [--project NAME] [--all] [--limit N]` | Lists cached tasks (default: the current project's open tasks, sorted by Order, then name, with tasks that have no Order last) with order, short ID and name. This is how scripts and agents discover task IDs. `--all` includes done tasks. |
| `tt task refresh [--full]` | Refreshes the task cache now, ignoring the TTL, the 2s cap and any back-off. `--full` fetches everything, which also detects deleted or archived tasks. |

The default project is chosen by `init` (`--project`), and re-running `init` changes it.

## 4. Data model

Everything lives in **one SQLite database** (pure-Go driver, no cgo) in the user data directory (for example `~/.local/share/tt/` on Linux). `TT_DB` overrides the path, which suits tests, containers and agents. There is deliberately no separate config file: the context and entries are in the same file, so they cannot drift apart, losing or replacing a config file cannot orphan entries from the context that owns them, and one backup covers everything. Tokens are never stored in the database (§9).

There are six tables: `contexts`, `entries`, `tasks`, `projects`, `cache_state` and `schema_migrations`. Every table that belongs to a context carries a `context_id` that references `contexts(id)`. v1 has a single context, but the column stays so that adding contexts later needs no migration of the other tables. Foreign keys are enforced (`PRAGMA foreign_keys = ON` on every connection). If the database is lost, `init` can rebuild the context, since it detects the Notion resources that already exist; only entries that had not yet synced are gone.

Several `tt` processes can touch the file at once (for example a sync in one terminal while a command runs in another), so the database is opened in WAL mode with a busy timeout. **Every command commits its change locally before any network call**, and no transaction is ever held across one.

**contexts** (exactly one row in v1)

| Column | Notes |
|---|---|
| `id` | UUID generated when the row is created, never changed. Primary key. |
| `tasks_db_id`, `projects_db_id`, `entries_db_id` | The Notion data-source IDs. Null until `init` connects the context. |
| `property_map` | JSON: the names of the task name, project relation, order and done properties. Null until `init`. |
| `default_project_page_id` | The default project; null if none. |
| `created_at` | |

**The context row.** The first migration inserts the single context row, local-only. Each migration re-checks the schema version inside its own `BEGIN IMMEDIATE` transaction, so two processes starting at the same time apply it once and create one row. Its Notion columns are null (`entries_db_id IS NULL`), and that is what **local-only** means; there is no separate flag to fall out of step. So `tt start "reading"` works the first time the binary runs.

What works while the context is local-only: `start` and `stop`, `add`, `edit`, `delete` and `list` with descriptions, and `status`, which exits 0 and says so. What needs Notion fails with `not_initialised` (exit 5) and a hint to run `tt init`: `--task`, `--pick`, `attach`, `task` and `sync`. Entries recorded meanwhile have no task, so they are `waiting`. When `init` connects the context (§5) it keeps the context's ID, so nothing is migrated or copied: the earlier entries are simply there, and `attach --entry` links them to tasks afterwards.

**entries**

| Column | Notes |
|---|---|
| `id` | UUID, generated when the entry is created; stored in Notion as `Local ID`, which is the idempotency key. Not a Notion page ID. |
| `context_id` | The owning context's ID; foreign key to `contexts(id)`. |
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

Short IDs are derived from the set of cached `page_id` values, not stored. Rows with `gone_at` set are kept, so existing entries can still show their task's name, but they are excluded from the picker, `task list` and order-number matching.

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

`init` configures the context. If it is still local-only it is connected in place, keeping its ID and every entry already recorded. Running `init` again on a connected context is safe: it re-detects and reuses what exists.

`init` does the following, checking before creating anything:

1. Validate the token (cheap "who am I" call). If none is available, fail with `no_token` and a hint to set `NOTION_TOKEN`.
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

5. Choose a default project, fill in the context's Notion columns (§4) in one transaction, and do a first full task refresh. Entries recorded before `init` stay as they are, unattached; `init` reports how many there are and suggests `tt attach --entry`.

Finally, `init` prints the steps for adding a rollup to the tasks database by hand (for example `Tracked (min)`: the sum of `Minutes` through the entries relation). Nothing in the tool depends on it.

Notion computes all totals. The tool never writes a duration or total. An existing hand-maintained number such as `Time Logged (min)` is never touched; combining legacy and tracked totals is the user's Notion setup.

**Minutes.** Notion drops the seconds (and milliseconds) from `Start` and `End`: a time sent as 09:01:30 is read back as 09:01:00 (verified in the spike). So the CLI truncates both timestamps to the minute, in UTC, before sending them, and defines an entry's `minutes` as the difference between its minute-truncated start and end. The `Minutes` formula in Notion computes exactly that, so per-entry minutes agree on both sides, and Notion still computes the totals. The exact span is kept locally and reported in JSON as `seconds`; `minutes` can differ from `seconds ÷ 60` by up to a minute either way (an entry from 09:01:50 to 09:02:10 is 20 seconds and 1 minute; one from 09:01:00 to 09:01:59 is 59 seconds and 0 minutes). The error is unbiased, because truncating both ends cancels out on average. An entry that starts and ends within the same minute counts as 0 minutes but is kept. Totals shown by `status` and `list` are the sum of the per-entry minutes, which matches the rollup in Notion. The CLI does the truncation itself rather than leaving it to Notion, so the rule does not depend on Notion's behaviour.

## 6. Sync

### 6.1 What is pushed

A stopped entry **with a task attached**, regardless of whether the task is marked done. Running timers and unattached entries are held back and reported as `waiting`. Tombstoned entries archive their Notion row. A tombstoned entry with no stored page ID is looked up by `Local ID` first: if a row is found it is archived, otherwise the tombstone is dropped locally. Archiving a row that is already archived counts as success (to be confirmed; open question 19).

**What is sent.** `Start` and `End` go as UTC instants (`Z` or `+00:00`), truncated to the minute (§5), and never with a `time_zone` field. Notion keeps the wall-clock digits of a timestamp and swaps its offset for the named zone's, so `08:00:15+00:00` sent with `time_zone: Europe/London` comes back as `08:00:00+01:00`, a different instant; with a non-UTC offset it rejects the request.

### 6.2 Idempotency

Before every create request for an entry without a stored `entry_page_id`, query the entries database by `Local ID`. If a row exists, adopt its page ID and update it; otherwise set `create_attempted_at`, create the row, and immediately record the returned page ID. The lookup is repeated on every retry of a failed create, not only the first attempt: a request that timed out may have succeeded on Notion's side, and creating again without looking would duplicate the row. Entries with a page ID are updated directly. This makes a retry after a crash, a timeout or a cancelled request safe. Notion has no unique constraints, so this guarantee holds for a single writer, which the sync lock enforces (§6.5). It also relies on a filtered query seeing a row that was just created, which the Notion spike confirmed (40 creates, each followed at once by a query on `Local ID`: no misses, no duplicates; open question 17). The query does not return trashed pages, so a row archived in Notion is invisible to this lookup; §6.3 covers that case.

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
| 404 on one entry's task page | Mark that entry `blocked` (`task_not_found`) with the error; continue with the rest. |
| 404 on an entry's own row, or the row is archived (deleted or archived by hand in Notion) | Mark that entry `blocked` (`row_missing`) rather than silently recreating or restoring a row the user may have removed on purpose. Updating an archived page does not return 404: Notion answers 400 `validation_error` ("Can't edit block that is archived"). So on a 400 for an entry that has a stored page ID, the engine retrieves the page and treats it as `row_missing` if it is in the trash and as `bad_request` otherwise; the error text is not relied on (open question 19). `sync --retry-blocked` restores the row and applies the update in one request (`in_trash: false` together with the properties, which Notion accepts, unlike editing the archived page). It uses the stored page ID, because a `Local ID` query cannot find a trashed row. If the page is gone for good (404), it falls back to the `Local ID` lookup and creates the row. |
| 400 on one entry | Mark `blocked` (`bad_request`); continue. |

Blocked entries are not retried on every run. They become pending again when edited or re-attached, or with `sync --retry-blocked`; if one of those retries blocks again, that counts as blocked in that run (§6.4). Ctrl-C cancels the context, lets the in-flight request finish, saves state, and exits cleanly.

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

**Exit codes** for `sync` come from the global scheme in §8. Two are specific to `sync`:

| Code | When |
|---|---|
| 0 | Nothing failed and nothing became blocked in this run. Waiting entries do not count. Entries that were already blocked by an earlier run, and that this run did not touch, do not count either. |
| 1 | The run happened, and at least one entry `failed` or became `blocked` in it (a retry that blocks again counts). |

Exit 4 (`sync_busy`, §6.5), 5 (the run could not start) and 6 (`offline`, §6.3) are as in §8.

So a permanent problem is reported with exit 1 once, when it happens, and a scheduled `tt sync` then goes back to exit 0 rather than looking broken until someone fixes the entry. The blocked entry is not forgotten: it stays in the `still_blocked` count, in the reminder line above, and in `tt status` until it is fixed.

**JSON output** (`--output json`) carries the same information: a `results` array with one object per entry touched in this run (its ID, its state, and an `error` with a stable `code` if it did not sync), plus a `summary` of the counts. `still_blocked` counts entries blocked by earlier runs that this run left alone.

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

### 6.5 One sync at a time, and scheduled sync

**Sync lock.** The guarantee in §6.2 holds for a single writer, so every sync takes a lock before it reads any entry and holds it for the whole run (an advisory lock on a file in the data directory, released by the OS if the process dies). A `tt sync` that finds the lock held fails with `sync_busy` (exit 4). Only `sync` takes the lock: other commands write locally, and WAL lets them run while a sync does (§4).

**Scheduled sync.** For a periodic catch-up (for example after a long offline spell), a cron job or launchd agent running `tt sync --no-input` works as is. It exits 1 once when an entry fails or becomes blocked and 0 afterwards (§6.4), so it does not look permanently broken. If it overlaps a manual sync, one of them exits 4 (`sync_busy`) and the next run picks up whatever is left. The tool does not install the job.

## 7. Command errors vs sync failures

Command errors are local, immediate, and never retried: `stop` with nothing running; `start` while a timer runs without `--switch`; `start` with no description and no task; `--at`, `--ago`, `--start`, `--end` or `--duration` giving an impossible span (an end before its start, or a time in the future); a selector that matches nothing or too much; a cancelled picker (`cancelled`, exit 2, nothing changed); a command that needs Notion run in a context that is not connected yet (`not_initialised`, exit 5, with a hint to run `tt init`); an unusable SQLite file (`database_unusable`, exit 5). Notion being unreachable is never an error for `start`, `stop`, `status`, or `list`, and none of them needs `init`. Only `init` and `sync` require Notion to be reachable. Each error maps to a stable exit code (§8).

## 8. Agent-friendly interface

The CLI is designed to be driven by scripts and AI agents as well as people. The rules below apply to every command.

**Output format.** `--output table|json` (default `table`). Table output is for humans and may change between releases. JSON output is the contract.

**JSON conventions**

- One JSON document on stdout per invocation, with a top-level `schema_version` integer.
- Mutating commands return the resulting object under `entry` (`start`, `stop`, `add`, `attach`, `edit`, `delete`), or per-entry results plus a summary (`sync`), so no follow-up `list` is needed.
- Fields are never omitted: absent values are `null`. IDs are always included. A command's set of keys is fixed, so a key that does not apply to an invocation (such as `stopped` on a plain `start`) is `null`, not missing.
- Timestamps are RFC 3339 in UTC; durations are integer seconds everywhere (the command line still accepts `10m` and `1h30m` as input).
- Key naming: a command that returns many things uses a plural key holding an array (`entries`, `tasks`); one that returns a single thing uses the singular key (`entry`).
- Notices that would otherwise be a line of text (e.g. "using task cache from 3h ago") appear in a `warnings` array, present in every success document except `sync`'s (which reports problems per entry, §6.4) and `[]` when empty. Each warning is an object, `{"code", "message", "details"}`: `code` is stable and documented, `message` is not, and `details` is `null` when there is nothing to add. Overlaps with existing entries (from `start --at/--ago`, `add`, or `edit`) are reported as `overlap` warnings whose `details.entry_ids` lists the overlapping entries. The warning codes are `overlap`, `stale_timer` and `task_cache_stale`.
- Commands with `--limit` (`list`, `task list`) include `"truncated"`, true when the limit cut the result.
- Array order is part of the contract: `list` is chronological, and `task list` is sorted by Order, then name, with tasks that have no Order last.
- `--wide` affects table output only.
- Discovery: agents find task IDs with `tt task list --output json` and entry IDs with `tt list --output json` (or from the response to `start` and `stop`). Every selector accepts the IDs those commands return.

**Entry object.** Wherever an entry is returned it has these fields:

```
{"id": "a1b2c3d4-…", "start": "2026-10-12T09:00:00Z", "end": "2026-10-12T10:12:00Z",
 "seconds": 4320, "minutes": 72, "description": null,
 "task": {"page_id": "…", "short_id": "c8d9e0", "name": "…"},
 "sync_state": "pending", "sync_error": null, "deleted": false}
```

`task` is `null` for an unattached entry. For a running entry, `end`, `seconds` and `minutes` are `null`. `seconds` is the exact span; `minutes` follows the rule in §5 (Minutes).

**Responses by command**

| Command | Document (besides `schema_version`) |
|---|---|
| `start` | `entry`, `stopped` (`null`, or the entry that `--switch` stopped), `warnings` |
| `stop`, `add`, `attach`, `edit` | `entry`, `warnings` |
| `delete` | `dry_run`, `action` (`remove` or `tombstone`), `entry`, `removed` (true if removed outright rather than tombstoned), `warnings`. With `--dry-run`, `dry_run` is true and `removed` is `null`: nothing was changed, so there is no outcome. |
| `status` | `running` (an entry or `null`), `elapsed_seconds` (`null` when nothing is running), `today` (`entries`, `minutes`), `counts` (`pending`, `blocked`, `waiting`), `connected` (boolean), `warnings` (including the stale-timer warning) |
| `list` | `entries` (an array), `total_minutes`, `truncated`, `warnings` |
| `task list` | `tasks` (an array of task items, below), `truncated`, `warnings` |
| `sync` | `results` and `summary` (§6.4) |
| `init`, `task refresh` | the affected objects, named by the key rule above; their fields are fixed when those commands are built |

A collection is always an object holding an array, never a bare array, so it can carry `warnings` and totals.

**Task item.** Wherever a task appears in a list (`task list`, and the `candidates` list in errors), it has these fields:

```
{"page_id": "3f63b23e…", "short_id": "837922", "order": 2.3,
 "name": "Learn: error wrapping", "project": {"page_id": "3e73b23e…", "name": "Go Time Tracker"},
 "done": false}
```

`order` is a number, or `null` when unset (it is not unique). `project` is an object, or `null` for a task with no project.

**`status` and "today".** `elapsed_seconds` is the running entry's elapsed time, measured with the same clock reading as `today.minutes`. A running entry shows `end`, `seconds` and `minutes` as `null`, so this is how a script learns how long it has been running. `today.entries` is a count (`list` returns the entries themselves). `today.minutes` and `list`'s `total_minutes` are the minutes of all finished entries plus the elapsed minutes of the running entry, so the two commands agree. A running entry belongs to the day it started (§3.2), so a timer left running from yesterday is not part of "today"; the `stale_timer` warning flags it. Minutes are summed from each entry's own `minutes` value (§5), and the running entry's is computed the same way with the current time as its end.

**Examples**

`tt start --switch` (the `entry` and `stopped` values are entry objects, shortened here):

```
{"schema_version": 1,
 "entry": {"id": "b2c3d4e5-…", "start": "2026-10-14T10:12:40Z", "end": null, "seconds": null, …},
 "stopped": {"id": "a1b2c3d4-…", "start": "2026-10-14T09:00:12Z", "end": "2026-10-14T10:12:40Z",
             "seconds": 4348, "minutes": 72, …},
 "warnings": []}
```

A plain `tt start` returns the same keys with `"stopped": null`.

`tt delete --dry-run`:

```
{"schema_version": 1, "dry_run": true, "action": "tombstone",
 "entry": {"id": "a1b2c3d4-…", …}, "removed": null, "warnings": []}
```

`tt status` with nothing running, before `init` (exit 0):

```
{"schema_version": 1, "running": null, "elapsed_seconds": null,
 "today": {"entries": 0, "minutes": 0}, "counts": {"pending": 0, "blocked": 0, "waiting": 0},
 "connected": false, "warnings": []}
```

A warning:

```
{"code": "overlap", "message": "overlaps 1 existing entry",
 "details": {"entry_ids": ["9f8e7d6c-…"]}}
```

**Streams.** Data goes to stdout; diagnostics, warnings and errors go to stderr. No colour, spinners or progress output when stdout is not a terminal or `NO_COLOR` is set.

**Errors.** With `--output json`, a failed command (exit code 2 or above) writes `{"schema_version": 1, "error": {"code": "...", "message": "...", "details": {...}}}` to stderr and nothing to stdout. The `code` string is stable and documented; messages are not. For example, `timer_running` carries the running entry in `details`, and `offline` from `sync` carries how many entries were synced before it stopped and how many are still pending. A `sync` that ran but finished with failed or blocked entries (exit 1) has not failed as a command: it writes its normal result document to stdout.

**No prompts.** Prompts (task picker, confirmations, `init` wizard) appear only when both stdin and stdout are terminals and `--no-input` is not set. Otherwise the command never blocks:

- `--pick` (the picker) without a terminal is a usage error. A bare `--task` is also a usage error: it always needs a value.
- An ambiguous selector fails with the candidate list, in `details` for JSON output.
- Destructive commands (`delete`) require `--yes`.
- Every `init` prompt has a flag equivalent (§3.4).

**Exit codes** are global and stable:

| Code | Meaning | Example error codes |
|---|---|---|
| 0 | Success | |
| 1 | Partial failure: `sync` finished, but an entry failed or became blocked in the run | `sync_partial` |
| 2 | Usage error: bad flags or arguments, invalid time, cancelled picker | `usage`, `invalid_time`, `confirmation_required`, `cancelled` |
| 3 | Not found or ambiguous selector | `not_found`, `ambiguous_selector` |
| 4 | State conflict | `timer_running`, `no_timer_running`, `sync_busy` |
| 5 | Config, auth or local-store problem, or context not connected to Notion | `not_initialised`, `no_token`, `access_denied`, `database_too_new`, `database_unusable` |
| 6 | Notion unreachable (`sync` only) | `offline` |
| 10 | Unexpected internal error | `internal` |

`status` exits 0 even when nothing is running or the context is not connected; "nothing running" is a valid answer, not an error.

**Error `details`.** `details` carries structured data a script can act on, and is `null` when there is none; it never repeats the message. The codes that carry it:

| Code | Exit | `details` |
|---|---|---|
| `timer_running` | 4 | `{"entry": <entry object>}`, the timer that is running |
| `offline` | 6 | `{"synced": n, "pending": n}` |
| `ambiguous_selector` | 3 | `{"selector": "...", "candidates": [<task items>]}` |
| `not_found` | 3 | `{"selector": "..."}` |
| `invalid_time` | 2 | `{"input": "..."}` |
| `confirmation_required` | 2 | `{"flag": "--yes"}` |
| `database_too_new` | 5 | `{"database_version": n, "supported_version": n}` |
| `database_unusable` | 5 | `{"path": "..."}`, the database file |

Every other error code in the table above has `"details": null`: `usage`, `cancelled`, `no_timer_running`, `sync_busy`, `not_initialised`, `no_token`, `access_denied` and `internal`. `internal` never includes a stack trace or an underlying cause.

The per-entry codes inside a `sync` result (`rate_limited`, `server_error`, `task_not_found`, `row_missing`, `bad_request`; §6.4) describe one entry's outcome, not a failed command, and never become an exit code. `sync_partial` (exit 1) is not an error document: `sync` finished and writes its normal result to stdout.

**Previewing and bounding output.** `delete --dry-run` reports what would happen without changing anything. `list --limit N` bounds output size. Every command has `--help` text with examples.

## 9. Secrets

The Notion token comes from the `NOTION_TOKEN` environment variable. It is never accepted as a flag (it would end up in shell history) and never stored in the SQLite database or any other file. If it is not set, commands that need it fail with `no_token` (exit 5) and say how to set it. OS keychain storage is deferred (DESIGN-LATER.md).

## 10. Implementation notes

- Go, single static binary; pure-Go SQLite driver.
- CLI framework (Cobra vs stdlib `flag`) is an implementation choice, not a design decision. The design avoids optional-value flags, which pflag does not let take the next argument as their value: `--task` always takes a value and the picker is the separate `--pick` flag (open question 12, resolved). That also works with the stdlib `flag` package.
- Use the Notion API's data-source IDs for schemas and queries, and pin the API version: `Notion-Version: 2025-09-03`. Query with `POST /v1/data_sources/{id}/query`; the older `POST /v1/databases/{id}/query` returns 400 `invalid_request_url`. Store the data-source ID, not only the database ID.
- Keep the Notion client behind an interface so sync logic can be tested against a fake.
- Embed the timezone database (`import _ "time/tzdata"`, about 450 KB) so `TT_TIMEZONE` zone names resolve in minimal containers and the binary stays self-contained.
- Take time from an injectable `Clock` interface, not `time.Now` directly: "no future times", resolving `HH:MM` to today, the stale-timer warning and cache TTLs all need a controllable clock.
- `time.Date` does not guarantee what it returns for a local time that does not exist or occurs twice; detect both by converting the result back and comparing, so an `HH:MM` on a daylight-saving change day fails with `invalid_time` (§3.1). Test on both change days.
- Open SQLite in WAL mode with a busy timeout and foreign keys on, and keep transactions short and local (§4). The first migration creates the single context row, so every command can assume it exists.
- Test the first-run path explicitly: timing before `init`, then `init` connecting the context with its entries intact.
- The sync result needs to tell "blocked in this run" from "was already blocked": have the engine return a per-entry outcome for each entry it touched, and count the untouched blocked entries separately for `still_blocked`. The exit code is then a pure function of those counts, which makes it easy to table-test.
- Have the HTTP client classify every error as network-level (no response) or HTTP-level (a status code) in one place, and keep the consecutive-network-failure counter in the sync engine, not the client. Test the drop-mid-create case with a fake Notion client that creates the row and then fails the response: the next run must find the row by `Local ID` and not duplicate it.
- Truncate timestamps to the minute in one place, the function that builds the Notion payload, and test it: 09:01:50 becomes 09:01:00, a time already on a whole minute is unchanged, and `minutes` comes out right for 31 s, 59 s and 90 s spans and for a pair that straddles a minute boundary (09:01:59 to 09:02:00 is 1 minute). The fake Notion client should reject a payload that contains `time_zone`.
- Test the archived-row paths against the fake client: an update answered with 400 for a trashed page becomes `row_missing`, `--retry-blocked` restores and updates in one request, and archiving an already-archived row counts as success.
- Test the sync lock: a second `tt sync` started while one holds the lock exits 4 without reading any entry.

## 11. Decision log and open questions

Numbers are stable IDs, so references elsewhere (and in Notion task comments) keep working. Questions 10, 11 and 13 moved to [DESIGN-LATER.md](DESIGN-LATER.md) with their features.

**Open for v1**

- **2. Selector that matches nothing in the cache** is currently an error, even if the task was created seconds ago and the refresh timed out. Revisit if this proves annoying.
- **6. Full task refresh cadence** for detecting deleted or archived tasks.
- **7. JSON schema stability:** what bumps `schema_version`, and whether to publish JSON schemas for each command's output (useful for the Claude skill and for contract tests). The shapes themselves are defined in §8.
- **8. Short ID stability:** a displayed task short ID is relative to the cache, so it may need to lengthen as tasks are added. Scripts should store the full page ID from JSON output, which every selector accepts.
- **9. Overlapping entries:** currently allowed with a warning, since the user is the authority on what they did. The alternative is to reject overlaps unless `--force` is given, which prevents double-counted time at the cost of friction. Revisit after real use.
- **19. Archived rows, remaining probe.** The spike did not cover two things that §6.1 and §6.3 rely on. (a) Archiving a page that is already archived (`in_trash: true` on a trashed page). This happens when a `delete`'s archive reached Notion but the local update was lost, and the next sync repeats it. The design treats it as success; confirm that Notion does not answer with an error that needs different handling. (b) Retrieving a trashed page reports it as trashed (`in_trash`), which is how a 400 on update is classified as `row_missing` rather than `bad_request`. A scratch probe against a throwaway entries database settles both.

**Resolved**

- **1. Rollup and `Minutes` formula through the API:** yes to both (spike, 13 Oct). `init` creates the formula; in v1 it prints steps for adding the rollup by hand, and offering to add it is deferred (§5).
- **3. Stale-timer threshold:** fixed at 12 hours in v1 (§3.1); making it configurable is deferred with `tt config`.
- **4. Rounding and date precision:** Notion truncates `Start` and `End` to the minute, so the CLI truncates before sending and defines `minutes` from the truncated endpoints (spike, 13 Oct; §5). The alternatives were the CLI writing `Minutes` itself, which would end Notion computing the totals, or keeping a nearest-minute rule, which would leave the CLI and Notion disagreeing on some entries. Never send `time_zone` (§6.1).
- **5. Time zones:** UTC storage; the system zone or `TT_TIMEZONE` for local input and output; `HH:MM` means today, and fails on daylight-saving gaps and overlaps (§3.1). Still to test: `list` day boundaries on the two change days.
- **12. Task picker flag:** `--task SELECTOR` always takes a value, and the picker is the separate boolean `--pick`, because pflag optional-value flags do not consume the next argument (`tt start --task 4.1` would have taken `4.1` as the description). Cancelling it changes no data and exits 2 as `cancelled` (§3.1).
- **14. Using `tt` before `init`:** the first migration creates the context local-only; timing works without Notion, and `init` connects it in place, keeping its ID and entries (§4, §5).
- **15. Cron and blocked entries:** `sync` exits 1 only when an entry failed or became blocked in that run; entries blocked by earlier runs stay visible as `still_blocked`, in a reminder line and in `tt status` (§6.4).
- **16. Context storage:** the context lives in the SQLite database with the entries, with foreign keys, so there is no separate config file to lose or fall out of step. Tokens stay out of the database (§4, §9).
- **17. Query-after-create consistency:** 40 creates, each followed at once by a filtered query on `Local ID`, gave no misses and no duplicates (spike, 13 Oct), so no post-sync duplicate check is added. This is evidence rather than a guarantee; the failure-matrix tests repeat it under realistic sync load. Editing an archived page fails with 400 `validation_error`, and a trashed page does not appear in a `Local ID` query (§6.2, §6.3).
- **18. Network-failure handling:** network-level failures are counted per request attempt; 3 in a row stop the run with the affected entries left `pending` and exit 6, while `failed` is reserved for 429 and 5xx errors that persist after 5 attempts. The first connection failure of a run aborts at once (§6.3).
