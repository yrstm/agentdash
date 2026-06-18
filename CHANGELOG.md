# Changelog

## Unreleased

- Event hooks: `agentdash -w --on-needs-you <cmd>` and `--on-stuck <cmd>` run
  a command of yours once when an agent enters a needs-you state or goes
  `stuck?`. The agent row arrives as JSON on stdin (same shape as `--json`),
  with `AGENTDASH_EVENT`/`PID`/`TASK` in the env; commands run non-blocking
  and time-bounded. The binary still opens no socket — your command is what
  reaches the network. Foundation for desktop/phone/Slack notifiers; an
  example lives in `integrations/notify-example.sh`.
- `docs/agent-interface.md`: how another agent drives the fleet through the
  existing CLI (`--json`, `show`, `why`, `resume`) — a paste-ready skill, no
  MCP required.

- LOGIN SESSIONS is legible now. A dropped SSH login that lingers in utmp with
  no live process on its tty is labeled `(stale)` (dimmed) instead of a cryptic
  `.`; a shell running tmux shows the session it is driving (`tmux·apifix`)
  instead of a bare `tmux`; and a new FROM column shows the originating host so
  otherwise-identical shells are distinguishable.

## v2.3.0: 2026-06-16

- A violet ANSI-Shadow `AGENTDASH` launch banner with a one-glance HUD
  (agents supervised · active · idle, context held-idle vs burning, health)
  now heads the one-shot board on a TTY. Skipped entirely under `--plain`,
  `--json`, or when piped; falls back to a one-line wordmark on narrow panes.

## v2.2.2: 2026-06-16

- Collapse an agent's launcher and the real process it spawns into one row:
  a `node /usr/bin/codex` wrapper plus the vendored codex binary it execs were
  showing as two identical rows per chat. A process whose child is another
  agent of the same kind is now treated as the launcher and dropped.

## v2.2.1: 2026-06-15

- Stop listing codex's sandboxed tool-call subprocesses (`codex-linux-sandbox`
  / `bwrap`) as agents. Each codex tool call spawns a sandbox process whose
  command line contains "codex", so a single chat showed as a wall of
  `(no session)` codex rows. The board now shows the real codex chats.
- Grade the `stuck?` status by how long it has been quiet: a user/tool turn
  silent under `AGENTDASH_STUCK_SECS` (default 90s) now shows a soft `busy?`
  (dim, counts as working) instead of red `stuck?`, fixing the documented
  "silent over a minute = stuck?" false positive. `stuck?` is reserved for
  genuinely long-quiet turns.
- Header now splits context pressure: alongside `ctx held idle` (reclaimable,
  yellow when nothing is working) it shows `ctx burning` — context held by
  actively-running agents, red when a crash-loop is climbing it (the
  overnight-bill case).
- `--notify` now only pings when an *unwatched* agent (no one attached to its
  pane) flips to needs-you — no notification for agents you're already
  watching.

## v2.1.1: 2026-06-15

- `agentdash -w` no longer emits ANSI color when stdout isn't a terminal
  (piped/redirected headless mode), matching the one-shot path.

## v2.1.0: 2026-06-15

- `AGENTDASH_EXPAND=1` starts the board expanded, as if `-a` were passed
  (carried over from the v1 bash line).
- Orphaned wrapper detection: a `bash -c`/`nohup` launcher left running
  with no controlling tty after its agent has exited now surfaces in a
  ZOMBIES & ORPHANS section (carried over from the v1 bash line).
- Identical agent rows (same kind/model/tokens/status/task/cwd) collapse
  into a single `kind ×N` line so the board stays readable; press `a` (or
  pass `-a`) to expand them back to individual processes.
- Watch mode no longer garbles (repeated footer/status lines piling up) on
  resize: it forces a full repaint on each resize and clips every line to
  the pane width, so a too-wide line can't wrap and the alt-screen renderer
  stays in sync.

- Header decomposes "need you" into `N looping` (crash-loop, red) and
  `N blocked` (waiting on you, yellow), and turns `ctx held idle` yellow
  when nothing is working — so the counts say *what kind* at a glance.
- TASK column is tidied for display: scraped whitespace collapses and an
  unpaired row reads `(no session)`. (The raw task is unchanged in `--json`.)

## v2.0.0: 2026-06-11

Rewrite as a single static Go binary; the v1 bash implementation is
preserved, working, in `legacy/agentdash.sh`.

- Zero runtime dependencies: /proc is read directly (procps and
  iproute2 forks gone, python3 gone). tmux remains an exec boundary;
  docker is queried over its local unix socket with cgroup memory reads
  instead of the ~2s `docker stats` sample.
- Interactive watch mode (bubbletea): foreground sampling with a 1s
  /proc tick for process changes; no daemon, listener, or file watcher
  remains after the UI exits.
- New watch capabilities: `/` filter, `o` sort cycle (urgency,
  last-write, tokens, uptime), `?` help overlay, viewport scrolling,
  resize-aware layout, mouse wheel and click (off under `--plain`),
  `AGENTDASH_PROC_TICK`.
- Everything else preserved: every flag, subcommand and key, the visual
  language, `--json` schema_version 1 (enforced byte-for-byte against
  the legacy script by `tools/parity.sh` in CI), the cache file (a v1
  cache loads without a rescan), the context-windows conf and its
  self-learning, the status machine and pairing evidence chain.
- Distribution: goreleaser builds linux amd64/arm64 binaries with
  checksums and the brew formula on tag push; `install.sh` curl-pipe
  installer with checksum verification; `go install` works.
- Benchmarks before and after under `docs/benchmarks/`; the suite in
  `bench/` reruns on any box.

## v1.1.0: 2026-06-11

- One python3 spawn per frame, not two: the header's idle-context figure
  is now formatted in bash instead of a stray `python3 -c`.
- Clear startup error when python3 is missing, instead of a mid-render
  failure.
- README: corrected the "single bash script" description (it is one file,
  two languages), added Design notes on why the JSONL parsing is not bash
  regex or jq, and documented the incremental byte-offset scanner and the
  five-tier pid-to-session evidence chain.
- CONTRIBUTING: hard rule that the embedded Python stays stdlib-only and
  never opens a network connection.
- Reproducible benchmark suite under `bench/` and the v1 baseline numbers
  in `docs/benchmarks/v1-baseline.md`.

## v1.0.0: 2026-06-11

First release.

- Agent table: claude and codex sessions, wrapper processes, tmux
  sessions, logins, docker sandboxes, listening ports, zombies.
- Session pairing by an evidence chain: fd scan, unique-cwd match,
  start-time match, then a sticky guess marked `?`.
- Status machine: working / waiting / stuck? / idle / respawn ×N; a red
  `○` is an agent waiting with nobody attached.
- TOKENS deduped by message id, CTX bar with self-learning context
  windows (persisted to conf), ACT activity sparkline, LAST write age.
- Urgency-sorted stable rows; unenrichable rows and healthy sections
  collapse into an `ok:` line; `-a` expands, `-l` long view.
- Interactive watch mode (`-w`): a cursor selects a row; g jumps to its
  tmux pane, s/y/r open drill-down, provenance and resume panels over
  the board, L edits the label, t/l/a toggle views, q quits.
- Tree view (`-t` or the `t` key) groups agents under the wrapper
  process that spawned them, via the ppid chain.
- Subcommands: go, show, why, label, resume, recap. Flags: `--json`
  (schema_version 1), `--any-waiting`, `--plain`, `--notify` (OSC 9).
- Incremental byte-offset cache (0600, atomic, 7-day prune).
- shellcheck-clean; bats suite over made-up fixtures; vhs demo tape.
