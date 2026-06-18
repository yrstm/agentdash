#!/usr/bin/env bash
# Source this to build a deterministic benchmark box: the demo fixture
# environment (fake agents via command shims, fake $HOME) plus an inflated
# session corpus so cold-cache parse throughput measures something real.
#
#   source bench/env.sh [target-dir]
#
# Knobs (set before sourcing):
#   BENCH_S1_MB   size of the first claude session   (default 12)
#   BENCH_S2_MB   size of the second claude session  (default 8)
#   BENCH_CDX_MB  size of the codex rollout          (default 5)
#
# On a live box with real agents you can skip this entirely and run
# bench/run.sh against your normal environment; the doc records which
# setup produced the numbers.

BENCH_DIR=${1:-/tmp/agentdash-bench}
BENCH_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# shellcheck source=tests/demo-env.sh
source "$BENCH_ROOT/tests/demo-env.sh" "$BENCH_DIR"

_inflate() { # _inflate <path> <kind> <target-mb>
  python3 - "$1" "$2" "$3" <<'PY'
import json, sys, time
path, kind, mb = sys.argv[1], sys.argv[2], int(sys.argv[3])
target = mb * 1024 * 1024
ts = time.strftime('%Y-%m-%dT%H:%M:%S.000Z', time.gmtime(time.time() - 3600))
filler = 'the quick brown fox refactors the lazy module ' * 8
i = 0
with open(path, 'a') as f:
    while f.tell() < target:
        i += 1
        if kind == 'claude':
            f.write(json.dumps({
                'type': 'assistant', 'timestamp': ts,
                'message': {'id': f'msg_bench_{i}', 'model': 'claude-opus-4-8',
                            'content': [{'type': 'text', 'text': filler}],
                            'usage': {'input_tokens': 120, 'cache_creation_input_tokens': 300,
                                      'cache_read_input_tokens': 9000, 'output_tokens': 80}}}) + '\n')
        else:
            f.write(json.dumps({
                'timestamp': ts, 'type': 'event_msg',
                'payload': {'type': 'agent_message', 'message': filler}}) + '\n')
PY
}

_P1="$BENCH_DIR/home/.claude/projects/-work-api/s1.jsonl"
_P2="$BENCH_DIR/home/.claude/projects/-work-web/s2.jsonl"
_CDX=$(find "$BENCH_DIR/home/.codex/sessions" -name '*.jsonl' | head -1)

_inflate "$_P1" claude "${BENCH_S1_MB:-12}"
_inflate "$_P2" claude "${BENCH_S2_MB:-8}"
_inflate "$_CDX" codex "${BENCH_CDX_MB:-5}"

# restore the mtimes the demo set up (inflation touched the files):
# s1 waiting, s2 fresh, rollout idle
touch -d '15 minutes ago' "$_P1"
touch -d '2 hours ago' "$_CDX"

unset _P1 _P2 _CDX
unset -f _inflate

echo "bench env ready: HOME=$HOME corpus=$(du -sh "$BENCH_DIR/home/.claude" "$BENCH_DIR/home/.codex" | awk '{print $1}' | paste -sd+ -)"
