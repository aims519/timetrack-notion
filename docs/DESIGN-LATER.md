# timetrack-notion — Deferred Design

Features designed for `tt` but deferred from v1. [DESIGN.md](DESIGN.md) is the source of truth for v1; nothing here applies until a feature is brought back into it.

The text below was moved out of DESIGN.md when v1 was cut down, and is kept close to how it was written. Section references (§) point to DESIGN.md. Where a passage relies on something v1 left out, that is noted. Bringing a feature back means merging its text into DESIGN.md and checking each of the "touches" notes.

## 1. Automatic sync after changes

**Touches:** §1, §3.1 (`start --switch`), §3.3, §6.3, §7, §8 (the `sync` key, its example and the `sync_note` warning), and the settings in §4 of this file. v1 already has the sync lock (DESIGN.md §6.5); auto-sync adds the "skip quietly when the lock is held" rule below.

By default, a command that leaves entries waiting to be pushed makes one short, best-effort sync attempt after it has saved its change locally. This is what "background sync" means here: no daemon and no separate process, just the same process finishing its work. Manual `tt sync` stays available and works exactly as in §6.1 to §6.4.

The commands that change entries (`stop`, `add`, `edit`, `delete`, `attach`, and `start --switch`) also make a short best-effort sync attempt afterwards unless it is turned off; `--sync` / `--no-sync` override the setting for one command.

**Which commands.** `stop`, `add`, `edit`, `delete` and `attach`, and `start --switch` when the entry it stopped has a task: the commands that create or change a pushable entry. A plain `start`, `status`, `list` and the read-only commands never sync. `start --switch` accepts `--sync` / `--no-sync` and returns the `sync` object for the entry it stopped; a plain `start` has neither the flags nor a `sync` object (its `sync` key is `null`).

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

`result` is one of `synced`, `pending` (offline, timed out or stopped early), `failed`, `blocked`, `waiting` (no task attached), or `disabled`. `pending` in the object is the number of entries still waiting to be pushed after the attempt. Details of any problem go in `warnings`, with the code `sync_note`. Delete's `sync` object reports the archive of the Notion row.

**JSON contract changes.** The `sync` key is added to the documents of `start` (`null` unless `--switch`), `stop`, `add`, `attach`, `edit` and `delete` (`null` with `--dry-run`). `"sync": null` means the command never syncs; a `sync` object with `"attempted": false` means it would have but did not. Adding the key is backwards-compatible. The `tt start --switch` example gains:

```
 "sync": {"attempted": true, "result": "synced", "pending": 0},
```

**Control.**
- Setting `auto_sync` (boolean, default `true`) and `auto_sync_timeout` (duration, default `5s`); see §4 of this file.
- Per command: `--sync` and `--no-sync` on the commands listed above override the setting.
- Per environment: `TT_AUTO_SYNC=false` (or `true`) overrides the setting for a run, which suits scripts and containers. Precedence: flag, then environment variable, then setting.

**Sync lock.** Automatic sync that finds the lock held skips quietly. A manual `tt sync` that finds it held fails with `sync_busy` (exit 4), as in v1, because the user asked for it explicitly.

**Command errors (§7).** The automatic sync after a change never turns into a command error: when it cannot complete, the entries stay pending and the command still succeeds. Exit 6 is for manual `sync` only; automatic sync never changes another command's exit code.

**Open question 11. Automatic sync details:** is 5s the right default limit, and 1s for connecting? Should `start` also flush older pending entries opportunistically (it would add latency to the command that should be fastest)? Should the sync lock be a file lock or a row in SQLite (v1 uses a file lock)? Test that a cancelled create followed by a sync never duplicates a row.

## 2. Multiple contexts

kubectl-style contexts, each pointing at a Notion workspace/project set.

**Touches:** §1, §2 (Context), §3 (global flags), §3.1 (one timer across all contexts), §3.4, §4 (`contexts` columns, the default-context race), §5 (which context `init` configures), §8 (the `status` context object, `context` and `project` documents, `context delete --yes`), §10. Comes with §3 of this file (keychain), since a token per context needs somewhere to live.

**Concept.** A **Context** is a named configuration, stored in the same SQLite database as the entries it owns: Notion token reference, tasks/projects/entries databases, property mapping, default project. Identified by a stable generated ID; the name is only a display label, so renaming never orphans data. Only **one timer runs at a time, across all contexts**; `start` errors if a timer is running in any context, naming it.

**Global flag.** `--context <name-or-id>` overrides the current context.

**Commands**

| Command | Behaviour |
|---|---|
| `tt context list \| current \| use <name>` | List (current marked `*`, with each context's ID and whether it is connected), print, or switch the current context. Wherever a context is named (`--context`, `use`, `set`, `delete`), the name or the ID is accepted. |
| `tt context set <name> [--project <name>] [--rename <new-name>]` | Creates or edits a context, found by name or ID. `--rename` changes the display label only; the ID, entries and cache are untouched. Names must be unique. |
| `tt context delete <name> [--yes]` | Removes the context and everything that belongs to it (entries, cached tasks and projects) in one transaction, then its keychain token. Confirms first (or requires `--yes`); warns about unsynced entries; refuses if the running timer belongs to it, and refuses to delete the current context (switch first), so there is always a current one. |
| `tt project list \| current \| use <name>` | List cached projects, show, or set the current context's default project. Shortcut for `context set --project`. |
| `tt init --context <name>` | See "Which context `init` configures" below. |

**Data model additions to `contexts`**

| Column | Notes |
|---|---|
| `name` | Unique display label; can be renamed. |
| `is_current` | 1 for the current context. A partial unique index (`ON contexts (is_current) WHERE is_current = 1`) allows at most one, and `context use` switches it in a single transaction. The first context is created current, and `context delete` refuses to delete the current one, so there is always exactly one. |

A context's data is deleted explicitly, in one transaction, by `context delete`. The `id` is also the name of the context's keychain entry (§3 of this file).

**The default context.** The first command to run against a database with no contexts creates one named `default` and makes it current. This happens after the migrations, inside a `BEGIN IMMEDIATE` transaction that re-checks the table, so two processes starting at the same time create one. (v1 instead has the first migration insert its single row. When contexts arrive, a migration adds `name` and `is_current` to that row, naming it `default`.)

**Which context `init` configures.** In order: the context named by `--context`, if it exists; otherwise the local-only `default` context, which is adopted and renamed to the `--context` name if one was given (it keeps its ID and entries); otherwise a new context is created. Without `--context`, it configures the current context. Running `init` again on a connected context is safe: it re-detects and reuses what exists.

**JSON.** `status` returns `context` (`id`, `name`, `connected`) in place of v1's `connected`. The `context` and `project` commands return the affected objects under `contexts`, `context`, `projects` or `project`; their fields are fixed when those commands are built. Destructive commands that require `--yes` include `context delete`.

**Implementation notes.** Test two processes starting at once against a fresh database: one `default` context.

## 3. OS keychain

**Touches:** §3.4 (`init --store-token`), §4 (`contexts.id`), §9, §10.

`tt init --store-token` saves the token supplied via `NOTION_TOKEN` to the keychain.

Token lookup order: `NOTION_TOKEN` environment variable, then the OS keychain, behind a small `SecretStore` interface (keychain implementation plus an in-memory fake for tests). The keychain entry is named by the context's stable ID, so renaming a context never loses its token. No plaintext file fallback, and no token in the SQLite database: if neither source is available, commands that need the token fail with a clear message. On Linux the keychain needs a running Secret Service, so headless use relies on the environment variable. `NOTION_TOKEN` applies to whichever context is current, so with contexts in different Notion workspaces, store each token in the keychain instead.

## 4. Settings and `tt config`

**Touches:** §3.1 (`status`), §3.4, §4 (a seventh table), §8 (the `config` document).

| Command | Behaviour |
|---|---|
| `tt config list \| get <key> \| set <key> <value>` | Reads and writes the global settings. `set` validates the value: an unknown key, a malformed duration, or an unknown timezone fails with `usage` (exit 2). Supports `--output json`. |

**settings** (global, not per context), managed with `tt config`. One row per key (`key` primary key, `value` text); a key without a row has its default. `tt config set` validates before it writes.

- `stale_timer_threshold`: a duration, default `12h`. `status` warns when the running timer is older than this. (v1 hardcodes 12h.)
- `timezone`: an IANA zone name such as `Europe/London`, defaulting to the system zone. The `TT_TIMEZONE` environment variable overrides it for a single run, which suits agents and containers (usually UTC). (v1 has `TT_TIMEZONE` and the system zone only.)
- `auto_sync`: boolean, default `true`. See §1 of this file.
- `auto_sync_timeout`: a duration, default `5s`. See §1 of this file.

**JSON.** `config list | get | set` returns `settings` (every key for `list`, defaults filled in; just the key concerned for `get` and `set`) and `warnings`. Durations are integer seconds, including in `config` output; the command line still accepts `5s` and `12h` as input.

## 5. `HH:MM` across days, and daylight saving

**Touches:** §3.1 (Time arguments, Time zone), §8 (two warning codes), §10 (`Clock`, `time.Date`). v1 keeps `HH:MM` meaning today only, and fails with `invalid_time` on daylight-saving gaps and overlaps.

An `HH:MM` is read in the configured timezone and means **the most recent such time that is not in the future**: today if that time has already been reached, otherwise yesterday. So at 09:00, `stop --at 18:00` closes a timer left running overnight at 18:00 yesterday, and at 00:10, `start --at 23:50` means 23:50 the previous evening. It never reaches back more than 24 hours; for anything earlier, use an RFC 3339 timestamp. Whenever an `HH:MM` resolves to yesterday, the command says so (a line in table mode, an entry in `warnings` in JSON) and the echoed entry shows the full date, so a mistyped time is visible straight away.

An `--end` given as `HH:MM` together with `--start` is read relative to the start instead: it means the first such time after the start. So `add --start 23:00 --end 01:00` logs a two-hour entry across midnight, and `add --start 09:00 --end 10:00` an hour the same day. If that moment is in the future (for example `--start 16:00 --end 09:00` at 16:55, which would end tomorrow), the command fails with `invalid_time`.

Around daylight saving changes, a local time that does not exist (such as 01:30 on the spring-forward night) fails with `invalid_time`; it does not fall back to another day. A local time that occurs twice (autumn) resolves to its first occurrence and adds a warning; pass an RFC 3339 timestamp with an offset to mean the other one.

**JSON.** Warning codes `time_resolved_to_yesterday` and `ambiguous_local_time`.

**Implementation notes.** The `Clock` covers the most-recent-`HH:MM` rule. Cover the `time.Date` checks with tests on both DST change days, including `HH:MM` resolving to yesterday across a change.

**Open question 13. `HH:MM` resolution:** `HH:MM` means the most recent such time that is not in the future (today, else yesterday, never further back), echoed with a warning when it lands on yesterday. An `--end` given with `--start` means the first such time after the start, so entries across midnight work. The same DST rules apply.

## 6. Matching tasks by name

**Touches:** §3.1 (task selector), §4 (`tasks.gone_at`), §8 (`not_found` details, task items), §10. v1 keeps full IDs, short IDs, order numbers and the picker.

The selector gains a fourth rule, after the order number:

4. **Words from the task name**, matched within the current project's open tasks (see the matching rules below).

**Name matching (rule 4).** Lexical only; no fuzzy or semantic matching.

- The query and each task name are lower-cased, stripped of accents, and split into words at every non-alphanumeric character. So `agent-friendly` is the words `agent` and `friendly`, and `--json` is `json`. A query with no words left after this is a usage error (it would otherwise match every task).
- A task is a candidate if **every query word matches the start of some word** in its name, in any order. `agent json` finds "Design CLI to be agent-friendly (--json, …)", and `back` finds "Background sync".
- Matching is by word start, not substring, so `test` does not match "httptest".
- If any candidate matches every query word as a **whole word**, only those candidates are kept. This stops `cli` from also returning "client" when real CLI tasks exist, while `back` still finds "Background sync" when nothing matches whole.
- A task whose normalised name equals the normalised query wins outright.
- One remaining candidate resolves; several are ambiguous; none is `not_found`.
- Done tasks are excluded from order and name matching unless `--all` is given. A full or short ID always resolves, whether or not the task is done.

Tasks with `gone_at` set are excluded from name matching.

**Matches in other projects.** A selector that fails with `not_found` lists matches from other projects in the context, if any, with their project names. In JSON, `not_found` details become `{"selector": "...", "elsewhere": [<task items>]}`, `[]` if none, and task items appear in the `elsewhere` list as well as `candidates`.

**Agents.** Order and name matching exist for people. Scripts and agents should still pass full page IDs.

**Implementation notes.** Task name matching sits behind a small `Matcher` interface (lexical at first), so fuzzy suggestions or an opt-in semantic matcher can be added later without touching the commands.

**Open question 10. Beyond lexical matching:** typo-tolerant "did you mean" suggestions on `not_found` (suggest only, never auto-select) are cheap in pure Go and are planned for after name matching. Semantic matching would need a local embedding model (breaking the pure-Go static binary) or a remote API (breaking offline-first), and agents already do this job themselves from `tt task list`, so it stays a stretch experiment.

## 7. `sync --dry-run` and the schema check

**Touches:** §3.3, §5, §6.3, §6.4, §8 (`schema_mismatch`).

`tt sync --dry-run` reports what would be pushed, updated, archived or skipped, without writing to Notion or changing local state. In table output the status words become `would push`, `would update` and `would archive`, and nothing is sent. In JSON the document has `"dry_run": true` and the states are `would_push`, `would_update` and `would_archive`. `--dry-run` always exits 0.

`sync` re-validates the entries database schema before pushing, so renamed or deleted properties produce a clear error instead of a cryptic API failure.

| Failure | Handling |
|---|---|
| Schema mismatch | Abort before pushing anything. |

**JSON.** Error code `schema_mismatch` (exit 5). Its `details` are `null` for now; what they should carry depends on how the schema check is done.

## 8. `sync --strict`

**Touches:** §3.3, §6.4, §6.5, §8 (exit code 1).

`--strict` changes only the exit code: it exits 1 whenever any entry is blocked, not just entries that failed or became blocked in this run. A monitoring check or CI job that wants the persistent signal uses `--strict`; a scheduled `tt sync` can add it if the job should keep flagging blocked entries. Exit code 1 then reads: "`sync` finished, but an entry failed or became blocked in the run (with `--strict`, or any entry is blocked)". The sync exit code stays a pure function of the per-run counts, `still_blocked` and `--strict`.

## 9. Rollup offer in `init`

**Touches:** §3.4 (`init` flags), §5. v1 prints the manual steps instead.

`tt init` gains `--add-rollup | --no-add-rollup`, and step 5 becomes:

Offer to add a **rollup** on the tasks database (e.g. `Tracked (min)`: sum of `Minutes` via the entries relation). The API can create the rollup, as it can the `Minutes` formula (verified in the spike); because the rollup is added to the user's own tasks database, it is offered and never added without asking. Detection is structural (a rollup through that relation, over `Minutes`, with `sum`), not by name. An existing property is never modified; a name clash with a different shape offers a different name or skipping. Declining prints manual steps, and nothing in the tool depends on the rollup.

## 10. Indented JSON output

**Touches:** §3 (global flags), §8 (Conventions).

v1 prints JSON compactly, on one line. A flag such as `--indent` (or an `--output json-pretty` value) would print the same document indented, for reading by eye. It changes formatting only, never the content, so the JSON contract is unaffected. Until then, pipe the output through `jq`.
