# v1 baseline (bash + embedded python)

Performance baseline of agentdash v1.0.0 (commit b57acca), captured before
any v1.1/v2 work. The same `bench/run.sh` methodology reruns against the v2
binary for the after-comparison.

## Environment

| | |
|---|---|
| binary under test | `agentdash` v1.0.0 (frozen copy of commit b57acca) |
| kernel | Linux 6.18.5 x86_64 |
| distro | Ubuntu 24.04.4 LTS |
| CPU | Intel Xeon @ 2.10GHz, 4 cores |
| RAM | 15 GiB |
| bash | 5.2.21 |
| python3 | 3.11.15 |
| hyperfine | 1.18.0 |
| tmux | 3.4 |

## Corpus and agents

This box has no live agents, so the run used the deterministic synthetic
environment from `bench/env.sh` (the demo fixture box plus an inflated
corpus). That makes the numbers reproducible by anyone: `source bench/env.sh
&& bench/run.sh <binary>`.

| | |
|---|---|
| live agents on the board | 4 (2 claude, 1 codex, 1 hermes wrapper; shimmed pids) |
| `~/.claude/projects` | 2 sessions, 20.97 MB |
| `~/.codex/sessions` | 1 rollout, 5.24 MB |
| total parse corpus | 26,215,767 bytes (~25 MB) |
| docker | CLI present, **no daemon** on this box |

## Results

| # | metric | result |
|---|---|---|
| 1 | one-shot render, warm cache | 174.4 ms ± 4.3 ms (17 runs) |
| 2 | one-shot render, cold cache | 562.0 ms ± 52.6 ms (10 runs) |
| 2 | cold parse throughput | 44.5 MB/s |
| 3 | execve count per frame | 327 |
| 4 | idle watch CPU, 10 min avg (process tree) | 3.65 % |
| 5 | peak PSS, cold render (process tree) | 38,820 kB (~38 MB) |
| 6 | change-to-screen latency (watch, 5s interval) | avg 2,296 ms (8 trials, 1,158 to 4,788 ms) |
| 7 | clean-container footprint | not collected: no docker daemon (see below) |
| 8 | docker section added latency | not collected: no docker daemon (see below) |

### Notes per measurement

1. **Warm cache** (`bench/out/oneshot-warm.md`): the steady-state cost of one
   frame; the incremental scanner means almost no JSONL is re-parsed.
2. **Cold cache** (`bench/out/oneshot-cold.md`): `usage.json` removed before
   each run, so the full 25 MB corpus is parsed. Throughput = corpus bytes /
   mean wall time, so it understates the parser itself (the same wall second
   also covers process discovery and rendering).
3. **execve per frame**: every fork the script makes for a single board
   render (`tput`, `pgrep`, `ps`, `readlink`, `python3`, `tmux`, `ss`, `w`,
   `sort`, `sed`, `awk`, ...). Raw log in `bench/out/execve.log`.
4. **Idle watch CPU**: `-w 5` run headless for 600 s; CPU measured as the
   utime+stime+cutime+cstime delta of the watch process (children are reaped,
   so the parent's counters cover the whole tree, including the python3
   spawned every refresh). `pidstat` record in `bench/out/idle-cpu.txt`.
5. **Peak PSS**: sum of `smaps_rollup` Pss over the process group, sampled
   every 50 ms during a cold-cache render.
6. **Latency** (`bench/latency.sh`): an entry is appended to a session file
   while watch mode runs in tmux; time until `capture-pane` shows the row
   flip waiting -> working, 50 ms polling, append phase randomized within
   the poll interval. v1 polls on a 5 s interval, so the expected average
   is ~2.5 s; measured 2.3 s matches.
7. **Clean container**: this box has a docker CLI but no daemon, so the
   alpine measurement could not run here. `bench/container-footprint.sh`
   performs it on a docker-capable host. Known failure mode without deps:
   alpine has no bash, so the script dies at the shebang
   (`/usr/bin/env: 'bash': No such file or directory`); after
   `apk add bash python3 procps iproute2` it needs python3 (~50 MB class
   install on alpine) plus procps/iproute2 for `ps`/`pgrep`/`ss`.
   Rerun the script for the measured delta.
8. **Docker section**: same daemon limitation. The known cost on a docker
   box is the `docker stats --no-stream` sample (~2 s wall), which v1
   prefetches in the background to overlap with the scan.

## Reproduce

```sh
source bench/env.sh            # deterministic fake box + 25 MB corpus
bench/run.sh ./agentdash       # measurements 1-5, 8
bench/latency.sh ./agentdash   # measurement 6 (needs tmux)
bench/container-footprint.sh ./agentdash   # measurement 7 (needs docker)
```

`BENCH_IDLE_SECS` shortens the 10-minute idle window for quick iterations;
published numbers use the default 600.
