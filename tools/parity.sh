#!/usr/bin/env bash
# tools/parity.sh: the v2 exit gate. Runs the legacy bash board and the Go
# board over the same deterministic fixture box and diffs their --json
# (schema_version 1 is a frozen contract).
#
# The legacy script discovers processes through the demo-env command shims
# (pgrep/ps/readlink); the Go binary reads a fixture /proc tree via the
# AGENTDASH_PROC_ROOT test hook. Both see the same fake $HOME, cache and
# tmux shim. Volatile fields (uptime_s, last_write) are masked; everything
# else must match byte for byte.
#
# usage: tools/parity.sh            # builds the Go binary, runs both
#        tools/parity.sh <go-bin>   # use a prebuilt binary
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
LEGACY=$ROOT/agentdash
[ -f "$LEGACY" ] || LEGACY=$ROOT/legacy/agentdash.sh
[ -f "$LEGACY" ] || { echo "parity: no legacy script found" >&2; exit 1; }

GO_BIN=${1:-}
if [ -z "$GO_BIN" ]; then
  GO_BIN=$(mktemp -d)/agentdash-go
  (cd "$ROOT" && go build -o "$GO_BIN" ./cmd/agentdash)
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ---- fixture box: shims + sessions for bash, /proc tree for go -------------
# shellcheck source=tests/demo-env.sh
source "$ROOT/tests/demo-env.sh" "$WORK/demo"

# shellcheck source=tools/fake-proc.sh
source "$ROOT/tools/fake-proc.sh" "$WORK/proc"
PROC=$AGENTDASH_PROC_ROOT

# ---- run both, twice (the first run seeds the NEW-port state) --------------
"$LEGACY" --json > /dev/null
"$LEGACY" --json > "$WORK/legacy.json"
AGENTDASH_PROC_ROOT=$PROC "$GO_BIN" --json > /dev/null
AGENTDASH_PROC_ROOT=$PROC "$GO_BIN" --json > "$WORK/go.json"

normalize() {
  jq -S '{schema_version,
          agents: [.agents[] | del(.uptime_s, .last_write)] | sort_by(.pid),
          ports:  [.ports[]] | sort_by(.port)}' "$1"
}

normalize "$WORK/legacy.json" > "$WORK/legacy.norm.json"
normalize "$WORK/go.json" > "$WORK/go.norm.json"

if diff -u "$WORK/legacy.norm.json" "$WORK/go.norm.json"; then
  echo "parity: OK (legacy and go --json agree)"
else
  echo "parity: MISMATCH" >&2
  exit 1
fi
