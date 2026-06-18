#!/usr/bin/env bash
# bench/run.sh <binary-under-test>
#
# Captures the agentdash performance baseline in the *current* environment.
# For a reproducible synthetic box, `source bench/env.sh` first; on a live
# box just run it as-is and note the live-agent count in the results doc.
#
# Knobs:
#   BENCH_OUT        output dir (default: <repo>/bench/out)
#   BENCH_IDLE_SECS  idle watch-mode measurement window (default 600)
#
# Measurements (numbers refer to docs/benchmarks/*.md):
#   1 one-shot render, warm cache          (hyperfine)
#   2 one-shot render, cold cache + MB/s   (hyperfine, cache removed per run)
#   3 execve count per frame               (strace)
#   4 idle watch-mode CPU                  (/proc stat delta over the process
#                                           tree + pidstat for the record)
#   5 peak memory                          (PSS sampled from smaps_rollup)
#   8 docker section latency               (skip-docker vs not)
# 6 (change-to-screen latency) lives in bench/latency.sh.
# 7 (clean-container footprint) lives in bench/container-footprint.sh.
set -euo pipefail

BIN=${1:?usage: bench/run.sh <binary-under-test>}
BIN=$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT=${BENCH_OUT:-$ROOT/bench/out}
IDLE_SECS=${BENCH_IDLE_SECS:-600}
mkdir -p "$OUT"

say() { printf '== %s\n' "$*"; }
note() { printf '%s\n' "$*" | tee -a "$OUT/summary.txt"; }

: > "$OUT/summary.txt"
note "binary: $BIN"
note "date: $(date -u +%FT%TZ)"
note "host: $(uname -srm)"

# ---- corpus size (for MB/s) -------------------------------------------------
corpus_bytes=0
for d in "$HOME/.claude/projects" "$HOME/.codex/sessions"; do
  [ -d "$d" ] && corpus_bytes=$((corpus_bytes + $(du -sb "$d" | cut -f1)))
done
note "corpus bytes: $corpus_bytes"

# ---- 0: sanity — does the binary render without erroring? -------------------
# A binary that crashes mid-render (e.g. an embedded parser dying on the
# fixtures) still "runs" and gets timed, but the numbers are meaningless: it
# never parses the corpus. Catch that here instead of publishing it.
prime_err=$("$BIN" 2>&1 >/dev/null) || true
if [ -n "$prime_err" ]; then
  note "WARNING: '$BIN' wrote to stderr on a plain render — likely crashing,"
  note "WARNING: so the numbers below do NOT reflect real work. First lines:"
  printf '%s\n' "$prime_err" | head -3 | sed 's/^/  /' | tee -a "$OUT/summary.txt"
fi

# ---- 1: one-shot, warm cache ------------------------------------------------
say "one-shot, warm cache"
"$BIN" >/dev/null 2>&1 || true   # prime the cache
hyperfine --warmup 3 "$BIN" \
  --export-markdown "$OUT/oneshot-warm.md" --export-json "$OUT/oneshot-warm.json"
warm_mean=$(jq -r '.results[0].mean' "$OUT/oneshot-warm.json")
note "one-shot warm mean s: $warm_mean"

# ---- 2: one-shot, cold cache ------------------------------------------------
say "one-shot, cold cache"
hyperfine --prepare "rm -f $HOME/.cache/agentdash/usage.json" "$BIN" \
  --export-markdown "$OUT/oneshot-cold.md" --export-json "$OUT/oneshot-cold.json"
cold_mean=$(jq -r '.results[0].mean' "$OUT/oneshot-cold.json")
note "one-shot cold mean s: $cold_mean"
if [ "$corpus_bytes" -gt 0 ]; then
  note "cold parse throughput MB/s: $(awk -v b="$corpus_bytes" -v t="$cold_mean" \
    'BEGIN { printf "%.1f", b / 1048576 / t }')"
fi

# ---- 3: execve count per frame ----------------------------------------------
say "execve count per frame"
strace -f -e trace=execve -o "$OUT/execve.log" "$BIN" >/dev/null 2>&1 || true
execs=$(grep -c 'execve(' "$OUT/execve.log" || true)
note "execve per frame: $execs"

# ---- 4: idle watch-mode CPU over $IDLE_SECS s --------------------------------
# The watch loop forks python3 every refresh and reaps it, so the parent's
# cutime/cstime accumulates the whole tree: a stat delta is exact.
say "idle watch CPU ($IDLE_SECS s; this is the long one)"
tree_ticks() { # utime+stime+cutime+cstime, comm-safe
  sed 's/^[0-9]* ([^)]*) //' "/proc/$1/stat" | awk '{print $12+$13+$14+$15}'
}
setsid "$BIN" -w 5 >/dev/null 2>&1 </dev/null &
WPID=$!
sleep 2   # let the first frame settle before sampling
t0=$(tree_ticks "$WPID")
pidstat -p "$WPID" 5 $((IDLE_SECS / 5)) > "$OUT/idle-cpu.txt" 2>&1 &
PIDSTAT=$!
sleep "$IDLE_SECS"
t1=$(tree_ticks "$WPID")
kill "$PIDSTAT" 2>/dev/null || true
kill -TERM -- -"$WPID" 2>/dev/null || true
wait "$WPID" 2>/dev/null || true
clk=$(getconf CLK_TCK)
note "idle watch CPU % (tree, ${IDLE_SECS}s): $(awk -v d=$((t1 - t0)) -v c="$clk" -v s="$IDLE_SECS" \
  'BEGIN { printf "%.2f", d / c / s * 100 }')"

# ---- 5: peak memory during a cold-cache render -------------------------------
# Sum memory across the subtree rooted at the launched pid. We match by walking
# each candidate's ppid chain up to the launched pid rather than by process
# group: setsid forks on some hosts, which orphaned the pgid and made this
# report 0 (no match) or a bogus tiny value (partial match). Walking ppid needs
# no setsid and still captures the short-lived children (the python parser)
# alive at sample time.
# Prefer PSS (smaps_rollup); fall back to RSS (VmRSS in /proc/PID/status, always
# present) where it is not readable, and label which we got. A run that matches
# no process or can read no figure says "NOT MEASURED" instead of printing 0,
# which used to look like a real result.
say "peak memory (cold cache)"
rm -f "$HOME/.cache/agentdash/usage.json"
"$BIN" >/dev/null 2>&1 </dev/null &
MPID=$!
# smaps_rollup (PSS) needs PTRACE_MODE_READ: on hosts with
# kernel.yama.ptrace_scope >= 2 a *child's* rollup is unreadable even though
# our own is fine, which silently zeroed PSS. Decide PSS-vs-RSS by what we can
# actually read for the launched child, not self; VmRSS in status has no such
# gate. For true PSS comparable to the baseline, rerun with ptrace_scope=0.
sleep 0.02
mem_kind=Pss
awk '/^Pss:/{f=1} END{exit !f}' "/proc/$MPID/smaps_rollup" 2>/dev/null || mem_kind=Rss
# Map pid->ppid with the read builtin (no forks) once per sample, then walk
# ancestry in memory. The previous version forked `cat` per ancestor per pid;
# on a busy box a single scan outlived the ~200 ms render, so every sample saw
# only the reaped zombie and peak came out 0. ppid is the field after the
# state, reached by stripping through the last ") " (robust to ')' in comm,
# e.g. systemd's "(sd-pam)") and then the state.
peak=0 matched=0
while kill -0 "$MPID" 2>/dev/null; do
  unset PPMAP; declare -A PPMAP=()
  for d in /proc/[0-9]*; do
    read -r line 2>/dev/null < "$d/stat" || continue
    rest=${line##*) }; rest=${rest#* }
    PPMAP[${d#/proc/}]=${rest%% *}
  done
  s=0
  for pid in "${!PPMAP[@]}"; do
    p=$pid hops=0 hit=
    while [ "${p:-0}" -gt 1 ] && [ "$hops" -lt 64 ]; do
      [ "$p" = "$MPID" ] && { hit=1; break; }
      p=${PPMAP[$p]:-0}; hops=$((hops + 1))
    done
    [ -n "$hit" ] || continue
    matched=1
    if [ "$mem_kind" = Pss ]; then
      m=$(awk '/^Pss:/ {print $2}' "/proc/$pid/smaps_rollup" 2>/dev/null) || continue
    else
      m=$(awk '/^VmRSS:/ {print $2}' "/proc/$pid/status" 2>/dev/null) || continue
    fi
    s=$((s + ${m:-0}))
  done
  [ "$s" -gt "$peak" ] && peak=$s
  sleep 0.02
done
wait "$MPID" 2>/dev/null || true
if [ "$matched" = 0 ]; then
  note "peak memory: NOT MEASURED (no proc in subtree of pid=$MPID; /proc restricted?)"
elif [ "$peak" = 0 ]; then
  note "peak memory: NOT MEASURED ($mem_kind unreadable under this /proc)"
else
  note "peak $mem_kind kB (cold render, tree): $peak"
fi

# ---- 8: docker section latency ----------------------------------------------
say "docker section latency"
if docker info >/dev/null 2>&1; then
  hyperfine "AGENTDASH_SKIP_DOCKER=1 $BIN" "$BIN" \
    --export-markdown "$OUT/docker-latency.md" --export-json "$OUT/docker-latency.json"
  note "docker skip mean s: $(jq -r '.results[0].mean' "$OUT/docker-latency.json")"
  note "docker on mean s:   $(jq -r '.results[1].mean' "$OUT/docker-latency.json")"
else
  note "docker section latency: not collected (no docker daemon on this box)"
fi

say "done; summary:"
cat "$OUT/summary.txt"
