#!/usr/bin/env bash
# Source this to build a fixture /proc tree matching tests/demo-env.sh
# (pids 9001/9002 claude, 9003 codex, 9004 hermes wrapper, 9050 node
# listener on port 5173). The Go binary reads it via AGENTDASH_PROC_ROOT;
# the legacy script keeps using the demo command shims.
#
#   source tools/fake-proc.sh <proc-root>
# leaves AGENTDASH_PROC_ROOT exported.

FAKEPROC_ROOT=${1:?usage: source tools/fake-proc.sh <proc-root>}
_now=$(date +%s)
_btime=$((_now - 1000000))

_mkpid() { # pid ppid tty_minor(-1 = none) etimes cwd cmdline...
  local pid=$1 ppid=$2 ttymin=$3 et=$4 cwd=$5; shift 5
  local d="$FAKEPROC_ROOT/$pid" start=$(((_now - et - _btime) * 100)) ttynr=0
  [ "$ttymin" -ge 0 ] && ttynr=$((34816 + ttymin))
  mkdir -p "$d/fd"
  printf '%s\0' "$@" > "$d/cmdline"
  printf '%s' "$1" > "$d/comm"
  printf '%s (%s) S %s %s %s %s 0 0 0 0 0 0 0 0 0 0 0 0 0 0 %s 0 0 0\n' \
    "$pid" "${1##*/}" "$ppid" "$pid" "$pid" "$ttynr" "$start" > "$d/stat"
  if [ -n "$cwd" ]; then ln -sf "$cwd" "$d/cwd"; fi
}

rm -rf "$FAKEPROC_ROOT"
mkdir -p "$FAKEPROC_ROOT/net"
printf 'cpu  0 0 0 0\nbtime %s\n' "$_btime" > "$FAKEPROC_ROOT/stat"
printf '0.00 0.00 0.00 1/100 9999\n' > "$FAKEPROC_ROOT/loadavg"
_mkpid 9001 9004 1 900   /work/web claude
_mkpid 9002 1    2 7200  /work/api claude
_mkpid 9003 1    3 9000  /work/svc codex /work/svc
_mkpid 9004 1    4 90000 "$HOME"   hermes -p api
_mkpid 9050 1   -1 600   ""        node server.js
ln -sf 'socket:[77001]' "$FAKEPROC_ROOT/9050/fd/23"
cat > "$FAKEPROC_ROOT/net/tcp" <<'EOF'
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 77001 1 0000000000000000 100 0 0 10 0
EOF
printf '  sl  local_address rem_address st\n' > "$FAKEPROC_ROOT/net/tcp6"

export AGENTDASH_PROC_ROOT=$FAKEPROC_ROOT
unset -f _mkpid
unset _now _btime
