# shared test plumbing: run the embedded python engine against a fake $HOME

setup_engine() {
  export TESTHOME="$BATS_TEST_TMPDIR/home"
  mkdir -p "$TESTHOME"
  export HOME="$TESTHOME"
  PYENGINE="$BATS_TEST_TMPDIR/engine.py"
  # extract the python block between the PYEOF heredoc markers
  awk "/3<<'PYEOF'\$/ { f = 1; next } /^PYEOF\$/ { f = 0 } f" \
    "$BATS_TEST_DIRNAME/../legacy/agentdash.sh" > "$PYENGINE"
  [ -s "$PYENGINE" ] || { echo "failed to extract python engine" >&2; return 1; }
}

# encoded claude project dir for a cwd, created under the fake HOME
mkproj() {
  local enc
  enc=$(python3 -c "import re,sys; print(re.sub(r'[^A-Za-z0-9]','-',sys.argv[1]))" "$1")
  mkdir -p "$HOME/.claude/projects/$enc"
  printf '%s' "$HOME/.claude/projects/$enc"
}

# run table mode; stdin rows are "pid<TAB>kind<TAB>cwd<TAB>etimes"
run_table() {
  AGENTDASH_MODE=table python3 "$PYENGINE"
}

# field <n> of the first output row (tab-separated)
field() {
  cut -f"$1" | head -1
}

# extract a single bash function from the script and load it (plus deps)
load_fn() {
  local fn
  for fn in "$@"; do
    eval "$(sed -n "/^${fn}()/,/^}/p" "$BATS_TEST_DIRNAME/../legacy/agentdash.sh")"
  done
}
