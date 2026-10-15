# Design markup guide

Temporary file for the `docs/design-markup` branch. Delete it, and the markers in `DESIGN.md`, when the split is done.

This branch is based on `feature/output-contract` (a66d447), so it includes the §8 contract changes from `9d80a23`. `DESIGN.md` here differs from that commit only by review markers. No design text has been cut, moved or reworded.

## Reading the markers

| Marker | Meaning |
|---|---|
| `MOVE→LATER` | Cut and paste into `DESIGN-LATER.md`. `BEGIN` and `END` lines wrap a block; a tag on the end of a table row or bullet means that whole row or bullet. |
| `TRIM` | Stays in `DESIGN.md`, but the parts named in the marker go (or are reworded). |
| `DEDUP` | Say it once; point to the canonical place. |
| `FIX` | An inconsistency to correct. |
| `DECIDE` | Your call; the marker says what each choice costs. |
| `KEEP` | An exception inside something that moves, or the canonical copy of something repeated. |
| `ADD` | Text to write. |

Every marker contains the text `[MARKUP`, so they are easy to find (`grep -n 'MARKUP' docs/DESIGN.md`). To strip them all (this restores the original text, apart from a stray blank line or two):

```
grep -v '^> \*\*\[MARKUP' docs/DESIGN.md | sed -E 's/ \*\*\[MARKUP[^]]*\]\*\*//g' | cat -s
```

## What moves

| Feature | Where it appears | Notion task it matches |
|---|---|---|
| Automatic sync and the sync lock | 1, 3.1 (`start --switch`), 3.3, 4 (settings), 6.2, 6.3, **6.5**, 7, 8 (the `sync` key, its examples, `sync_busy`, `sync_note`), open question 11 | Automatic best-effort sync |
| Multiple contexts | 1, 2, 3 (`--context`), 3.4 (`context`, `project`), 4 (`name`, `is_current`, default-context race), 5, 8, 10 | Multiple contexts and OS keychain |
| Keychain | 3.4 (`--store-token`), 4 (`id`), 9, `SecretStore` | Multiple contexts and OS keychain |
| `tt config` and the settings table | 3.1 (`status`), 3.4, 4, 8 (the `config` row) | none yet |
| `HH:MM` input, yesterday rule, DST | 3.1 (Time arguments, Time zone), 8 (two warning codes), 10 (`time.Date`, Clock), open question 13 | DST edge cases and HH:MM-means-yesterday rule |
| Order-number and name matching, `--pick` | 3.1 (selector rules 3 and 4, Name matching, the `--pick` sentences), 3.2, 8 (`elsewhere`), 10 (`Matcher`), open question 10 | Task selector: order numbers, name matching and --pick |
| `sync --dry-run` and the schema check | 3.3, 5, 6.3, 6.4, 8 (`schema_mismatch` details) | Sync --dry-run and schema check |
| Rollup offer in `init` | 3.4, 5 (step 5) | none yet |

Whole blocks, rows and bullets marked `MOVE→LATER` come to about 2,100 words of the 11,000. The `TRIM` markers take out more from inside paragraphs and rows. Expect v1 to end up at roughly 7,500 to 8,000 words, not the 6,000 I first guessed.

Most of the new §8 contract text stays. The parts tied to deferred features are the `sync` key and its examples, `delete --dry-run`, the `config` row, the `elsewhere` lists, two warning codes (`time_resolved_to_yesterday`, `ambiguous_local_time`) plus `sync_note`, and the `sync_busy` and `schema_mismatch` rows.

## Decisions for you

1. **`CLAUDE.md` cites the sections that move.** Its "Design rules that must not be broken" list says every sync takes the sync lock (6.5), that a failed sync never loses a change (6.5), and that the token comes from `NOTION_TOKEN` or the OS keychain (9). If the lock and the keychain go to LATER, those rules need rewording. Either keep a minimal lock in v1 (an advisory file lock is small, without the auto-sync around it), or state the single-writer assumption as a known limitation. `CLAUDE.md` says design changes need your express permission, so I have not touched it.
2. **The picker.** With `--pick` and the interactive attach deferred, attaching a task means copying a short ID from `tt task list`. That suits agents, but it is the clumsiest part for you. Deferring name and order matching but keeping the picker is a reasonable middle.
3. **One `contexts` row or none.** I suggest keeping the table with a single row (the first migration inserts it). That removes the creation race and its test, and `context_id` stays on every table.
4. **`HH:MM` at all?** If you want it in v1, keep only "today, else `invalid_time`".
5. **The `sync` key in JSON.** With auto-sync deferred, the entry commands have nothing to put in `sync`. Dropping the key now is safe, because adding a key to a JSON document later does not break readers. Keeping `"sync": null` as a reserved slot is also fine, but §8 then needs to say what the key means.
6. **`TT_TIMEZONE`, `--strict`, `list --week/--project/--wide`, `delete --dry-run`, the rollup offer.** Small each; the markers say what each saves.

## Code already on this branch that the cut touches

I have not changed any Go. If the markers are followed, these will no longer match §8, so they are worth a look when you get there:

- `internal/output/sync.go` and `documents.go`: the `Sync` object and `SyncResult` (including `disabled`), and `EntryDoc.Sync`.
- `internal/output/warning.go`: `TimeResolvedToYesterday`, `AmbiguousLocalTime` and `SyncNote`.
- `internal/clierr/clierr.go`: `SyncBusy` and `SchemaMismatch`.

Unused constants cost nothing, so keeping them is fine. The point is that the contract text and the types should agree.

## After you cut

- Section references (`§6.5`, `§3.4` and so on) and open-question numbers are cited across the doc. Search for `§` and `open question` after moving things.
- The Comments on several Notion tasks cite `DESIGN.md` sections. They need the new numbering.
- The Claude Project holds an older copy of `DESIGN.md` (its title differs, and its §8 predates the contract changes), so reconcile or retire it. `CLAUDE.md` names `docs/DESIGN.md` as the source of truth.
