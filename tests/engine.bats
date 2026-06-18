#!/usr/bin/env bats
# engine tests: pairing chain, status machine, token dedup, respawn, fail-soft

load helper

setup() {
  setup_engine
  CWD=/work/proj
  PROJ=$(mkproj "$CWD")
}

# output fields: 1 pid, 2 model, 3 tokens, 4 ctx, 5 ctxtok, 6 status, 7 last, 8 spark, 9 task

@test "token dedup: per-content-block usage counted once per message id" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  out=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table)
  tokens=$(echo "$out" | field 3)
  # usage block appears twice with the same id: in=1000 out=50, NOT 2000/100
  [ "$tokens" = "1.0k/0.1k" ]
}

@test "small token counts still carry a unit" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  out=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table)
  tokens=$(echo "$out" | field 3)
  [[ "$tokens" != */50 ]]   # the old 68k/836 bug shape
}

@test "pairing: single live file in project dir pairs exactly (no ?)" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  task=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table | field 9)
  [[ "$task" != *\? ]]
  [[ "$task" == fix\ the\ failing* ]]
}

@test "pairing: ambiguous twins fall to start-ts when timestamps match" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  sed 's/checkout/database/' "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" > "$PROJ/s2.jsonl"
  # fixture first ts = 2026-01-01T00:00:00Z; make the process exactly that old
  et=$(( $(date +%s) - $(date -u -d '2026-01-01T00:00:00Z' +%s) ))
  task=$(printf '901\tclaude\t%s\t%s\n' "$CWD" "$et" | run_table | field 9)
  [[ "$task" != *\? ]]
}

@test "pairing: no evidence at all degrades to a marked guess" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s2.jsonl"
  # two candidates, process age matches neither timestamp -> heuristic + '?'
  task=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table | field 9)
  [[ "$task" == *\? ]]
}

@test "status: fresh write is working" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  st=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table | field 6)
  [ "$st" = working ]
}

  # status tests: the process must predate the file write, or the pairing
  # chain (correctly) refuses the file as evidence

@test "status: quiet after assistant turn is waiting" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  # the fixture's last entry is a pending tool call (working/stuck); end on a
  # genuine assistant text turn so "waiting on you" is the right grade
  printf '{"type":"assistant","timestamp":"2026-01-01T00:02:00.000Z","message":{"id":"msg_99","model":"claude-opus-4-8","content":[{"type":"text","text":"done, your turn"}]}}\n' >> "$PROJ/s1.jsonl"
  touch -d '3 minutes ago' "$PROJ/s1.jsonl"
  st=$(printf '901\tclaude\t%s\t1200\n' "$CWD" | run_table | field 6)
  [ "$st" = waiting ]
}

@test "status: quiet after user/tool entry is stuck?" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  printf '{"type":"user","timestamp":"2026-01-01T00:02:00.000Z","message":{"content":[{"type":"tool_result"}]},"toolUseResult":{}}\n' >> "$PROJ/s1.jsonl"
  touch -d '3 minutes ago' "$PROJ/s1.jsonl"
  st=$(printf '901\tclaude\t%s\t1200\n' "$CWD" | run_table | field 6)
  [ "$st" = "stuck?" ]
}

@test "status: long quiet is idle" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  touch -d '20 minutes ago' "$PROJ/s1.jsonl"
  st=$(printf '901\tclaude\t%s\t7200\n' "$CWD" | run_table | field 6)
  [ "$st" = idle ]
}

@test "respawn: three fresh pids on one session within the window" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  printf '901\tclaude\t%s\t10\n' "$CWD" | run_table > /dev/null
  printf '902\tclaude\t%s\t10\n' "$CWD" | run_table > /dev/null
  st=$(printf '903\tclaude\t%s\t10\n' "$CWD" | run_table | field 6)
  [[ "$st" == respawn\ ×3 ]]
}

@test "fail soft: malformed lines never kill the row" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-malformed.jsonl" "$PROJ/s1.jsonl"
  out=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table)
  model=$(echo "$out" | field 2)
  task=$(echo "$out" | field 9)
  [ "$model" = "sonnet-4-6" ]
  [[ "$task" == survive\ malformed* ]]
}

@test "codex: model, ctx and window come from the rollout file" {
  mkdir -p "$HOME/.codex/sessions/2026/01/01"
  cp "$BATS_TEST_DIRNAME/fixtures/codex-rollout.jsonl" \
     "$HOME/.codex/sessions/2026/01/01/rollout-2026-01-01T00-00-00-cafe.jsonl"
  out=$(printf '905\tcodex\t/work/svc\t10\n' | run_table)
  model=$(echo "$out" | field 2)
  ctx=$(echo "$out" | field 4)
  [ "$model" = "gpt-5.5" ]
  # last request = 68k of a 272k window (from the file, not the table) = 25%
  [ "$ctx" = "25%" ]
}

@test "codex: resume command uses the rollout id, not claude --resume" {
  mkdir -p "$HOME/.codex/sessions/2026/01/01"
  cp "$BATS_TEST_DIRNAME/fixtures/codex-rollout.jsonl" \
     "$HOME/.codex/sessions/2026/01/01/rollout-2026-01-01T00-00-00-cafe.jsonl"
  printf '905\tcodex\t/work/svc\t10\n' | run_table > /dev/null  # populates pidmap
  out=$(AGENTDASH_MODE=resume AGENTDASH_PID=905 python3 "$PYENGINE" </dev/null)
  [ "$out" = "cd /work/svc && codex resume cafe" ]
}

@test "ctx window self-correction learns and persists" {
  cp "$BATS_TEST_DIRNAME/fixtures/claude-basic.jsonl" "$PROJ/s1.jsonl"
  # 300k of context on a model assumed 200k -> bump to 1M and write the conf
  printf '{"type":"assistant","timestamp":"2026-01-01T00:03:00.000Z","message":{"id":"msg_02","model":"claude-opus-4-8","content":[{"type":"text","text":"big"}],"usage":{"input_tokens":300000,"output_tokens":10}}}\n' >> "$PROJ/s1.jsonl"
  ctx=$(printf '901\tclaude\t%s\t10\n' "$CWD" | run_table | field 4)
  [ "$ctx" = "30%" ]
  grep -q 'claude-opus-4-8 1000000' "$HOME/.config/agentdash/context-windows.conf"
}
