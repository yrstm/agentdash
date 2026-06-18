#!/usr/bin/env bash
# bench/latency.sh <binary-under-test>
#
# Measurement 6: change-to-screen latency. Runs the binary in watch mode
# inside a throwaway tmux session, appends an entry to a session file, and
# polls `tmux capture-pane` at 50ms until the row flips waiting -> working.
#
# Requires the bench env (source bench/env.sh first): it targets the
# "-work-web" fixture session whose task text contains "migrate".
set -euo pipefail

BIN=${1:?usage: bench/latency.sh <binary-under-test>}
BIN=$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT=${BENCH_OUT:-$ROOT/bench/out}
TRIALS=${BENCH_LATENCY_TRIALS:-5}
mkdir -p "$OUT"

# the bench env shims tmux on PATH; use the real one on a private socket
TMUX_BIN=${TMUX_BIN:-$(PATH=/usr/bin:/usr/local/bin:/bin command -v tmux)}
SOCK=agentdash-bench
TARGET="$HOME/.claude/projects/-work-web/s2.jsonl"
[ -f "$TARGET" ] || { echo "latency.sh: bench env not set up (source bench/env.sh)" >&2; exit 1; }

tm() { "$TMUX_BIN" -L "$SOCK" "$@"; }
row() { tm capture-pane -p -t bench 2>/dev/null | grep migrate || true; }

cleanup() { tm kill-server 2>/dev/null || true; }
trap cleanup EXIT
cleanup

tm new-session -d -s bench -x 220 -y 50 \
  "env HOME='$HOME' PATH='$PATH' AGENTDASH_WORKING_SECS=$AGENTDASH_WORKING_SECS \
   AGENTDASH_IDLE_SECS=$AGENTDASH_IDLE_SECS AGENTDASH_SKIP_DOCKER=1 \
   AGENTDASH_PROC_ROOT='${AGENTDASH_PROC_ROOT:-}' '$BIN' -w 5"

wait_state() { # wait_state <word> <timeout-ms>; echoes ms waited, fails on timeout
  local want=$1 budget=$2 t0 now
  t0=$(date +%s%3N)
  while :; do
    case "$(row)" in *"$want"*) date +%s%3N; return 0 ;; esac
    now=$(date +%s%3N)
    [ $((now - t0)) -gt "$budget" ] && return 1
    sleep 0.05
  done
}

mark() { # append a fresh assistant entry; the row must flip to working
  printf '{"type":"assistant","timestamp":"%s","message":{"id":"msg_lat_%s","model":"claude-opus-4-8","content":[{"type":"text","text":"latency marker"}],"usage":{"output_tokens":1}}}\n' \
    "$(date -u +%FT%T.000Z)" "$1" >> "$TARGET"
}

# Precondition: the board must actually render the target agent row at all.
# If it never does, the binary paired no agent and every trial would soft-skip
# into an empty result. The usual cause is a binary that reads the real /proc
# (v1) being benched against agents that only exist in a fake /proc: a fake
# /proc helps only a binary that honors AGENTDASH_PROC_ROOT (v2). Fail loudly
# here instead of emitting "board never showed waiting" eight times.
touch -d '20 minutes ago' "$TARGET"
wait_state migrate 15000 >/dev/null || {
  echo "latency.sh: board never rendered the target agent row within 15s." >&2
  echo "  The binary under test paired no agent for $TARGET." >&2
  echo "  If it reads the real /proc (v1), the synthetic agents must exist" >&2
  echo "  there; a fake /proc only helps a binary honoring AGENTDASH_PROC_ROOT." >&2
  exit 1
}

echo "trial  latency_ms" | tee "$OUT/latency.txt"
total=0 n=0
for t in $(seq 1 "$TRIALS"); do
  touch -d '20 minutes ago' "$TARGET"
  wait_state waiting 15000 >/dev/null \
    || { echo "trial $t: board never showed waiting" >&2; continue; }
  # "waiting" appears right after a refresh: without a random phase the
  # append would always land at the same point in the poll interval and
  # every trial would measure the same (worst) case
  sleep "0.$((RANDOM % 10))"; sleep $((RANDOM % 5))
  t0=$(date +%s%3N)
  mark "$t"
  t1=$(wait_state working 15000) \
    || { echo "trial $t: board never showed working" >&2; continue; }
  dt=$((t1 - t0))
  echo "$t      $dt" | tee -a "$OUT/latency.txt"
  total=$((total + dt)); n=$((n + 1))
done
if [ "$n" -gt 0 ]; then
  echo "avg    $((total / n))" | tee -a "$OUT/latency.txt"
else
  echo "latency.sh: 0/$TRIALS trials succeeded; no latency recorded" \
    | tee -a "$OUT/latency.txt" >&2
  exit 1
fi
