#!/usr/bin/env bash
# bench/container-footprint.sh <binary-under-test>
#
# Measurement 7: the "true footprint" of the tool's dependencies, measured
# in a clean alpine:latest container.
#   a) run the binary as-is and record the failure mode
#   b) install what it needs (bash python3 procps iproute2 for v1; nothing
#      for a static v2 binary) and record the installed-size delta
# Needs a working docker daemon with network access for apk.
set -euo pipefail

BIN=${1:?usage: bench/container-footprint.sh <binary-under-test>}
BIN=$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT=${BENCH_OUT:-$ROOT/bench/out}
mkdir -p "$OUT"

if ! docker info >/dev/null 2>&1; then
  echo "container footprint: not collected (no docker daemon on this box)" \
    | tee "$OUT/container-footprint.txt"
  exit 0
fi

{
  echo "== bare alpine, run as-is =="
  docker run --rm -v "$BIN:/usr/local/bin/agentdash:ro" alpine:latest \
    agentdash --json 2>&1 || echo "(exit $?)"

  echo
  echo "== installed-size delta for the v1 dependency set =="
  docker run --rm alpine:latest sh -c '
    before=$(du -sk / 2>/dev/null | cut -f1)
    apk add --no-cache bash python3 procps iproute2 >/dev/null
    after=$(du -sk / 2>/dev/null | cut -f1)
    echo "delta KiB: $((after - before))"
  '

  echo
  echo "== run again with deps installed =="
  docker run --rm -v "$BIN:/usr/local/bin/agentdash:ro" alpine:latest sh -c '
    apk add --no-cache bash python3 procps iproute2 >/dev/null
    agentdash --json' 2>&1 | head -20
} | tee "$OUT/container-footprint.txt"
