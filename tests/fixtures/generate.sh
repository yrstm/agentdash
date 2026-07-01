#!/usr/bin/env bash
# Build a fully synthetic $HOME tree for tests that need real session/config
# files rather than the fixture /proc tree tools/fake-proc.sh provides.
# Fully synthetic: no real usernames, prompts, or secrets, and this script
# never reads the invoking user's actual $HOME.
#
# usage: tests/fixtures/generate.sh <dest-dir> [seed]
#
# Same seed always produces a byte-identical tree (no $RANDOM, no
# wall-clock-derived content) so output is reproducible and diffable. The
# seed only perturbs non-load-bearing padding; every planted shape below is
# present regardless of seed.
set -euo pipefail

DEST=${1:?usage: tests/fixtures/generate.sh <dest-dir> [seed]}
SEED=${2:-1}

rm -rf "$DEST"
mkdir -p "$DEST"

CLAUDE_DIR="$DEST/.claude"
CODEX_DIR="$DEST/.codex/sessions/2026/01/01"
PROJECT="$DEST/work/widget"

# ---- cwd -> ~/.claude/projects/<enc> encoding, matching
# internal/procs/pair.go's ProjDir (regex [^A-Za-z0-9] -> "-") ---------------
encode_cwd() { sed -E 's/[^A-Za-z0-9]/-/g' <<<"$1"; }

PROJ_ENC=$(encode_cwd "$PROJECT")
SESS_DIR="$CLAUDE_DIR/projects/$PROJ_ENC"
mkdir -p "$SESS_DIR" "$CODEX_DIR" "$PROJECT/.cursor/rules" "$PROJECT/.git"

# ---- global instruction file (scope=global) --------------------------------
cat > "$CLAUDE_DIR/CLAUDE.md" <<'EOF'
# fixture global instructions

Synthetic global memory file for tests. Prefer rebasing local branches over
merge commits.
EOF

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

cat > "$PROJECT/.cursor/rules/style.mdc" <<'EOF'
---
description: style rules
---
Always use 2 spaces for indentation.
EOF

# ---- session-normal.jsonl: plausible multi-turn session, ends on an
# assistant text turn so status resolves to "waiting" once the file goes
# quiet -----------------------------------------------------------------
cat > "$SESS_DIR/session-normal.jsonl" <<'EOF'
{"type":"permission-mode","permissionMode":"default","sessionId":"00000000-0000-0000-0000-0000000000n1"}
{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"add pagination to the widget list endpoint"}}
{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"id":"msg_n1","model":"claude-opus-4-8","content":[{"type":"text","text":"Looking at the endpoint now."}],"usage":{"input_tokens":100,"cache_creation_input_tokens":300,"cache_read_input_tokens":600,"output_tokens":50}}}
{"type":"assistant","timestamp":"2026-01-01T00:00:06.000Z","message":{"id":"msg_n1","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}],"usage":{"input_tokens":100,"cache_creation_input_tokens":300,"cache_read_input_tokens":600,"output_tokens":50}}}
{"type":"user","timestamp":"2026-01-01T00:00:07.000Z","message":{"content":[{"type":"tool_result"}]},"toolUseResult":{"ok":true}}
{"type":"assistant","timestamp":"2026-01-01T00:00:10.000Z","message":{"id":"msg_n2","model":"claude-opus-4-8","content":[{"type":"text","text":"Pagination added; tests green."}],"usage":{"input_tokens":150,"cache_creation_input_tokens":0,"cache_read_input_tokens":900,"output_tokens":40}}}
EOF

# ---- session-truncated.jsonl: mid-write crash -- last line has no closing
# braces and no trailing newline. scanRegion (internal/parse/scan.go) must
# leave the partial tail unconsumed rather than choke on it. -----------------
printf '%s\n' \
  '{"type":"permission-mode","permissionMode":"default","sessionId":"00000000-0000-0000-0000-0000000000t1"}' \
  '{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"rotate the log shipping credentials"}}' \
  '{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"id":"msg_t1","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Rotating now."}],"usage":{"input_tokens":80,"output_tokens":20}}}' \
  > "$SESS_DIR/session-truncated.jsonl"
printf '{"type":"assistant","timestamp":"2026-01-01T00:00:09.000Z","message":{"id":"msg_t2","model":"claude-sonnet-4-6","content":[{"type":"tool_use","name":"Bash","input":{"command":"vault rotate' \
  >> "$SESS_DIR/session-truncated.jsonl"

# ---- session-oversized.jsonl: one line over scanWindow (1<<20 bytes in
# internal/parse/scan.go), generated in-line rather than checked into the
# repo, so scanRegion's overflow-accumulation path gets exercised. A normal
# line follows to confirm scanning continues past it. seed only picks the
# filler byte so the tree stays deterministic per seed. ---------------------
chars="abcdefghijklmnopqrstuvwxyz"
fill_char=${chars:$(( SEED % 26 )):1}
filler=$(head -c 1500000 /dev/zero | tr '\0' "$fill_char")
{
  printf '{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"pasted this by mistake: %s"}}\n' "$filler"
  printf '{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"id":"msg_o1","model":"claude-opus-4-8","content":[{"type":"text","text":"That was a big paste."}],"usage":{"input_tokens":500,"output_tokens":20}}}\n'
} > "$SESS_DIR/session-oversized.jsonl"

# ---- session-compacted.jsonl: two summary entries -> CompactionN == 2 ------
cat > "$SESS_DIR/session-compacted.jsonl" <<'EOF'
{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"long refactor session, expect compaction"}}
{"type":"summary","summary":"refactor session part 1"}
{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"id":"msg_c1","model":"claude-opus-4-8","content":[{"type":"text","text":"Continuing after compaction."}],"usage":{"input_tokens":100,"output_tokens":50}}}
{"type":"summary","summary":"refactor session part 2"}
{"type":"assistant","timestamp":"2026-01-01T00:01:00.000Z","message":{"id":"msg_c2","model":"claude-opus-4-8","content":[{"type":"text","text":"Second compaction done."}],"usage":{"input_tokens":120,"output_tokens":45}}}
EOF

# ---- session-secret.jsonl: a fake AWS key, the canonical AWS-docs example
# placeholder (AKIA...EXAMPLE) -- feeds a future `agentdash trail secrets`;
# no current code consumes this, planted ahead of that work. ----------------
cat > "$SESS_DIR/session-secret.jsonl" <<'EOF'
{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"can you check why deploys are failing"}}
{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"id":"msg_s1","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo leaked AWS key AKIAIOSFODNN7EXAMPLE in the old deploy script"}}],"usage":{"input_tokens":90,"output_tokens":30}}}
EOF

# ---- agent-*.jsonl: a subagent transcript. board/actions.go's Recap and
# history.go's isClaudeSubagent both treat this prefix specially. -----------
cat > "$SESS_DIR/agent-a1b2c3d4.jsonl" <<'EOF'
{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"investigate the flaky integration test"}}
{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"id":"msg_sa1","model":"claude-haiku-4-5","content":[{"type":"text","text":"Found a race in the queue drain."}],"usage":{"input_tokens":60,"output_tokens":25}}}
EOF

# ---- codex rollout: session_meta + turn_context (with approval/sandbox
# fields, for a future `agentdash trail commands` annotation) + token_count +
# event_msg turns, all in one file (matches the existing single-file
# convention in internal/parse/testdata/codex-golden.jsonl). ----------------
cat > "$CODEX_DIR/rollout-2026-01-01T00-00-00-fixture.jsonl" <<EOF
{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"00000000-0000-0000-0000-0000000000f1","cwd":"$PROJECT","originator":"codex-tui","cli_version":"0.134.0"}}
{"timestamp":"2026-01-01T00:00:02.000Z","type":"turn_context","payload":{"model":"gpt-5.5","approval_policy":"on-request","sandbox_policy":"workspace-write"}}
{"timestamp":"2026-01-01T00:00:03.000Z","type":"event_msg","payload":{"type":"user_message","message":"add rate limiting to the widget API"}}
{"timestamp":"2026-01-01T00:01:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":42000,"cached_input_tokens":20000,"output_tokens":3000},"last_token_usage":{"input_tokens":30000,"cached_input_tokens":18000},"model_context_window":272000}}}
{"timestamp":"2026-01-01T00:01:05.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Rate limiting added; tests green."}}
EOF

echo "generated fixture HOME at $DEST (seed=$SEED)"
