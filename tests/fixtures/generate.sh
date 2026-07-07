#!/usr/bin/env bash
# Build a fully synthetic $HOME tree for tests that need real session/config
# files rather than the fixture /proc tree tools/fake-proc.sh provides.
# Fully synthetic: no real usernames, prompts, or secrets, and this script
# never reads the invoking user's actual $HOME.
#
# usage: tests/fixtures/generate.sh <dest-dir> [seed] [ref-epoch]
#
# ref-epoch (default: invocation time) anchors every embedded JSONL
# timestamp and file mtime as a fixed offset before it, so status states
# (working/waiting/idle) and A2's future rolling 5h/7d usage windows are
# reachable at test time without depending on wall-clock luck. Pin ref-epoch
# (and seed) to get a byte-identical tree across runs; left at the default,
# the tree is fresh but still internally consistent for whatever "now" the
# caller measures against moments later.
set -euo pipefail

DEST=${1:?usage: tests/fixtures/generate.sh <dest-dir> [seed] [ref-epoch]}
SEED=${2:-1}
REF=${3:-$(date +%s)}

# Portable epoch formatting: GNU date wants `-d @epoch`, BSD (macOS) wants
# `-r epoch`. Detect once so the fixture generator runs on both CI runners.
if date -u -r 0 +%Y >/dev/null 2>&1; then DATE_MODE=bsd; else DATE_MODE=gnu; fi
fmt_epoch() { # $1=epoch $2=+format
  if [ "$DATE_MODE" = bsd ]; then date -u -r "$1" "$2"; else date -u -d "@$1" "$2"; fi
}
iso() { fmt_epoch "$1" +%Y-%m-%dT%H:%M:%S.000Z; } # epoch -> claude/codex timestamp
ago() { echo $(( REF - $1 )); }                    # seconds-before-ref -> epoch
# Set a file's mtime from an epoch. `touch -t` (CCYYMMDDhhmm.SS) is accepted by
# both GNU and BSD touch, unlike GNU-only `touch -d @epoch`. touch interprets
# the stamp in LOCAL time, so it must be formatted in local time too — a UTC
# stamp lands the mtime off by the UTC offset on any non-UTC machine.
fmt_epoch_local() { # $1=epoch $2=+format
  if [ "$DATE_MODE" = bsd ]; then date -r "$1" "$2"; else date -d "@$1" "$2"; fi
}
touch_at() { touch -t "$(fmt_epoch_local "$1" +%Y%m%d%H%M.%S)" "$2"; }

rm -rf "$DEST"
mkdir -p "$DEST"

CLAUDE_DIR="$DEST/.claude"
PROJECT="$DEST/work/widget"

# ---- cwd -> ~/.claude/projects/<enc> encoding, matching
# internal/procs/pair.go's ProjDir (regex [^A-Za-z0-9] -> "-") ---------------
encode_cwd() { sed -E 's/[^A-Za-z0-9]/-/g' <<<"$1"; }

PROJ_ENC=$(encode_cwd "$PROJECT")
SESS_DIR="$CLAUDE_DIR/projects/$PROJ_ENC"
mkdir -p "$SESS_DIR" "$PROJECT/.cursor/rules" "$PROJECT/.git"

# ---- global instruction file (scope=global) --------------------------------
cat > "$CLAUDE_DIR/CLAUDE.md" <<'EOF'
# fixture global instructions

Synthetic global memory file for tests. Prefer rebasing local branches over
merge commits.
EOF
touch_at "$(ago $((30 * 86400)))" "$CLAUDE_DIR/CLAUDE.md" # a long-standing rule file

# ---- project instruction files: a planted conflict + a dead path ----------
# CLAUDE.md says tabs; the cursor rule says 2 spaces -- same topic, opposite
# value (feeds a future conflicting-instructions check). CLAUDE.md also
# references docs/setup.md, which does not exist -- a dead path that
# internal/drift's staleRules already flags today.
cat > "$PROJECT/CLAUDE.md" <<'EOF'
# widget service

Use tabs for indentation.
See docs/setup.md for build steps.
EOF
touch_at "$(ago $((14 * 86400)))" "$PROJECT/CLAUDE.md"

cat > "$PROJECT/.cursor/rules/style.mdc" <<'EOF'
---
description: style rules
---
Always use 2 spaces for indentation.
EOF
touch_at "$(ago $((14 * 86400)))" "$PROJECT/.cursor/rules/style.mdc"

# ---- session-normal.jsonl: three usage-bearing assistant turns spread at
# ~6d/~4h/~9min before ref, so a future A2 `usage` command has one point
# outside the 5h window but inside 7d (6d), one inside both near the 5h edge
# (4h), and one clearly recent (9min). Ends on assistant text with mtime 9min
# before ref -- inside (WorkingSecs, IdleSecs) = (60s, 600s) -- so status
# resolves to "waiting". ------------------------------------------------
T_6D=$(ago $((6 * 86400)))
T_4H=$(ago $((4 * 3600)))
T_9MIN=$(ago $((9 * 60)))
cat > "$SESS_DIR/session-normal.jsonl" <<EOF
{"type":"permission-mode","permissionMode":"default","sessionId":"00000000-0000-0000-0000-0000000000n1"}
{"type":"user","timestamp":"$(iso $((T_6D - 5)))","message":{"content":"add pagination to the widget list endpoint"}}
{"type":"assistant","timestamp":"$(iso "$T_6D")","message":{"id":"msg_n0","model":"claude-opus-4-8","content":[{"type":"text","text":"Looking at the endpoint now."}],"usage":{"input_tokens":100,"cache_creation_input_tokens":300,"cache_read_input_tokens":600,"output_tokens":50}}}
{"type":"user","timestamp":"$(iso $((T_4H - 5)))","message":{"content":"still going -- keep at it"}}
{"type":"assistant","timestamp":"$(iso "$T_4H")","message":{"id":"msg_n1","model":"claude-opus-4-8","content":[{"type":"text","text":"Added the query changes."}],"usage":{"input_tokens":120,"cache_creation_input_tokens":0,"cache_read_input_tokens":700,"output_tokens":45}}}
{"type":"user","timestamp":"$(iso $((T_9MIN - 5)))","message":{"content":"finish up and confirm tests pass"}}
{"type":"assistant","timestamp":"$(iso "$T_9MIN")","message":{"id":"msg_n2","model":"claude-opus-4-8","content":[{"type":"text","text":"Pagination added; tests green."}],"usage":{"input_tokens":150,"cache_creation_input_tokens":0,"cache_read_input_tokens":900,"output_tokens":40}}}
EOF
touch_at "$T_9MIN" "$SESS_DIR/session-normal.jsonl"

# ---- session-truncated.jsonl: mid-write crash, seconds old -- last line has
# no closing braces and no trailing newline. scanRegion (internal/parse/
# scan.go) must leave the partial tail unconsumed rather than choke on it.
# Fresh mtime (10s before ref, < WorkingSecs=60) resolves status to
# "working". -------------------------------------------------------------
T_40S=$(ago 40)
T_35S=$(ago 35)
T_10S=$(ago 10)
printf '%s\n' \
  '{"type":"permission-mode","permissionMode":"default","sessionId":"00000000-0000-0000-0000-0000000000t1"}' \
  "{\"type\":\"user\",\"timestamp\":\"$(iso "$T_40S")\",\"message\":{\"content\":\"rotate the log shipping credentials\"}}" \
  "{\"type\":\"assistant\",\"timestamp\":\"$(iso "$T_35S")\",\"message\":{\"id\":\"msg_t1\",\"model\":\"claude-sonnet-4-6\",\"content\":[{\"type\":\"text\",\"text\":\"Rotating now.\"}],\"usage\":{\"input_tokens\":80,\"output_tokens\":20}}}" \
  > "$SESS_DIR/session-truncated.jsonl"
printf '{"type":"assistant","timestamp":"%s","message":{"id":"msg_t2","model":"claude-sonnet-4-6","content":[{"type":"tool_use","name":"Bash","input":{"command":"vault rotate' \
  "$(iso "$T_10S")" >> "$SESS_DIR/session-truncated.jsonl"
touch_at "$T_10S" "$SESS_DIR/session-truncated.jsonl"

# ---- session-oversized.jsonl: one line over scanWindow (1<<20 bytes in
# internal/parse/scan.go), generated in-line rather than checked into the
# repo, so scanRegion's overflow-accumulation path gets exercised. A normal
# line follows to confirm scanning continues past it. seed only picks the
# filler byte so the tree stays deterministic per (seed, ref). --------------
T_125S=$(ago 125)
T_120S=$(ago 120)
chars="abcdefghijklmnopqrstuvwxyz"
fill_char=${chars:$(( SEED % 26 )):1}
filler=$(head -c 1500000 /dev/zero | tr '\0' "$fill_char")
{
  printf '{"type":"user","timestamp":"%s","message":{"content":"pasted this by mistake: %s"}}\n' "$(iso "$T_125S")" "$filler"
  printf '{"type":"assistant","timestamp":"%s","message":{"id":"msg_o1","model":"claude-opus-4-8","content":[{"type":"text","text":"That was a big paste."}],"usage":{"input_tokens":500,"output_tokens":20}}}\n' "$(iso "$T_120S")"
} > "$SESS_DIR/session-oversized.jsonl"
touch_at "$T_120S" "$SESS_DIR/session-oversized.jsonl"

# ---- session-compacted.jsonl: two summary entries -> CompactionN == 2.
# mtime 3 days before ref (> IdleSecs=600) resolves status to "idle". --------
T_3D=$(ago $((3 * 86400)))
T_3D_60=$(ago $((3 * 86400 + 60)))
T_3D_65=$(ago $((3 * 86400 + 65)))
cat > "$SESS_DIR/session-compacted.jsonl" <<EOF
{"type":"user","timestamp":"$(iso "$T_3D_65")","message":{"content":"long refactor session, expect compaction"}}
{"type":"summary","summary":"refactor session part 1"}
{"type":"assistant","timestamp":"$(iso "$T_3D_60")","message":{"id":"msg_c1","model":"claude-opus-4-8","content":[{"type":"text","text":"Continuing after compaction."}],"usage":{"input_tokens":100,"output_tokens":50}}}
{"type":"summary","summary":"refactor session part 2"}
{"type":"assistant","timestamp":"$(iso "$T_3D")","message":{"id":"msg_c2","model":"claude-opus-4-8","content":[{"type":"text","text":"Second compaction done."}],"usage":{"input_tokens":120,"output_tokens":45}}}
EOF
touch_at "$T_3D" "$SESS_DIR/session-compacted.jsonl"

# ---- session-secret.jsonl: a fake AWS key, the canonical AWS-docs example
# placeholder (AKIA...EXAMPLE) -- feeds a future `agentdash trail secrets`;
# no current code consumes this, planted ahead of that work. ----------------
T_1H=$(ago 3600)
cat > "$SESS_DIR/session-secret.jsonl" <<EOF
{"type":"user","timestamp":"$(iso "$T_1H")","message":{"content":"can you check why deploys are failing"}}
{"type":"assistant","timestamp":"$(iso "$T_1H")","message":{"id":"msg_s1","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo leaked AWS key AKIAIOSFODNN7EXAMPLE in the old deploy script"}}],"usage":{"input_tokens":90,"output_tokens":30}}}
EOF
touch_at "$T_1H" "$SESS_DIR/session-secret.jsonl"

# ---- agent-*.jsonl: a subagent transcript. board/actions.go's Recap and
# history.go's isClaudeSubagent both treat this prefix specially. -----------
T_30MIN=$(ago $((30 * 60)))
cat > "$SESS_DIR/agent-a1b2c3d4.jsonl" <<EOF
{"type":"user","timestamp":"$(iso "$T_30MIN")","message":{"content":"investigate the flaky integration test"}}
{"type":"assistant","timestamp":"$(iso "$T_30MIN")","message":{"id":"msg_sa1","model":"claude-haiku-4-5","content":[{"type":"text","text":"Found a race in the queue drain."}],"usage":{"input_tokens":60,"output_tokens":25}}}
EOF
touch_at "$T_30MIN" "$SESS_DIR/agent-a1b2c3d4.jsonl"

# ---- codex rollout: session_meta + turn_context (with approval/sandbox
# fields, for a future `agentdash trail commands` annotation) + token_count +
# event_msg turns, all in one file (matches the existing single-file
# convention in internal/parse/testdata/codex-golden.jsonl). Directory and
# filename date components are derived from the rollout's own start time,
# same convention tests/demo-env.sh uses for its rollout fixture. -----------
T_ROLL=$(ago $((25 * 60)))
CODEX_DIR="$DEST/.codex/sessions/$(fmt_epoch "$T_ROLL" +%Y/%m/%d)"
mkdir -p "$CODEX_DIR"
ROLLOUT="$CODEX_DIR/rollout-$(fmt_epoch "$T_ROLL" +%Y-%m-%dT%H-%M-%S)-fixture.jsonl"
cat > "$ROLLOUT" <<EOF
{"timestamp":"$(iso "$T_ROLL")","type":"session_meta","payload":{"id":"00000000-0000-0000-0000-0000000000f1","cwd":"$PROJECT","originator":"codex-tui","cli_version":"0.134.0"}}
{"timestamp":"$(iso $((T_ROLL + 2)))","type":"turn_context","payload":{"model":"gpt-5.5","approval_policy":"on-request","sandbox_policy":"workspace-write"}}
{"timestamp":"$(iso $((T_ROLL + 3)))","type":"event_msg","payload":{"type":"user_message","message":"add rate limiting to the widget API"}}
{"timestamp":"$(iso $((T_ROLL + 60)))","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":42000,"cached_input_tokens":20000,"output_tokens":3000},"last_token_usage":{"input_tokens":30000,"cached_input_tokens":18000},"model_context_window":272000}}}
{"timestamp":"$(iso $((T_ROLL + 65)))","type":"event_msg","payload":{"type":"agent_message","message":"Rate limiting added; tests green."}}
EOF
touch_at "$((T_ROLL + 65))" "$ROLLOUT"

echo "generated fixture HOME at $DEST (seed=$SEED, ref=$REF)"
