# Contributing

Small project, small PRs please.

The most useful thing to add is support for another agent CLI
(gemini-cli, opencode, aider, ...). Parsers are additive: you shouldn't
need to touch existing code paths.

A parser is three functions:

1. **detect**: a branch in `procs.KindOf()` mapping the process command
   line to your kind string. First match wins, so put specific names
   before generic ones. If your tool is a wrapper that doesn't write
   session files, add it to `procs.WrapperKinds` and stop here.
2. **locate**: find the session file for a process. Look at
   `procs.LocateCodex` for the shape: it takes `(home, cwd, start)` and
   returns `(path, sure)`. Only return `sure=true` when the evidence is
   exact; anything less marks the row with a `?`. (Claude is
   special-cased as a batch pass in `procs.PairClaude` because several
   processes can share one project dir.)
3. **update**: fold one parsed JSONL line into the session entry. Look
   at `updateCodex` in `internal/parse`. Every field is optional and
   missing values render as a dim `-`. Register the function in the
   `updaters` map in `internal/parse/entry.go`.

**Optional, store-backed agents (build tags).** Most agents write JSONL
transcripts and slot into the three functions above with no new
dependencies. An agent whose canonical store is something heavier — e.g.
Hermes, which keeps sessions in SQLite — must stay **opt-in** so the
default binary stays small and dependency-free. Such an adapter lives in
files tagged `//go:build hermes` (with a matching `!hermes` stub only
where a symbol must exist in both builds) and registers through the core
extension hooks: `procs.RegisterKind`/`RegisterRuntime`,
`board.RegisterExternalKind`/`RegisterExternalPair`/`RegisterExternalResume`,
`history.RegisterSource`/`RegisterResume`, and
`render.RegisterExternalTurns`. The default `go build` compiles none of it
and links no SQLite; opt in with `go build -tags hermes`.

Ground rules:

- never fail on a weird line, just return; the cell stays `-`. The fuzz
  target (`FuzzApply`) enforces no-panic; run it before sending a PR
- your update function only sees appended bytes, don't reread files
- bump `ParserV` when you extract a new field, so caches rescan once
- no network calls at runtime, ever: everything comes from local files
  and /proc. The single permitted socket is the local docker unix
  socket. CI enforces this with an strace smoke test over a one-shot run
- dependency budget: `bubbletea`, `lipgloss`, `bubbles`,
  `golang.org/x/term` (plus their transitive closure). Anything else
  needs a written justification in the PR. JSON parsing is stdlib
  `encoding/json`. No cgo: `CGO_ENABLED=0` always. The one carve-out is
  `modernc.org/sqlite` (pure Go, read-only), pulled in **only** under
  `-tags hermes` — it never enters the default build or its binary.
  `go mod tidy` keeps it (it reads the tagged files), so the manifest
  stays honest either way
- `--json` schema_version 1 is frozen; new fields require schema_version
  2 and a discussion first

Add a fixture under `internal/parse/testdata/` (made-up data only: no
real prompts, keys, or paths) and a Go test per behavior you claim.
`go test ./...`, `go vet ./...` and `tools/parity.sh` have to pass.

The legacy bash implementation in `legacy/agentdash.sh` is frozen apart
from bug fixes; its embedded Python is stdlib-only and must never open
a network connection (PRs adding pip dependencies will be rejected).
Its bats suite under `tests/` still runs in CI.
