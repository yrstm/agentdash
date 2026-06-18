#!/usr/bin/env bats
# board (bash-side) tests: tree regrouping

load helper

# fake `ps -eo pid=,ppid=`: 11 is a child of wrapper 10, 12 is not
ps() { printf '11 10\n10 1\n12 1\n'; }

fill_rows() { # 3 rows in urgency order: claude 11, claude 12, hermes 10
  local name
  for name in R_KIND R_PID R_TTY R_GLYPH R_NEED R_ETS R_LAST R_SPARK \
              R_MODEL R_TOKENS R_CTX R_CTXTOK R_STATUS R_CWD R_TASK R_TREECH; do
    eval "$name=(a b c)"
  done
  R_KIND=(claude claude hermes)
  R_PID=(11 12 10)
  R_TREECH=(" " " " " ")
}

@test "tree: a child moves under its wrapper with the branch glyph" {
  load_fn tree_order
  WRAPPER_KINDS=" hermes "
  fill_rows
  tree_order
  [ "${R_PID[*]}" = "12 10 11" ]
  [ "${R_TREECH[2]}" = "└" ]
  [ "${R_TREECH[0]}" = " " ]
}

@test "tree: companion arrays are reordered together" {
  load_fn tree_order
  WRAPPER_KINDS=" hermes "
  fill_rows
  R_TASK=(task-of-11 task-of-12 task-of-10)
  tree_order
  [ "${R_TASK[*]}" = "task-of-12 task-of-10 task-of-11" ]
}

@test "tree: no wrappers on the board is a no-op" {
  load_fn tree_order
  WRAPPER_KINDS=" hermes "
  fill_rows
  R_KIND=(claude claude codex)
  tree_order
  [ "${R_PID[*]}" = "11 12 10" ]
}
