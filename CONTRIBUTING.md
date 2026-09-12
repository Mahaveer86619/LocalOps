# Contributing to LocalOps

Thanks for taking a look. This started as a personal project to keep tabs
on one Linux box, so it's intentionally small — that's the bar for
contributions too: see [§25 of the design doc](docs/DESIGN.md#25-explicit-non-goals)
before proposing anything that starts to look like Kubernetes, a
workflow engine, or a monitoring platform. If in doubt, open an issue to
discuss before writing code.

## Dev setup

Requires Go 1.25+. No external services needed — SQLite is embedded
(pure Go, no cgo, via `modernc.org/sqlite`) and the whole repo is one Go
module.

```bash
git clone https://github.com/Mahaveer86619/LocalOps
cd LocalOps
go build ./...
go run ./cmd/localops     # starts on :7717, ./localops.db
```

In another shell:

```bash
go run ./cmd/localops-cli status
curl -s localhost:7717/health
```

## Project layout

See [CLAUDE.md](CLAUDE.md) for a directory-by-directory map and the
non-obvious constraints (nullable-column scanning, why migrations live
where they do, the SDK's no-op contract). Read that before touching
`internal/tasks/store.go`, `internal/watchers/store.go`, or `client/`.

## Before opening a PR

```bash
gofmt -l .        # should print nothing
go vet ./...
go build ./...
```

There's no test suite yet — a good first contribution is one, targeting
`internal/*/store.go` against a real `:memory:` SQLite DB
(`storage.Open(":memory:")`) rather than mocking `database/sql`. If your
change touches behavior, a test covering it is appreciated but not
required to open a PR — happy to iterate in review.

## Ground rules

- **Keep the client SDK (`client/`) dependency-free and panic/error-free
  from the caller's perspective.** Every method must be safe to call when
  LocalOps is disabled or unreachable — that's the entire value
  proposition (see README "Zero risk to production"). If your change
  makes any `client.*` call able to return an error, block indefinitely,
  or panic, it needs to go back to the drawing board.
- **No new dependencies without a reason in the PR description.** The
  project already leans on Echo, `modernc.org/sqlite`, and gopsutil for
  the server; the client stays stdlib-only. Adding another dependency to
  `client/` in particular should be rare.
- **LocalOps tracks and alerts; it doesn't execute or remediate** (design
  doc §3). A PR that has LocalOps itself restart a tunnel, retry a
  connection, or run application logic is out of scope by design, not by
  oversight.
- Small, focused PRs over large ones. If a change spans the API, the
  store, and a script, that's fine — just keep the diff to one concern.

## Reporting bugs / requesting features

Open an issue. For bugs, include what you ran (`localops`/`localops-cli`
version or commit, OS/arch) and the actual vs. expected behavior. For
features, a concrete use case beats a general request — this project
grows by "I needed to track X," not by speculative generality.
