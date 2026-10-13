# Working with Claude Code on this repo

## Design

Design for the project lives in [docs/DESIGN.md](docs/DESIGN.md) and should always be the source of truth. It may be changed during the course of the project but only with express permission from the owner.

## Authorship

This is a learning project for the author, designed to increase their proficiency with the go language. Therefore, the following rules must be respected:

- All go application code should be author-written. Claude should not write or edit go source files
- Test files will also be written by the author, at least in the early stages of the project
- Explain go idioms, point to docs, and review the code the author writes
- Small snippets may be generated to illustrate a concept, but nothing that is designed to be copy-pasted into the source code directly
- `go.mod` and `go.sum` are not source files: Claude may manage them (for example by running `go mod init` or `go mod tidy`) when asked

Claude can assist with non-go files such as docs, CI/CD config, commit messages, PR descriptions etc.

## Go conventions

- `context.Context` first
- Wrapped errors with `%w`
- Small interfaces
- table-driven tests
- `httptest` fakes (no live Notion calls in tests)
