```
 █████╗  ██████╗ ███████╗███╗   ██╗████████╗██████╗  █████╗ ███████╗██╗  ██╗
██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝██╔══██╗██╔══██╗██╔════╝██║  ██║
███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║   ██║  ██║███████║███████╗███████║
██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║   ██║  ██║██╔══██║╚════██║██╔══██║
██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║   ██████╔╝██║  ██║███████║██║  ██║
╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝   ╚═════╝ ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝
```

# agentdash

`w`, but for coding agents. agentdash prints a table of the agent
processes on a Linux or macOS box: what each one is working on, what
model it is on, how full its context is, and whether it is blocked
waiting on you. `-w` turns the table into a small interactive TUI.

It is a single static binary (no cgo, no runtime services), reading
the session files agent CLIs already write locally (Claude Code and
Codex are supported; adding another agent is a small parser, see
CONTRIBUTING.md) and the OS process table directly — `/proc` on Linux,
`ps`/`lsof` on macOS. No daemon, no server, no API calls, no telemetry,
no file watcher, and it never launches or manages sessions. Watch mode
samples foreground state on the TUI refresh tick, like `w` or `htop`. I
wrote it because I kept losing track of agents across tmux sessions;
maybe it is useful to you too. Runs on Linux and macOS.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/yrstm/agentdash/main/install.sh | sh
```

or grab the binary directly (`linux-amd64`, `linux-arm64`,
`darwin-amd64`, `darwin-arm64`):

```sh
curl -fsSLo ~/.local/bin/agentdash https://github.com/yrstm/agentdash/releases/latest/download/agentdash-linux-amd64
chmod +x ~/.local/bin/agentdash
```

or with Homebrew (Linux and macOS), or go install:

```sh
brew install yrstm/agentdash/agentdash
go install github.com/yrstm/agentdash/cmd/agentdash@latest
```

It is a single static binary with no cgo and no runtime services.
Optional at runtime: tmux (pane jumping, attachment glyphs), docker
(sandbox section, skipped if absent), jq (only for the integrations).
On macOS, `lsof` (shipped with the OS) is used to read process working
directories, open session files, and listening ports — the roles
`/proc` fills on Linux; the ports and sandbox sections degrade
gracefully if it is unavailable. The auditable v1 bash version lives in
`legacy/` and keeps working (Linux only).

Dependency footprint (measured with `go list -buildvcs=false`; absolute package
counts vary a little by Go toolchain): the default build is **~201 compiled
packages, 15 non-stdlib, 6 third-party modules**, against a **30-module
resolution graph** (`go list -m all`). Watch mode is a small raw-terminal loop —
no Bubble Tea / Charm stack — so the only third-party code in the default binary
is `go-runewidth` (Unicode column alignment) plus `golang.org/x/term`/`x/sys`.
`-tags=hermes` adds the pure-Go modernc SQLite driver. Note: the default build
does **not compile** modernc, but `modernc.org/sqlite` is a direct `go.mod`
requirement, so it **remains in the module resolution graph** until Hermes is
split into its own module.

### Optional: Hermes (SQLite-backed agents)

Claude and Codex write JSONL transcripts and work out of the box. Hermes
keeps its sessions in a SQLite `state.db`, so support for it is an **opt-in
build** — the default binary stays small and links no database driver:

```sh
go build -tags hermes -o agentdash ./cmd/agentdash
```

The tagged build reads `~/.hermes/state.db` (and `profiles/*/state.db`)
read-only, pairing a live Hermes process by `HERMES_SESSION_ID` when it is
exported, otherwise by cwd and start time. Nothing about the default build
changes.

## Usage

```
agentdash              print the table once
agentdash -w [secs]    watch mode (default 5s), keys below
agentdash -a           expand collapsed rows and healthy sections
agentdash -l           long view: adds PID, TTY, UP columns
agentdash -t           tree view: group agents under the wrapper that spawned them
agentdash --json       machine-readable output (schema_version 1)
agentdash --plain      no color, no glyphs; NO_COLOR is honored
agentdash --any-waiting   exit 0 if anything needs you

agentdash go [row|pid]     jump to that agent's tmux pane
agentdash show <row|pid>   drill-down with recent turns and resume command
agentdash why <row|pid>    where every value on the row came from
agentdash label <row|pid> "text"   pin a task label
agentdash resume <row|pid> print the resume command for a session
agentdash recap [4h]       what changed since you last looked
agentdash memory [repo|.]  agent-memory drift and change history (--json for tooling)
```

### Memory drift (`agentdash memory`)

In plain terms: agentdash quietly watches the memory files your agents rely on
(`CLAUDE.md`, `AGENTS.md`) and tells you when a project's memory has gone stale
or out of step with recent work — so you can spot an agent running on outdated
notes before it bites you.

Agents accumulate durable memory in repo-root `CLAUDE.md` / `AGENTS.md`.
Every normal `agentdash` run opportunistically samples those files for the
projects that have a live session (an mtime/size check short-circuits before
hashing, so steady-state cost is tiny) and appends to an append-only,
never-pruned log at `~/.cache/agentdash/memory-log.jsonl` — but only when the
content hash actually changes, so a same-size edit is still recorded.

`agentdash memory` shows a cross-project board ranked by how far each
project's memory trails its recent work (git commit time and dirty-tree state
when available, else file mtime), flags `stale` and — when a memory change
landed while two or more live sessions shared the project — `⚠concurrent`
(a mechanical risk signal, not proven authorship). `agentdash memory <repo|.>`
prints that project's change log, newest last, each entry labelled
`baseline` (the first time agentdash observed the file — it did not create it),
`grew`, `shrunk`, or `same-size-rewrite`. `--json` emits the same data as a
stable `schema_version: 1` document (cross-project board, or per-project log).

**Exactly what it samples.** Repo-root `CLAUDE.md` and `AGENTS.md` only — no
recursive crawl, no other markdown, no subdirectories. It reads each file only
to hash it; **file contents are never stored.** Each appended log row holds:
`project` path, `path`, `kind`, `bytes`, `sha256`, `mtime`, sample `ts`, and the
live-`sessions` count at sample time — metadata and a hash, nothing more.

**Log location & retention.** The log is a single append-only JSONL file at:

```
~/.cache/agentdash/memory-log.jsonl
```

It is **never pruned** — that is the point (long-term history) — so it grows by
one line per real content change. It records no file contents, but project paths
and names can themselves be sensitive; the file stays local and is yours to
delete. (A future `agentdash memory compact` / `--forget <repo>` could trim it;
not built yet.)

Local only, no network, read-only toward your files; the scope is deliberately
tight (repo-root files, never a filesystem crawl).

### Updating

agentdash has no self-update command — the binary never touches the network.
Instead the plain board shows the running binary's build age (from its embedded
VCS stamp) and, once it crosses `AGENTDASH_STALE_DAYS` (default 14), *prints* the
reinstall command — carrying the build tags it was built with, so a Hermes build
tells you to reinstall with `-tags=hermes`. You run it yourself:

```
go install -tags=hermes github.com/yrstm/agentdash/cmd/agentdash@main
```

### Watch mode keys

Watch mode has a cursor (`▸`); keys act on the selected row. Panels open
over the board and any key returns to it.

```
j/k or arrows   move the cursor
tab             switch between Agents and History
g               jump to the agent's tmux pane
s               drill-down panel: recent turns, session path, resume command
y               provenance panel: where each value on the row came from
L               edit the task label
r               show the resume command
/               filter rows across task, cwd, model, status (Esc clears)
o               cycle sort: urgency, last-write, tokens, uptime
History view: s details, r resume, i read/command disclosure
t               toggle tree view        l   toggle long view
a               toggle expanded view    ?   help
mouse           wheel scrolls, click selects (off under --plain)
q               quit
```

The board refreshes on foreground TUI ticks. Process starts and exits
are picked up by a cheap /proc scan every second (`AGENTDASH_PROC_TICK`);
the full board and History view resample on the watch interval.

## Event hooks

Watch mode can run a command of yours when an agent changes state, so you
can fire-and-forget an agent and be reached only when it actually needs
you — a desktop toast, a phone push, a Slack message, a log line:

```
agentdash -w --on-needs-you <cmd>   run <cmd> when an agent enters a needs-you state
agentdash -w --on-stuck <cmd>       run <cmd> when an agent's status becomes stuck?
```

The command runs through `sh -c`, so quote it as one argument. It fires
once on the *transition into* the state (not repeatedly while it lasts),
and only with `-w`. agentdash itself opens no socket and makes no network
call anywhere; your command is what reaches out.

Each invocation receives the agent as JSON on **stdin** — one object,
byte-identical to an entry in the `agents` array of `agentdash --json`,
wrapped in an envelope:

```json
{"event":"needs_you","ts":1718566400,"attached":false,"agent":{"agent":"claude","pid":4123,"needs_you":true,"status":"waiting","cwd":"/home/dev/code/app-be","task":"rebase the feature branch",…}}
```

`attached` is whether someone is on its tmux pane (so a notifier can stay
quiet for agents you are already watching). For quick shell one-liners the
headline fields are also in the environment: `AGENTDASH_EVENT`,
`AGENTDASH_PID`, `AGENTDASH_TASK`. A `stuck?` transition counts as
needs-you too, so with both hooks set it fires both events; deduplicate on
`event` if that matters. Commands run non-blocking and are bounded by a
10s timeout, so a slow or wedged hook never stalls the board.

A minimal example notifier lives in `integrations/notify-example.sh`:

```sh
agentdash -w --on-needs-you 'integrations/notify-example.sh'
```

To let another agent read and triage the fleet through the same CLI (no
MCP needed), see [docs/agent-interface.md](docs/agent-interface.md).

## The table

```
devbox 14:02 · 2 need you · 3 working · 6 idle · 8m ctx held idle · load 0.27

  AGENT    LAST  MODEL      TOKENS     CTX        ACT      STATUS     CWD        TASK
▸ claude ○ 4m    opus-4-8   34m/359k   ▓▓▓░░  45%   ▅▂▁    waiting    ~/c/api    flaky checkout test
  claude ● 12s   fable-5    18m/285k   ▓▓░░░  35%   ▆█▇▅   working    ~          migrate the queue
  + 2 wrappers · 1 unmatched (-a to list)

  ok: tmux ×4 · logins ×2 · sandboxes ×3 · ports ×4 · no zombies
```

Rows sort by urgency and keep their order between refreshes. Healthy
sections collapse into the `ok:` line and expand only when something is
flagged; pass `-a` (or set `AGENTDASH_EXPAND=1`) to always expand
everything. Defunct (zombie) processes and orphaned wrapper processes — a
`bash -c`/`nohup` launcher still running with no controlling tty after its
agent has exited — surface in a ZOMBIES & ORPHANS section when present.
Glyphs: `●` tmux attached, `○` detached; a red `○` means the agent is
waiting and nobody is attached.

Columns: LAST is time since the session file was written. TOKENS is
cumulative input/output, where input includes cache reads and writes (it
measures context fill, not billing). CTX is the last request against the
model's context window, yellow at 70%, red at 85%. ACT is bytes appended
per refresh over the last 8 intervals. TASK is your label, else the
session's summary, else the first prompt; a trailing `?` means the
process-to-session pairing was heuristic.

Colors carry one meaning each: green working, yellow worth a look, red
needs you now, dim ignorable. A healthy board is almost colorless.

Tree view (`-t`, or `t` in watch mode) regroups the rows so an agent sits
under the wrapper process that spawned it, found by walking the ppid
chain. Urgency order still applies to the top level.

## Design notes

agentdash started life as a bash script with an embedded stdlib-only
Python parser: session files are JSONL with nested, escaped content
(prompts hold quotes, unicode, pasted code), which bash regex cannot
parse correctly, and jq would have been a lateral dependency swap that
loses the stateful incremental scan. v1 proved the heuristics in
bash+python; v2 compiles them. The genre's reference tools (`w`,
`htop`) are compiled C for a reason: the Go version reads /proc and
the session files directly, drops the procps/iproute2/python3
dependencies entirely, and keeps watch mode as a foreground sampler
with no daemon, listener, or file watcher. The v1 script is preserved, working, in
`legacy/agentdash.sh`; `tools/parity.sh` holds the two implementations
to identical `--json` output. The only socket the tool will ever open
is the local docker unix socket for the sandboxes section, and CI
enforces that with strace.

The scanner is incremental by byte offset: each render stats every
paired session file and parses only the bytes appended since the last
look, folding them into a cached per-session entry. Only complete lines
are consumed, since an agent may be mid-write; a partial tail line
waits for the next refresh. That is why a board over gigabytes of
session history costs milliseconds once the cache is warm.

Pairing a pid to a session file never guesses silently. A five-tier
evidence chain runs from exact to heuristic, records which tier fired,
and anything below the confident tiers is marked with a `?` on the
board. `agentdash why <row>` prints the recorded evidence for every
value on the row.

## Performance

Measured with the reproducible suite in `bench/` (4-core container,
25 MB session corpus, methodology and caveats in
[docs/benchmarks/](docs/benchmarks/v2-comparison.md)):

| metric | v1 (bash+python) | v2 (Go) |
|---|---|---|
| one-shot render, warm cache | 174 ms | 15 ms |
| one-shot render, cold cache (25 MB) | 562 ms | 368 ms |
| idle watch CPU, 10 min avg | 3.65 % | 0.29 % |
| session write to screen | ~2.3 s | ~80 ms |
| peak memory (PSS), cold render | 38 MB | 17 MB |
| dependencies on a clean box | bash python3 procps iproute2 | none |

## How values are derived

Pairing a process to a session file walks an evidence chain: an open fd
in /proc, then a unique session file in the project dir for the process
cwd, then a first-entry timestamp within 5 minutes of process start,
then a sticky previous guess, then the newest unclaimed recent file
(the last two are heuristic and marked `?`). Codex sessions pair on an
open rollout fd first (exact, and the only thing that catches a
`codex resume`, whose rollout filename keeps its original start time),
otherwise on the cwd and start time recorded in the rollout file.
`agentdash why <row>` prints which tier applied.

Status: file written under 60s ago is `working`; over 10 minutes quiet is
`idle`; in between, `waiting` if the last entry is an assistant turn, else
it is graded by how long it has been quiet on a user/tool entry: `busy?`
(dim) under `AGENTDASH_STUCK_SECS` (default 90s) — likely a slow tool call,
not alarming — and `stuck?` (red) past it. This grading replaces the old
flat "silent over a minute = stuck?" false positive. `respawn ×N` means
three or more fresh pids on one session file within 10 minutes. Thresholds
are configurable via `AGENTDASH_WORKING_SECS`, `AGENTDASH_STUCK_SECS` and
`AGENTDASH_IDLE_SECS`.

Context windows come from `~/.config/agentdash/context-windows.conf`
(`<model-id-substring> <window-tokens>`, first match wins), then built-in
defaults, then self-correction: if observed context exceeds the assumed
window, the larger tier is adopted and written back to the conf.

Port flags: `NEW` is first seen since the previous run, `dup-cwd` is two
listeners in one project directory, `no-agent` is a tty-less listener in a
project directory no agent or tmux pane is using.

## Privacy

The TASK column shows prompt text and the cache at
`~/.cache/agentdash/usage.json` persists it (mode 0600). Mind
screen-sharing and log shipping.

## License

MIT.
