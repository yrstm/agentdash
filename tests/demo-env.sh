#!/usr/bin/env bash
# Source this to put a deterministic fake box on PATH/HOME for the demo gif
# (and for eyeballing the renderer without real agents). Creates fixture
# sessions + command shims under /tmp/agentdash-demo.

DEMO=${1:-/tmp/agentdash-demo}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
rm -rf "$DEMO"
mkdir -p "$DEMO/home" "$DEMO/bin"

# ---- fixture sessions -----------------------------------------------------
P1="$DEMO/home/.claude/projects/-work-api"
P2="$DEMO/home/.claude/projects/-work-web"
mkdir -p "$P1" "$P2" "$DEMO/home/.codex/sessions/2026/06/01"
# fixture timestamps are rewritten to "recently" so session ages look real
TS_NEW=$(date -u -d '10 minutes ago' +%FT%H:%M)
sed -e 's/fix the failing checkout test/track down the flaky checkout test timeout/' \
    -e "s/2026-01-01T00:00/$TS_NEW/" \
  "$ROOT/tests/fixtures/claude-basic.jsonl" > "$P1/s1.jsonl"
sed -e 's/fix the failing checkout test/migrate the queue consumer to the new schema/' \
    -e "s/2026-01-01T00:00/$TS_NEW/" \
  "$ROOT/tests/fixtures/claude-basic.jsonl" > "$P2/s2.jsonl"
touch -d '15 minutes ago' "$P1/s1.jsonl"    # last turn is a pending tool call -> stuck?, detached
# the rollout filename timestamp must match the fake pid's start (etimes 9000)
# or the pairing is (correctly) marked heuristic
ROLLOUT="$DEMO/home/.codex/sessions/2026/06/01/rollout-$(date -u -d '9000 seconds ago' +%FT%H-%M-%S)-demo.jsonl"
sed "s/2026-01-01T00:0/$(date -u -d '3 hours ago' +%FT%H:0)/" \
  "$ROOT/tests/fixtures/codex-rollout.jsonl" > "$ROLLOUT"
touch -d '2 hours ago' "$ROLLOUT"

# ---- command shims ----------------------------------------------------------
cat > "$DEMO/bin/pgrep" <<'SH'
#!/usr/bin/env bash
printf '9001 claude\n9002 claude\n9003 codex /work/svc\n9004 hermes -p api\n'
SH
cat > "$DEMO/bin/ps" <<'SH'
#!/usr/bin/env bash
case "$*" in
  *-eo\ pid=,ppid=*) printf '9001 9004\n9002 1\n9003 1\n9004 1\n' ;;
  *-eo\ pid,stat,args*) echo "  PID STAT ARGS" ;;
  *9001*) echo "pts/1     900 9004" ;;
  *9002*) echo "pts/2    7200 1" ;;
  *9003*) echo "pts/3    9000 1" ;;
  *9004*) echo "pts/4   90000 1" ;;
esac
SH
cat > "$DEMO/bin/readlink" <<'SH'
#!/usr/bin/env bash
case "$1" in
  /proc/9001/cwd) echo /work/web ;;
  /proc/9002/cwd) echo /work/api ;;
  /proc/9003/cwd) echo /work/svc ;;
  /proc/9004/cwd) echo "$HOME" ;;
  *) exec /usr/bin/readlink "$@" ;;
esac
SH
cat > "$DEMO/bin/tmux" <<'SH'
#!/usr/bin/env bash
case "$*" in
  list-panes*pane_tty*) printf '/dev/pts/1|1|main|0|%%1\n/dev/pts/2|0|api|0|%%2\n' ;;
  list-panes*) exit 0 ;;
  ls*) now=$(date +%s)
       printf 'main|attached|%s\napi|detached|%s\n' "$((now-7200))" "$((now-90000))" ;;
esac
SH
cat > "$DEMO/bin/ss" <<'SH'
#!/usr/bin/env bash
echo 'State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process'
echo 'LISTEN 0      511    0.0.0.0:5173      0.0.0.0:*    users:(("node",pid=9050,fd=23))'
SH
cat > "$DEMO/bin/w" <<'SH'
#!/usr/bin/env bash
echo 'demo     pts/9    10:01    2.00s  0.1s 0.1s sshd: demo'
SH
cat > "$DEMO/bin/hostname" <<'SH'
#!/usr/bin/env bash
echo devbox
SH
chmod +x "$DEMO/bin/"*

export HOME="$DEMO/home"
# `agentdash` on the demo PATH is the legacy script (the v2 binary is
# whatever you put on PATH yourself)
ln -sf "$ROOT/legacy/agentdash.sh" "$DEMO/bin/agentdash"
export PATH="$DEMO/bin:$ROOT:$PATH"
export AGENTDASH_SKIP_DOCKER=1
# wide thresholds so statuses hold still while a recording is in progress
export AGENTDASH_WORKING_SECS=300
export AGENTDASH_IDLE_SECS=3600
export PS1='demo$ '
