# Working with Claude Code on this repo

## What this is

`tt` (a placeholder name) is a small Go CLI for tracking time against tasks. It works fully offline, stores everything in a local SQLite database, and syncs finished entries to Notion.

## Design

Design for the project lives in [docs/DESIGN.md](docs/DESIGN.md) and should always be the source of truth. It may be changed during the course of the project but only with express permission from the owner.

[docs/DESIGN-LATER.md](docs/DESIGN-LATER.md) holds features deferred from v1. It is not part of v1: don't build or review code against it unless the owner brings a feature back into `DESIGN.md`.

## Authorship

This is a learning project for the author, designed to increase their proficiency with the go language. Therefore, the following rules must be respected:

- All go application code should be author-written. Claude should not write or edit go source files, except for the tests and formatting described below
- Explain go idioms, point to docs, and review the code the author writes
- Small snippets may be generated to illustrate a concept, but nothing that is designed to be copy-pasted into the source code directly
- `go.mod` and `go.sum` are not source files: Claude may manage them (for example by running `go mod init` or `go mod tidy`) when asked
- Formatting: Claude may run `go fmt ./...`, which only changes layout. Nothing else in a go file is changed without being asked, and `golangci-lint run --fix` is not covered by this

### Tests

Claude may write and edit `_test.go` files when the author says what to test, provided the test is a kind the author has already written themselves:

- A kind of test means a technique, not a subject: for example table-driven unit tests, or store tests against a real database in `t.TempDir()`. A new function to test is not a new kind
- Before writing a kind of test the author hasn't written yet (for example the first `httptest` fake, fuzz test, benchmark, golden file or `Example` function), stop and say so. The author writes the first one; after that it's a known kind
- If a test needs a change to application code to be testable, describe the change for the author to make rather than making it
- When handing tests over, list the cases covered so the author can review them

Claude can assist with non-go files such as docs, CI/CD config, commit messages, PR descriptions etc.

## Go conventions

- `context.Context` first
- Wrapped errors with `%w`
- Small interfaces
- table-driven tests
- `httptest` fakes (no live Notion calls in tests)

## Before committing

Run from the repo root, and fix or report anything that fails:

- `go fmt ./...`
- `go mod tidy -diff` (run `go mod tidy` if it reports a difference)
- `go build ./...`
- `go vet ./...`
- `go test ./...`
- `golangci-lint run`

CI runs the same checks (`.github/workflows/ci.yml`).

## Design rules that must not be broken

Details are in [docs/DESIGN.md](docs/DESIGN.md); these are the ones to check code and reviews against.

- The Notion token is never stored in a file or the database, and never accepted as a flag. In v1 it comes from `NOTION_TOKEN` only (§9)
- One writer for sync: every sync takes the sync lock before reading any entry (§6.5)
- Commit locally before any network call, and never hold a transaction across one. A failed sync never loses a change (§4, §6.3)
- JSON output is the contract. Table output is for humans and may change (§8)
