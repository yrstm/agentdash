#!/usr/bin/env bash
# agentdash: `w` for your AI agents. Linux-only, bash 4+.
# Observes agents started any way (terminal, tmux, ssh, cron): read-only,
# zero-config, no daemon, zero API calls. It never launches or owns sessions.
# README.md documents every heuristic.
VERSION=1.1.0

usage() {
  cat <<'EOF'
agentdash: `w` for your AI agents (Linux-only, read-only, no daemon)

usage: agentdash [flags | subcommand]
  (no args)          render the board once
  -w [secs]          watch mode (default 5s; q or Ctrl-C exits)
                     keys: j/k or arrows move the cursor · g go · s show
                     y why · L label · r resume · t tree · a all · q quit
  -a                 expand everything: collapsed rows and healthy sections
  -l                 long view: adds PID, TTY, UP columns
  -t, --tree         group agent rows under the wrapper that spawned them
  --json             machine-readable agents + ports (schema_version 1)
  --plain            no color, no glyphs (NO_COLOR is honored too)
  --notify           with -w: OSC 9 desktop notification when a session flips
                     to waiting (needs tmux 3.3+ `allow-passthrough on`)
  --any-waiting      exit 0 if any session needs you, 1 otherwise (for scripts)
  go [row|pid]       jump to the agent's tmux pane (no arg: first that needs you)
  show <row|pid>     drill-down: task, recent turns, session path, resume command
  why <row|pid>      provenance per cell: pairing evidence, value sources
  label <row|pid> <text>   set a persistent TASK label ("" clears)
  resume <row|pid>   print the `claude --resume` command (with cwd)
  recap [4h|30m|2d]  what changed since you last looked (default: last recap)
  --help | --version

config (~/.config/agentdash/context-windows.conf):
  <model-id-substring> <window-tokens>   # first match wins; self-learned
                                         # entries are appended automatically
environment:
  AGENTDASH_SKIP_DOCKER=1    skip the docker sandboxes section
  AGENTDASH_WORKING_SECS=60  file younger than this -> "working"
  AGENTDASH_IDLE_SECS=600    file older than this -> "idle"
EOF
}

INTERVAL="" JSON_MODE="" NOCOLOR="" NOTIFY="" LONGVIEW="" EXPAND="" ANYWAIT="" TREE=""
ACTION="" ACTION_ARG="" ACTION_ARG2=""
case "${1:-}" in
  go|recap|resume|show|why) ACTION=$1; ACTION_ARG=${2:-} ;;
  label) ACTION=$1; ACTION_ARG=${2:-}; ACTION_ARG2=${3:-} ;;
esac
if [ -z "$ACTION" ]; then
  while [ $# -gt 0 ]; do
    case "$1" in
      --help|-h) usage; exit 0 ;;
      --version) echo "agentdash $VERSION"; exit 0 ;;
      --json) JSON_MODE=1 ;;
      --plain|--no-color) NOCOLOR=1 ;;
      --notify) NOTIFY=1 ;;
      --any-waiting) ANYWAIT=1 ;;
      -a|--all) EXPAND=1 ;;
      -l|--long) LONGVIEW=1 ;;
      -t|--tree) TREE=1 ;;
      -w|--watch)
        INTERVAL=5
        case "${2:-}" in (*[!0-9]*|'') ;; (*) INTERVAL=$2; shift ;; esac ;;
      *) echo "agentdash: unknown argument: $1 (try --help)" >&2; exit 2 ;;
    esac
    shift
  done
fi

command -v python3 >/dev/null 2>&1 || {
  echo "agentdash: python3 is required (apt install python3)" >&2
  exit 1
}

[ -n "${NO_COLOR:-}" ] && NOCOLOR=1
if [ -n "$NOCOLOR" ]; then
  B="" D="" G="" Y="" R="" N=""
else
  B=$(tput bold 2>/dev/null) || B=""
  D=$(tput dim 2>/dev/null) || D=""
  G=$(tput setaf 2 2>/dev/null) || G=""
  Y=$(tput setaf 3 2>/dev/null) || Y=""
  R=$(tput setaf 1 2>/dev/null) || R=""
  N=$(tput sgr0 2>/dev/null) || N=""
fi

CACHE_DIR=$HOME/.cache/agentdash

pad() { # pad <string> <width>: pad by display chars, not bytes (handles ×, …)
  local s=$1 w=$2 n=${#1}
  printf '%s' "$s"
  while [ "$n" -lt "$w" ]; do printf ' '; n=$((n + 1)); done
}

trunc() { # trunc <string> <width>: hard-truncate with ellipsis
  local s=$1 w=$2
  if [ "${#s}" -gt "$w" ]; then printf '%s…' "${s:0:w-1}"; else printf '%s' "$s"; fi
}

fish_path() { # ~/code/checkout-api → ~/c/checkout-api; tail survives truncation
  local p=${1/#$HOME/\~} w=$2 out="" seg last
  case "$p" in
    */*) last=${p##*/}; p=${p%/*}
         while IFS= read -r -d/ seg; do out+="${seg:0:1}/"; done <<< "$p/"
         p="$out$last" ;;
  esac
  if [ "${#p}" -gt "$w" ]; then printf '…%s' "${p: -$((w - 1))}"; else printf '%s' "$p"; fi
}

fmt_up() { # seconds → compact duration (42m / 16h / 1d6h)
  local s=$1 u
  if [ "$s" -lt 60 ]; then u="${s}s"
  elif [ "$s" -lt 3600 ]; then u="$((s / 60))m"
  elif [ "$s" -lt 86400 ]; then u="$((s / 3600))h"
  else u="$((s / 86400))d$((s % 86400 / 3600))h"; [ "${#u}" -gt 5 ] && u="$((s / 86400))d"
  fi
  printf '%s' "$u"
}

hum_ctx() { # tokens → 68k / 1.2m / 12m (header idle-context figure)
  local n=$1 t
  if [ "$n" -ge 10000000 ]; then printf '%sm' "$(((n + 500000) / 1000000))"
  elif [ "$n" -ge 1000000 ]; then
    t=$(((n + 50000) / 100000))
    printf '%s.%sm' "$((t / 10))" "$((t % 10))"
  else printf '%sk' "$((n / 1000))"
  fi
}

fmt_mem() { # 1.137MiB → 1.1M ; 28.3MiB → 28M ; 1.2GiB → 1.2G
  local v=$1 n u
  n=${v%%[A-Za-z]*}
  u=${v#"$n"}; u=${u:0:1}
  case "$n" in ''|*[!0-9.]*) printf '%s' "$v"; return ;; esac
  if awk -v n="$n" 'BEGIN { exit !(n >= 10) }'; then
    printf '%.0f%s' "$n" "$u"
  else
    printf '%.1f%s' "$n" "$u"
  fi
}

fmt_runfor() { # docker RunningFor ("2 hours ago", "About a minute") → 2h / 1m
  local v=${1% ago} n u
  case "$v" in
    "Less than a second"*) printf '0s'; return ;;
    "About a minute"*) printf '1m'; return ;;
    "About an hour"*) printf '1h'; return ;;
  esac
  n=${v%% *}; u=${v#* }; u=${u:0:1}
  case "$n" in ''|*[!0-9]*) printf '%s' "$v" ;; *) printf '%s%s' "$n" "$u" ;; esac
}

ctx_cell() { # "40%" → "▓▓░░░  40%" (10 cols); yellow ≥70, red ≥85; "-" padded
  local raw=$1 p bar="" i c=""
  if [[ $raw =~ ^([0-9]+)%$ ]]; then
    p=${BASH_REMATCH[1]}
    if [ -n "$NOCOLOR" ]; then
      printf '%-10s' "$p%"
      return
    fi
    for ((i = 0; i < 5; i++)); do
      if [ $((i * 20)) -lt "$p" ]; then bar+="▓"; else bar+="░"; fi
    done
    if [ "$p" -ge 85 ]; then c=$R; elif [ "$p" -ge 70 ]; then c=$Y; fi
    printf '%s%s %3s%%%s' "$c" "$bar" "$p" "$N"
  else
    printf '%-10s' "-"
  fi
}

# shellcheck disable=SC1003  # the trailing \\ is a literal ST terminator, not quoting
osc9() { # desktop notification via OSC 9 (passes ssh; tmux needs allow-passthrough)
  local msg=$1
  if [ -n "${TMUX:-}" ]; then printf '\033Ptmux;\033\033]9;%s\007\033\\' "$msg"
  else printf '\033]9;%s\007' "$msg"; fi
}

# ---------------------------------------------------------------------------
# Parser contract, part 1 (bash): detect. One case branch per agent; first
# match wins. Wrapper agents (no session files) go in WRAPPER_KINDS and are
# listed, not enriched. Part 2 (python): locate + extract, see CONTRIBUTING.md.
# ---------------------------------------------------------------------------
WRAPPER_KINDS=" hermes "
agent_kind_of() {
  case "$1" in
    *hermes*) printf 'hermes' ;;
    *claude*) printf 'claude' ;;
    *codex*)  printf 'codex' ;;
  esac
}

# Modes (AGENTDASH_MODE): table (default): stdin rows
#   "pid \t kind \t cwd \t etimes" →
#   "pid \t model \t tokens \t ctx \t ctxtok \t status \t last \t spark \t task"
# recap: "state \t age \t title \t last-msg \t resume-cmd" lines
# resume/show/why: AGENTDASH_PID; label: AGENTDASH_PID + AGENTDASH_LABEL
# (program arrives on fd 3 so stdin stays free for the row data)
resolve_tasks() {
  python3 /dev/fd/3 3<<'PYEOF'
import sys, os, json, glob, re, time, calendar

HOME = os.path.expanduser('~')
TASK_W = 60
TS_SLACK = 300
CACHE_PATH = os.path.join(HOME, '.cache', 'agentdash', 'usage.json')
CONF_PATH = os.path.join(HOME, '.config', 'agentdash', 'context-windows.conf')
NOW = time.time()
MODE = os.environ.get('AGENTDASH_MODE', 'table')
PARSER_V = 3  # bump when parsers extract new fields: forces a one-time rescan

def envint(name, dflt):
    try:
        return int(os.environ.get(name, dflt))
    except ValueError:
        return dflt

W_SECS = envint('AGENTDASH_WORKING_SECS', 60)
I_SECS = envint('AGENTDASH_IDLE_SECS', 600)

def parse_line(ln):
    if isinstance(ln, bytes):
        ln = ln.decode('utf-8', 'replace')
    ln = ln.strip()
    if not ln:
        return None
    try:
        return json.loads(ln)
    except Exception:
        return None  # fail soft: malformed lines never kill the board

def iso_epoch(ts):
    try:
        return calendar.timegm(time.strptime(ts[:19], '%Y-%m-%dT%H:%M:%S'))
    except (ValueError, TypeError):
        return None

# ---- context windows: conf override first, then built-ins, then learning --
def load_overrides():
    out = []
    try:
        with open(CONF_PATH) as f:
            for ln in f:
                ln = ln.split('#', 1)[0].strip()
                if not ln:
                    continue
                parts = ln.split()
                if len(parts) >= 2:
                    try:
                        out.append((parts[0], int(parts[1].replace('_', '').replace(',', ''))))
                    except ValueError:
                        pass
    except OSError:
        pass
    return out

OVERRIDES = load_overrides()

def window_for(model):
    if not model:
        return None, None
    for sub, w in OVERRIDES:  # first match in file order wins
        if sub in model:
            return w, f'conf override "{sub}"'
    if '[1m]' in model:
        return 1_000_000, 'built-in ([1m] id)'
    if any(k in model for k in ('claude', 'opus', 'sonnet', 'haiku', 'fable')):
        return 200_000, 'built-in default (200k)'
    if 'gpt' in model or 'codex' in model:
        return 272_000, 'built-in default (272k)'
    return None, None

def learn_window(model, win):
    # self-correction fired: persist what we learned so CTX% is right from
    # the first refresh next time
    if not model or any(sub in model for sub, _ in OVERRIDES):
        return
    try:
        os.makedirs(os.path.dirname(CONF_PATH), exist_ok=True)
        header = not os.path.exists(CONF_PATH)
        with open(CONF_PATH, 'a') as f:
            if header:
                f.write('# agentdash context-window overrides: <model-id-substring> <tokens>\n'
                        '# first match wins. example:\n'
                        '#   my-model-id 400000\n')
            f.write(f'{model} {win}  # learned by agentdash (observed context exceeded prior assumption)\n')
        OVERRIDES.append((model, win))
    except OSError:
        pass

# ---- incremental session-file scanner (parses only appended bytes) -------
def load_cache():
    try:
        with open(CACHE_PATH) as f:
            return json.load(f)
    except Exception:
        return {}

def save_cache(cache):
    cache = {p: e for p, e in cache.items()
             if p.startswith('_') or NOW - e.get('seen', 0) < 7 * 86400}
    try:
        os.makedirs(os.path.dirname(CACHE_PATH), exist_ok=True)
        tmp = f'{CACHE_PATH}.{os.getpid()}'
        with open(tmp, 'w') as f:
            json.dump(cache, f)
        os.chmod(tmp, 0o600)  # the cache holds prompt text
        os.replace(tmp, CACHE_PATH)
    except OSError:
        pass

def first_user_text(obj):
    msg = obj.get('message') or {}
    c = msg.get('content')
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        for part in c:
            if isinstance(part, dict) and part.get('type') == 'text':
                return part.get('text')
    return None

# ---------------------------------------------------------------------------
# Parser contract, part 2 (python). Each agent provides:
#   locate(cwd, pid, start) -> (path, sure) or batch-pairs in pair_claude()
#   update(ent, obj)        -> fold one jsonl line into the session entry,
#                              filling: model, in, out, ctx, win (optional),
#                              last_type, last_user_ts, title_user, summary,
#                              last_text
# Adding an agent = one update fn + one locate fn + an AGENTS entry + a
# detect branch in the bash agent_kind_of(). See CONTRIBUTING.md.
# ---------------------------------------------------------------------------
def upd_claude(ent, obj):
    t = obj.get('type')
    if t in ('user', 'assistant'):
        ent['last_type'] = t
    if t == 'summary' and obj.get('summary'):
        ent['summary'] = obj['summary']
    elif t == 'user':
        txt = first_user_text(obj)
        if txt and not ent.get('title_user'):
            ent['title_user'] = txt
        if txt and 'toolUseResult' not in obj:  # human turn, not a tool result
            ts = iso_epoch(obj.get('timestamp'))
            if ts:
                ent['last_user_ts'] = ts
    elif t == 'assistant':
        msg = obj.get('message') or {}
        content = msg.get('content')
        if isinstance(content, list) and any(
                isinstance(b, dict) and b.get('type') == 'tool_use' for b in content):
            ent['last_type'] = 'tool'  # parity with the Go board: a pending tool call is not "waiting on you"
        if msg.get('model'):
            ent['model'] = msg['model']
        txt = first_user_text(obj)
        if txt:
            ent['last_text'] = ' '.join(txt.split())[:160]
        u = msg.get('usage') or {}
        mid = msg.get('id')
        # one turn writes a line per content block, all carrying the same
        # usage: dedupe by message id or totals get inflated
        if u and (not mid or mid != ent.get('last_mid')):
            ent['last_mid'] = mid
            i = ((u.get('input_tokens') or 0) + (u.get('cache_creation_input_tokens') or 0)
                 + (u.get('cache_read_input_tokens') or 0))
            ent['in'] = ent.get('in', 0) + i
            ent['out'] = ent.get('out', 0) + (u.get('output_tokens') or 0)
            if i:
                ent['ctx'] = i

def upd_codex(ent, obj):
    t = obj.get('type')
    pay = obj.get('payload') or {}
    pt = pay.get('type')
    if t == 'event_msg':
        if pt == 'user_message':
            ent['last_type'] = 'user'
            ts = iso_epoch(obj.get('timestamp'))
            if ts:
                ent['last_user_ts'] = ts
            if not ent.get('title_user') and pay.get('message'):
                ent['title_user'] = pay['message']
        elif pt == 'agent_message':
            ent['last_type'] = 'assistant'
            if pay.get('message'):
                ent['last_text'] = ' '.join(str(pay['message']).split())[:160]
        elif pt == 'token_count':
            info = pay.get('info') or {}
            tot = info.get('total_token_usage') or {}
            ent['in'] = (tot.get('input_tokens') or 0) + (tot.get('cached_input_tokens') or 0)
            ent['out'] = tot.get('output_tokens') or 0
            last = info.get('last_token_usage') or {}
            li = (last.get('input_tokens') or 0) + (last.get('cached_input_tokens') or 0)
            if li:
                ent['ctx'] = li
            if info.get('model_context_window'):
                ent['win'] = info['model_context_window']
    elif t == 'response_item' and pt == 'message':
        if pay.get('role') in ('user', 'assistant'):
            ent['last_type'] = pay['role']
        if pay.get('role') == 'user' and not ent.get('title_user'):
            for part in pay.get('content') or []:
                if isinstance(part, dict) and part.get('type') in ('input_text', 'text'):
                    ent['title_user'] = part.get('text')
                    break
    elif t == 'turn_context' and pay.get('model'):
        ent['model'] = pay['model']

def scan_session(path, cache, kind):
    try:
        st = os.stat(path)
    except OSError:
        return None
    ent = cache.get(path)
    if (not ent or ent.get('kind') != kind or ent.get('offset', 0) > st.st_size
            or ent.get('v') != PARSER_V):
        ent = {'kind': kind, 'offset': 0, 'v': PARSER_V}
    consumed = 0
    if st.st_size > ent['offset']:
        try:
            with open(path, 'rb') as f:
                f.seek(ent['offset'])
                buf = f.read()
        except OSError:
            return None
        nl = buf.rfind(b'\n')  # only complete lines; the file may be mid-write
        if nl >= 0:
            upd = AGENTS[kind]['update']
            for ln in buf[:nl].split(b'\n'):
                obj = parse_line(ln)
                if isinstance(obj, dict):
                    try:
                        upd(ent, obj)
                    except Exception:
                        pass  # fail soft on surprising shapes
            ent['offset'] += nl + 1
            consumed = nl + 1
    ent['hist'] = (ent.get('hist') or [])[-7:] + [consumed]  # activity sparkline
    ent['mtime'] = st.st_mtime
    ent['seen'] = NOW
    cache[path] = ent
    return ent

# ---- claude: session location ----------------------------------------------
def proj_dir(cwd):
    enc = re.sub(r'[^A-Za-z0-9]', '-', cwd)
    return os.path.join(HOME, '.claude', 'projects', enc)

def claude_paths_for(cwd):
    return sorted(glob.glob(os.path.join(proj_dir(cwd), '*.jsonl')),
                  key=lambda p: os.path.getmtime(p), reverse=True)

def fd_session(pid, proj):
    # exact when it hits, but claude does not normally hold the jsonl open
    try:
        fds = os.listdir(f'/proc/{pid}/fd')
    except OSError:
        return None
    for fd in fds:
        try:
            t = os.readlink(f'/proc/{pid}/fd/{fd}')
        except OSError:
            continue
        if t.startswith(proj + os.sep) and t.endswith('.jsonl'):
            return t
    return None

_FTS = {}
def first_ts(path):
    # epoch of the session's first timestamped entry (≈ session start)
    if path in _FTS:
        return _FTS[path]
    val = None
    try:
        with open(path, errors='replace') as f:
            for _ in range(25):
                obj = parse_line(f.readline())
                if isinstance(obj, dict) and obj.get('timestamp'):
                    val = iso_epoch(obj['timestamp'])
                    break
    except OSError:
        pass
    _FTS[path] = val
    return val

def mtime_of(p):
    try:
        return os.path.getmtime(p)
    except OSError:
        return 0

# ---- codex: session location ------------------------------------------------
CODEX_TS_RE = re.compile(r'rollout-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2})')

def locate_codex(cwd, pid, start):
    root = os.path.join(HOME, '.codex', 'sessions')
    if not os.path.isdir(root):
        return None, False
    files = []
    for dp, _, fns in os.walk(root):
        for fn in fns:
            if fn.endswith('.jsonl'):
                p = os.path.join(dp, fn)
                try:
                    files.append((os.path.getmtime(p), p))
                except OSError:
                    pass
    files.sort(reverse=True)
    for _, p in files[:40]:  # newest rollouts only, keep the scan fast
        try:
            with open(p, errors='replace') as f:
                meta = parse_line(f.readline())
        except OSError:
            continue
        if (isinstance(meta, dict) and meta.get('type') == 'session_meta'
                and (meta.get('payload') or {}).get('cwd') == cwd):
            # the rollout filename embeds the UTC session start
            m = CODEX_TS_RE.search(os.path.basename(p))
            try:
                ts = calendar.timegm(time.strptime(m.group(1), '%Y-%m-%dT%H-%M-%S')) if m else None
            except ValueError:
                ts = None
            return p, bool(ts) and abs(ts - start) <= TS_SLACK
    return None, False

AGENTS = {
    'claude': {'update': upd_claude},                          # located by pair_claude()
    'codex':  {'update': upd_codex, 'locate': locate_codex},
}

# ---- presentation ---------------------------------------------------------
def hum(n):
    if n >= 10_000_000: return f'{n / 1e6:.0f}m'
    if n >= 1_000_000:  return f'{n / 1e6:.1f}m'
    if n >= 10_000:     return f'{n // 1000}k'
    if n >= 50:         return f'{n / 1e3:.1f}k'
    return str(n)

def short_model(m):
    if not m:
        return '-'
    m = re.sub(r'^claude-', '', m)
    m = re.sub(r'\[1m\]', '', m)
    return re.sub(r'-20\d{6}$', '', m)

def status_of(ent, respawn_n=0):
    if respawn_n >= 3:
        return f'respawn ×{respawn_n}'
    age = NOW - ent.get('mtime', 0)
    if age < W_SECS:
        return 'working'
    if age > I_SECS:
        return 'idle'
    return 'waiting' if ent.get('last_type') == 'assistant' else 'stuck?'

def clean(s, width=TASK_W):
    if not s:
        return None
    if '<' in s:  # slash-command wrappers etc. pollute titles
        s = re.sub(r'<[^>]{1,40}>', ' ', s)
    s = ' '.join(s.split())
    return (s[:width - 1] + '…' if len(s) > width else s) or None

SPARK_CH = ' ▁▂▃▄▅▆█'
def spark_of(ent):
    out = []
    hist = (ent.get('hist') or [])[-8:]
    hist = [0] * (8 - len(hist)) + hist
    for b in hist:
        lvl = 0
        if b > 0:
            lvl = min(7, max(1, b.bit_length() // 2 - 3))  # ~log scale: 256B→1, 1MB→7
        out.append(SPARK_CH[lvl])
    return ''.join(out)

def ago(sec):
    sec = max(0, int(sec))
    if sec < 60: return f'{sec}s'
    if sec < 3600: return f'{sec // 60}m'
    if sec < 86400: return f'{sec // 3600}h'
    return f'{sec // 86400}d'

def title_of(ent, path, labels):
    return clean(labels.get(path) or ent.get('summary') or ent.get('title_user'))

def fields_of(ent, sure, respawn_n, path, labels):
    model = short_model(ent.get('model'))
    ti, to = ent.get('in', 0), ent.get('out', 0)
    tokens = f'{hum(ti)}/{hum(to)}' if ti or to else '-'
    win, _src = window_for(ent.get('model'))
    if ent.get('win'):
        win = ent['win']
    # session files don't record 1M-context mode: if the measured context
    # already exceeds the assumed window, adopt the larger tier and remember it
    if win and ent.get('ctx') and ent['ctx'] > win:
        win = 1_000_000
        learn_window(ent.get('model'), win)
    ctxtok = ent.get('ctx') or 0
    ctx = f'{min(round(ctxtok * 100 / win), 100)}%' if ctxtok and win else '-'
    title = title_of(ent, path, labels)
    if title:
        task = title if sure else f'{title} ?'
    else:
        task = '(no session found)'
    last = ago(NOW - ent.get('mtime', NOW))
    return model, tokens, ctx, str(ctxtok), status_of(ent, respawn_n), last, spark_of(ent), task

cache = load_cache()
LABELS = cache.get('_labels') or {}

# ---- recap mode -----------------------------------------------------------
if MODE == 'recap':
    since = os.environ.get('AGENTDASH_SINCE') or ''
    since = float(since) if since else float(cache.get('_recap_ts') or 0)
    since = max(since, NOW - 7 * 86400)
    live = {v['path']: pid for pid, v in (cache.get('_pidmap') or {}).items()
            if os.path.exists(f'/proc/{pid}')}
    items = []
    for d in glob.glob(os.path.join(HOME, '.claude', 'projects', '*')):
        for p in glob.glob(os.path.join(d, '*.jsonl')):
            if os.path.basename(p).startswith('agent-'):  # subagent transcripts
                continue
            try:
                mt = os.path.getmtime(p)
            except OSError:
                continue
            if mt <= since:
                continue
            ent = scan_session(p, cache, 'claude')
            if not ent or not (ent.get('title_user') or ent.get('summary')):
                continue
            title = title_of(ent, p, LABELS)
            last = clean(ent.get('last_text'), 100) or ''
            st = status_of(ent)
            if p in live:
                state = 'WAITING' if st in ('waiting', 'stuck?') else st
                rcmd = ''
            else:
                state = 'finished' if ent.get('last_type') == 'assistant' else 'died?'
                sid = os.path.basename(p)[:-6]
                cd = f"cd {ent['cwd']} && " if ent.get('cwd') else ''
                rcmd = f'{cd}claude --resume {sid}' if state == 'died?' else ''
            items.append((state, NOW - mt, title, last, rcmd))
    order = {'WAITING': 0, 'died?': 1, 'finished': 2, 'working': 3, 'idle': 4}
    items.sort(key=lambda i: (order.get(i[0], 5), i[1]))
    for state, age, title, last, rcmd in items:
        print(f'{state}\t{ago(age)}\t{title}\t{last}\t{rcmd}')
    cache['_recap_ts'] = NOW
    save_cache(cache)
    sys.exit(0)

# ---- pid-addressed modes: resume / show / why / label ---------------------
if MODE in ('resume', 'show', 'why', 'label'):
    pid = os.environ.get('AGENTDASH_PID', '')
    m = (cache.get('_pidmap') or {}).get(pid)
    if not m:
        print(f'agentdash: no session known for pid {pid} (is it on the board?)',
              file=sys.stderr)
        sys.exit(1)
    path = m['path']
    sid = os.path.basename(path)[:-6]
    cd = f"cd {m['cwd']} && " if m.get('cwd') else ''
    if m.get('kind') == 'codex':
        # rollout-<ts>-<id>.jsonl: codex resumes by the trailing id
        rm = re.match(r'rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-(.+)$', sid)
        resume_cmd = f'{cd}codex resume {rm.group(1) if rm else sid}'
    else:
        resume_cmd = f'{cd}claude --resume {sid}'
    if MODE == 'resume':
        print(resume_cmd)
        sys.exit(0)
    if MODE == 'label':
        LABELS[path] = os.environ.get('AGENTDASH_LABEL', '').strip()
        if not LABELS[path]:
            LABELS.pop(path, None)
        cache['_labels'] = LABELS
        save_cache(cache)
        print(f'label {"set" if LABELS.get(path) else "cleared"} for {sid}')
        sys.exit(0)
    ent = cache.get(path) or {}
    # styling handed in by the shell so NO_COLOR/--plain stay honored
    CB = os.environ.get('AD_B', '')
    CD = os.environ.get('AD_D', '')
    CY = os.environ.get('AD_Y', '')
    CR = os.environ.get('AD_R', '')
    CN = os.environ.get('AD_N', '')
    if MODE == 'why':
        how = m.get('how', 'unknown')
        how_txt = {
            'fd': 'process holds the jsonl open under its project dir (exact)',
            'cwd': 'only one live session file in the project dir for its cwd (exact)',
            'start-ts': 'session first-entry timestamp matches process start ±5min (confident)',
            'sticky': "kept last draw's guess (sticky heuristic: could be wrong)",
            'recency': 'newest unclaimed file written since process start (heuristic)',
            'meta': 'session file records this cwd in its metadata '
                    '(exact when the filename timestamp also matches process start)',
        }.get(how, how)
        print(f'{CB}pid {pid}{CN} {CD}→ {path}{CN}')
        print()
        print(f'  Pairing:  {how_txt}')
        print(f'  Model:    "{ent.get("model") or "?"}": last assistant message in the session file')
        win, src = window_for(ent.get('model'))
        if ent.get('win'):
            win, src = ent['win'], 'recorded in the rollout file (exact)'
        if win and ent.get('ctx') and ent['ctx'] > win:
            win, src = 1_000_000, f'{src}, self-corrected to 1M (measured context exceeded it)'
        print(f'  Window:   {win or "?"}: {src or "unknown model"}')
        print(f'  Context:  {ent.get("ctx") or "?"} tokens in the most recent request (incl. cache)')
        print(f'  Tokens:   in={ent.get("in", 0)} out={ent.get("out", 0)}: summed usage blocks, deduped by message id')
        age = NOW - ent.get('mtime', NOW)
        print(f'  Status:   file written {ago(age)} ago, last entry type "{ent.get("last_type")}"')
        print()
        print(f'  {CD}values marked exact come from the file or /proc; heuristics say so{CN}')
        sys.exit(0)
    # show: styled like a settings/usage panel: label/value + a wide bar
    title = title_of(ent, path, LABELS) or '(untitled)'
    print(f'{CB}{title}{CN}')
    print()
    print(f'  Model:    {short_model(ent.get("model"))}')
    print(f'  Tokens:   {hum(ent.get("in", 0))} in / {hum(ent.get("out", 0))} out  '
          f'{CD}(input includes cache){CN}')
    win, _ = window_for(ent.get('model'))
    if ent.get('win'):
        win = ent['win']
    if win and ent.get('ctx') and ent['ctx'] > win:
        win = 1_000_000
    if win and ent.get('ctx'):
        pct = min(round(ent['ctx'] * 100 / win), 100)
        filled = round(pct * 30 / 100)
        bc = CR if pct >= 85 else CY if pct >= 70 else ''
        print(f'  Context:  {bc}{"█" * filled}{CN}{CD}{"░" * (30 - filled)}{CN} {pct}% used')
    else:
        print(f'  Context:  {CD}unknown{CN}')
    print(f'  Last:     written {ago(NOW - ent.get("mtime", NOW))} ago')
    print(f'  Session:  {CD}{path}{CN}')
    print(f'  Resume:   {resume_cmd}')
    print()
    print(f'  {CB}Recent turns{CN}')
    try:
        size = os.path.getsize(path)
        with open(path, 'rb') as f:
            # a single entry can be huge (pasted images); take a wide tail
            f.seek(max(0, size - 4_000_000))
            lines = f.read().split(b'\n')
    except OSError:
        lines = []
    turns = []
    for ln in lines:
        obj = parse_line(ln)
        if not isinstance(obj, dict):
            continue
        t = obj.get('type')
        if t in ('user', 'assistant'):
            txt = first_user_text(obj)
            if txt and (t == 'assistant' or 'toolUseResult' not in obj):
                turns.append((t, ' '.join(txt.split())[:150]))
        elif t == 'event_msg':  # codex rollout shape
            pay = obj.get('payload') or {}
            if pay.get('type') in ('user_message', 'agent_message') and pay.get('message'):
                role = 'user' if pay['type'] == 'user_message' else 'assistant'
                turns.append((role, ' '.join(str(pay['message']).split())[:150]))
    for role, txt in turns[-6:]:
        print(f'    {role:>9}{CD}:{CN} {txt}')
    fts = first_ts(path)
    if fts and ent.get('mtime'):
        span = ent['mtime'] - fts
        ted = span / (18 * 60)
        if ted >= 2:
            print()
            print(f'  {CD}This session spans ~{ted:.0f}x a TED talk{CN}')
    sys.exit(0)

# ---- table mode -----------------------------------------------------------
rows = [l.rstrip('\n').split('\t') for l in sys.stdin if l.strip()]

claude_by_cwd = {}
for pid, kind, cwd, etimes in rows:
    if kind == 'claude':
        claude_by_cwd.setdefault(cwd, []).append((int(etimes), pid))

# Evidence chain for pairing a claude pid with its session file:
#   1. fd      : a jsonl open under /proc/<pid>/fd (exact)
#   2. cwd     : the project dir holds exactly one live candidate (exact)
#   3. start-ts: first entry timestamp ≈ process start, ±5min (confident;
#                 re-derived each draw, twin ties go to the freshest file)
#   4. sticky  : last draw's guess (heuristic, marked ?)
#   5. recency : newest unclaimed file written since proc start (marked ?)
pidmap = cache.get('_pidmap', {})
new_pidmap = {}
claude_path = {}  # pid -> (path, sure)

for cwd, procs in claude_by_cwd.items():
    paths = claude_paths_for(cwd)
    proj = proj_dir(cwd)
    claimed = set()

    def assign(pid, path, start, sure, how, cwd=cwd):
        claude_path[pid] = (path, sure)
        claimed.add(path)
        new_pidmap[pid] = {'path': path, 'start': start, 'sure': sure, 'cwd': cwd, 'how': how}

    def live_candidates(start):
        # a live session's file must have been written since its process began
        return [p for p in paths if p not in claimed and mtime_of(p) >= start - 60]

    pending = []
    for et, pid in sorted(procs):  # newest process first
        start = round(NOW - et)
        p = fd_session(pid, proj)
        if p and p not in claimed:
            assign(pid, p, start, True, 'fd')
        else:
            pending.append((pid, start))
    rest = []
    for pid, start in pending:
        cands = live_candidates(start)
        if len(cands) == 1:
            assign(pid, cands[0], start, True, 'cwd')
        else:
            rest.append((pid, start))
    rest2 = []
    for pid, start in rest:
        cands = [(abs(first_ts(p) - start), -mtime_of(p), p)
                 for p in paths
                 if p not in claimed and first_ts(p)
                 and abs(first_ts(p) - start) <= TS_SLACK]
        if cands:
            assign(pid, min(cands)[2], start, True, 'start-ts')
        else:
            rest2.append((pid, start))
    rest3 = []
    for pid, start in rest2:
        prev = pidmap.get(pid)
        if (prev and abs(prev.get('start', 0) - start) <= 5  # not a reused pid
                and prev.get('path') in paths and prev['path'] not in claimed):
            assign(pid, prev['path'], start, False, 'sticky')
        else:
            rest3.append((pid, start))
    for pid, start in rest3:
        cands = live_candidates(start)
        if cands:
            assign(pid, cands[0], start, False, 'recency')

cache['_pidmap'] = new_pidmap

# respawn detection: many fresh pids on one session file in a short window
# means something keeps relaunching the same task
seen_pids = cache.get('_pids_by_path') or {}

codex_located = {}
for pid, kind, cwd, etimes in rows:
    model = tokens = ctx = status = last = task = '-'
    ctxtok = '0'
    spark = ' ' * 8
    path, sure = None, False
    start = round(NOW - int(etimes))
    if kind == 'claude':
        path, sure = claude_path.get(pid, (None, False))
        task = '(no session found)'
    elif kind in AGENTS and 'locate' in AGENTS[kind]:
        if cwd not in codex_located:
            codex_located[cwd] = AGENTS[kind]['locate'](cwd, pid, start)
        path, sure = codex_located[cwd]
        if path:  # pid-addressed modes (show/why/resume/label) need the pairing
            new_pidmap[pid] = {'path': path, 'start': start, 'sure': sure,
                               'cwd': cwd, 'how': 'meta', 'kind': kind}
        else:
            task = '(no session found)'
    respawn_n = 0
    if path:
        rec = seen_pids.setdefault(path, {})
        if pid not in rec:
            rec[pid] = NOW
        seen_pids[path] = {k: v for k, v in rec.items() if NOW - v <= 900}
        respawn_n = sum(1 for v in seen_pids[path].values() if NOW - v <= 600)
        ent = scan_session(path, cache, kind)
        if ent:
            ent['cwd'] = cwd
            model, tokens, ctx, ctxtok, status, last, spark, task = \
                fields_of(ent, sure, respawn_n, path, LABELS)
    print(f'{pid}\t{model}\t{tokens}\t{ctx}\t{ctxtok}\t{status}\t{last}\t{spark}\t{task}')

cache['_pids_by_path'] = seen_pids
save_cache(cache)
PYEOF
}

# fills R_* row arrays plus AGENT_CWDS/AGENT_PIDS (used by collect_ports);
# rows come out urgency-sorted (needs-you first, idle last), stable by pid
collect_agents() {
  declare -gA PANE_BY_TTY=()
  local ptty pat psess pwin ppane
  while IFS='|' read -r ptty pat psess pwin ppane; do
    [ -n "$ptty" ] && PANE_BY_TTY[$ptty]="$pat|$psess|$pwin|$ppane"
  done < <(tmux list-panes -a \
    -F '#{pane_tty}|#{session_attached}|#{session_name}|#{window_index}|#{pane_id}' 2>/dev/null)

  AGENT_CWDS=(); AGENT_PIDS=(); AGENT_TTYS=()
  R_KIND=(); R_PID=(); R_TTY=(); R_GLYPH=(); R_NEED=(); R_ETS=(); R_LAST=()
  R_SPARK=(); R_MODEL=(); R_TOKENS=(); R_CTX=(); R_CTXTOK=(); R_STATUS=()
  R_CWD=(); R_TASK=(); R_TREECH=()
  COLLAPSED_NOTE=""

  local -a META=()
  local taskq="" line pid args kind tty ets ppid cwd pinfo
  mapfile -t AGENT_LINES < <(pgrep -af 'claude|codex|hermes' 2>/dev/null \
    | grep -vE 'pgrep|hermes-snap|shell-snapshot|node --ping|sandboxes/|/bin/bash -c|^[0-9]+ +tmux ')
  for line in "${AGENT_LINES[@]}"; do
    pid=${line%% *}; args=${line#* }
    kind=$(agent_kind_of "$args")
    [ -z "$kind" ] && continue
    read -r tty ets ppid <<< "$(ps -o tty=,etimes=,ppid= -p "$pid" 2>/dev/null)"
    [ -z "$tty" ] && continue   # died between pgrep and ps
    cwd=$(readlink "/proc/$pid/cwd" 2>/dev/null)
    AGENT_CWDS+=("$cwd")
    AGENT_PIDS+=("$pid")
    AGENT_TTYS+=("$tty")
    META+=("$pid|$kind|$tty|$ets|${ppid:-0}|$cwd|$args")
    case "$WRAPPER_KINDS" in
      *" $kind "*) ;;
      *) taskq+="$pid	$kind	$cwd	${ets:-0}"$'\n' ;;
    esac
  done

  declare -A MODELT=() TOKT=() CTXT=() CTXTOKT=() STATT=() LASTT=() SPARKT=() TASKT=()
  local model tokens ctx ctxtok status lastw spark task
  if [ -n "$taskq" ]; then
    while IFS=$'\t' read -r pid model tokens ctx ctxtok status lastw spark task; do
      [ -z "$pid" ] && continue
      MODELT[$pid]=$model; TOKT[$pid]=$tokens; CTXT[$pid]=$ctx; CTXTOKT[$pid]=$ctxtok
      STATT[$pid]=$status; LASTT[$pid]=$lastw; SPARKT[$pid]=$spark; TASKT[$pid]=$task
    done < <(printf '%s' "$taskq" | resolve_tasks)
  fi

  # urgency sort: respawn/stuck > waiting > working > unenriched > idle
  local m rank wrappers=0 unmatched=0
  local -a KEYED=()
  for m in "${META[@]}"; do
    IFS='|' read -r pid kind tty ets ppid cwd args <<< "$m"
    status=${STATT[$pid]:--}
    task=${TASKT[$pid]:--}
    case "$WRAPPER_KINDS" in
      *" $kind "*)
        # tree mode needs the wrappers visible: they are the parents
        if [ -z "$EXPAND" ] && [ -z "$TREE" ]; then wrappers=$((wrappers + 1)); continue; fi ;;
      *)
        if [ "$task" = "(no session found)" ] && [ -z "$EXPAND" ]; then
          unmatched=$((unmatched + 1)); continue
        fi ;;
    esac
    case "$status" in
      'stuck?'|respawn*) rank=0 ;;
      waiting) rank=1 ;;
      working) rank=2 ;;
      idle) rank=4 ;;
      *) rank=3 ;;
    esac
    KEYED+=("$rank $pid $m")
  done
  [ "$wrappers" -gt 0 ] && COLLAPSED_NOTE="+ $wrappers wrapper$( [ "$wrappers" -ne 1 ] && printf 's')"
  if [ "$unmatched" -gt 0 ]; then
    COLLAPSED_NOTE+="${COLLAPSED_NOTE:+ · }$unmatched unmatched"
  fi
  [ -n "$COLLAPSED_NOTE" ] && COLLAPSED_NOTE+=" (-a to list)"

  local glyph need prof
  while read -r rank pid m; do
    [ -z "$m" ] && continue
    IFS='|' read -r pid kind tty ets ppid cwd args <<< "$m"
    case "$kind" in
      hermes)
        prof=$(sed -nE 's/.* -p +([^ ]+).*/\1/p' <<< "$args")
        model="-"; tokens="-"; ctx="-"; ctxtok=0; status="-"; lastw="-"; spark="        "
        task="wrapper: hermes${prof:+ -p $prof}" ;;
      *)
        model=${MODELT[$pid]:--}; tokens=${TOKT[$pid]:--}
        ctx=${CTXT[$pid]:--}; ctxtok=${CTXTOKT[$pid]:-0}; status=${STATT[$pid]:--}
        lastw=${LASTT[$pid]:--}; spark=${SPARKT[$pid]:-        }
        task=${TASKT[$pid]:--} ;;
    esac
    case "$status" in waiting|'stuck?'|respawn*) need=yes ;; *) need=no ;; esac
    pinfo=${PANE_BY_TTY[/dev/$tty]:-}
    if [ -n "$pinfo" ]; then
      if [ "${pinfo%%|*}" -ge 1 ] 2>/dev/null; then glyph="●"; else glyph="○"; fi
    else
      glyph=" "
    fi
    R_KIND+=("$kind"); R_PID+=("$pid"); R_TTY+=("$tty"); R_GLYPH+=("$glyph")
    R_NEED+=("$need"); R_ETS+=("${ets:-0}"); R_LAST+=("$lastw"); R_SPARK+=("$spark")
    R_MODEL+=("$model"); R_TOKENS+=("$tokens"); R_CTX+=("$ctx"); R_CTXTOK+=("$ctxtok")
    R_STATUS+=("$status"); R_CWD+=("$cwd"); R_TASK+=("$task"); R_TREECH+=(" ")
  done < <(printf '%s\n' "${KEYED[@]}" | sort -k1,1n -k2,2n)
  [ -n "$TREE" ] && tree_order
}

# regroup rows so each agent sits under the wrapper that spawned it (walks
# the ppid chain; an agent whose ancestry hits a wrapper on the board is its
# child). Top-level rows keep the urgency order.
tree_order() {
  [ "${#R_PID[@]}" -eq 0 ] && return
  local p pp i j n name
  declare -A PP=() WRAP_AT=()
  while read -r p pp; do PP[$p]=$pp; done < <(ps -eo pid=,ppid= 2>/dev/null)
  for i in "${!R_PID[@]}"; do
    case "$WRAPPER_KINDS" in *" ${R_KIND[i]} "*) WRAP_AT[${R_PID[i]}]=$i ;; esac
  done
  [ "${#WRAP_AT[@]}" -eq 0 ] && return
  local -a PARENT=() ORDER=()
  for i in "${!R_PID[@]}"; do
    PARENT[i]=""
    [ -n "${WRAP_AT[${R_PID[i]}]:-}" ] && continue
    p=${PP[${R_PID[i]}]:-}; n=0
    while [ -n "$p" ] && [ "$p" -gt 1 ] 2>/dev/null && [ "$n" -lt 32 ]; do
      if [ -n "${WRAP_AT[$p]:-}" ]; then PARENT[i]=$p; break; fi
      p=${PP[$p]:-}; n=$((n + 1))
    done
  done
  for i in "${!R_PID[@]}"; do
    [ -n "${PARENT[i]}" ] && continue   # children render under their wrapper
    ORDER+=("$i")
    if [ -n "${WRAP_AT[${R_PID[i]}]:-}" ]; then
      for j in "${!R_PID[@]}"; do
        [ "${PARENT[j]}" = "${R_PID[i]}" ] && { ORDER+=("$j"); R_TREECH[j]="└"; }
      done
    fi
  done
  local -n arr
  for name in R_KIND R_PID R_TTY R_GLYPH R_NEED R_ETS R_LAST R_SPARK \
              R_MODEL R_TOKENS R_CTX R_CTXTOK R_STATUS R_CWD R_TASK R_TREECH; do
    local -n arr=$name
    local -a tmp=()
    for j in "${ORDER[@]}"; do tmp+=("${arr[j]}"); done
    arr=("${tmp[@]}")
    unset -n arr
  done
}

# fills PORT_INFO ("port|proc|pid|cwd|flags"); needs collect_agents first.
# flags: SUSPECT:dup-cwd, SUSPECT:no-agent, NEW (first seen since last run)
collect_ports() {
  # agents sit in ~ but do project work via subshells: descendant cwds count as agent-used
  local p k cwd
  declare -A KIDS=() DSEEN=()
  while read -r p k; do KIDS[$k]+="$p "; done < <(ps -eo pid=,ppid=)
  local -a dstack=("${AGENT_PIDS[@]}")
  while [ "${#dstack[@]}" -gt 0 ]; do
    p=${dstack[-1]}; unset 'dstack[-1]'
    # shellcheck disable=SC2086  # word-splitting the child list is intended
    for k in ${KIDS[$p]:-}; do
      [ -n "${DSEEN[$k]:-}" ] && continue
      DSEEN[$k]=1
      cwd=$(readlink "/proc/$k/cwd" 2>/dev/null)
      [ -n "$cwd" ] && AGENT_CWDS+=("$cwd")
      dstack+=("$k")
    done
  done
  local ACTIVE_DIRS
  ACTIVE_DIRS=$( { printf '%s\n' "${AGENT_CWDS[@]}";
                   tmux list-panes -a -F '#{pane_current_path}' 2>/dev/null; } | sort -u)
  is_active_dir() { # cwd matches (either direction of prefix) some live agent/tmux dir
    local d
    while read -r d; do
      # an agent idling in ~ or / does not own every project beneath it
      case "$d" in ''|/|"$HOME") continue ;; esac
      case "$1" in "$d"|"$d"/*) return 0 ;; esac
      case "$d" in "$1"/*) return 0 ;; esac
    done <<< "$ACTIVE_DIRS"
    return 1
  }
  local -a RAW=()
  mapfile -t RAW < <(ss -tlnp 2>/dev/null | tail -n +2 \
    | sed -nE 's/.*[:.]([0-9]+) +[^ ]+:[^ ]+ +users:\(\("([^"]+)",pid=([0-9]+).*/\1 \2 \3/p' \
    | sort -un)
  local PREV_PORTS=""
  [ -f "$CACHE_DIR/ports.state" ] && PREV_PORTS=$(cat "$CACHE_DIR/ports.state" 2>/dev/null)
  declare -A CWD_COUNT=()
  local line port proc pid flags
  local -a PRE=()
  for line in "${RAW[@]}"; do
    read -r port proc pid <<< "$line"
    cwd=$(readlink "/proc/$pid/cwd" 2>/dev/null)
    PRE+=("$port|$proc|$pid|$cwd")
    case "$cwd" in /code/*|/home/*/*) CWD_COUNT[$cwd]=$(( ${CWD_COUNT[$cwd]:-0} + 1 )) ;; esac
  done
  PORT_INFO=()
  local curset=""
  for line in "${PRE[@]}"; do
    IFS='|' read -r port proc pid cwd <<< "$line"
    curset+="$port "
    flags=""
    if [ -n "$PREV_PORTS" ] && ! grep -qw "$port" <<< "$PREV_PORTS"; then
      flags="NEW"
    fi
    if [ -n "$cwd" ] && [ "${CWD_COUNT[$cwd]:-0}" -ge 2 ]; then
      flags="${flags:+$flags,}SUSPECT:dup-cwd"
    fi
    # no-agent only fires for daemonized listeners (tty=?): a server with a live
    # controlling tty is someone's interactive foreground work, not an orphan
    case "$cwd" in
      /code/*|/home/*/*)
        if [ "$(ps -o tty= -p "$pid" 2>/dev/null | tr -d ' ')" = "?" ] \
            && ! is_active_dir "$cwd"; then
          flags="${flags:+$flags,}SUSPECT:no-agent"
        fi ;;
    esac
    PORT_INFO+=("$port|$proc|$pid|$cwd|$flags")
  done
  mkdir -p "$CACHE_DIR" && printf '%s\n' "$curset" > "$CACHE_DIR/ports.state" \
    && chmod 600 "$CACHE_DIR/ports.state" 2>/dev/null
}

# start `docker stats` in the background so its ~2s sampling overlaps the
# session-file scan instead of adding to it
prefetch_docker() {
  DOCKER_TMP="" DOCKER_PID=""
  if [ -z "${AGENTDASH_SKIP_DOCKER:-}" ] && command -v docker >/dev/null 2>&1; then
    DOCKER_TMP=$(mktemp)
    docker stats --no-stream --format '{{.Name}}|{{.MemUsage}}' > "$DOCKER_TMP" 2>/dev/null &
    DOCKER_PID=$!
  fi
}

# fills SANDBOX_INFO ("name|profile|up|mem|flag") unless docker is absent/skipped
collect_sandboxes() {
  SANDBOX_INFO=()
  SANDBOX_OK=""
  if [ -n "${AGENTDASH_SKIP_DOCKER:-}" ] || ! command -v docker >/dev/null 2>&1; then
    return
  fi
  SANDBOX_OK=1
  local MEMS="" CONTS name up prof mem flag PREV=""
  if [ -n "${DOCKER_TMP:-}" ]; then
    wait "$DOCKER_PID" 2>/dev/null
    MEMS=$(cat "$DOCKER_TMP" 2>/dev/null)
    rm -f "$DOCKER_TMP"; DOCKER_TMP=""
  else
    MEMS=$(docker stats --no-stream --format '{{.Name}}|{{.MemUsage}}' 2>/dev/null)
  fi
  CONTS=$(docker ps --format '{{.Names}}|{{.RunningFor}}' 2>/dev/null)
  [ -f "$CACHE_DIR/sandboxes.state" ] && PREV=$(cat "$CACHE_DIR/sandboxes.state" 2>/dev/null)
  local curset=""
  [ -z "$CONTS" ] && return
  while IFS='|' read -r name up; do
    [ -z "$name" ] && continue
    curset+="$name "
    prof=$(docker inspect "$name" --format '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}' 2>/dev/null \
      | sed -nE 's|.*/\.hermes/profiles/([^/]+)/.*|\1|p' | head -1)
    mem=$(grep "^$name|" <<< "$MEMS" | cut -d'|' -f2 | cut -d/ -f1 | tr -d ' ')
    flag=""
    if [ -n "$PREV" ] && ! grep -qw "$name" <<< "$PREV"; then flag="NEW"; fi
    SANDBOX_INFO+=("$name|${prof:-"(default)"}|$(fmt_runfor "$up")|$(fmt_mem "${mem:--}")|$flag")
  done <<< "$CONTS"
  mkdir -p "$CACHE_DIR" && printf '%s\n' "$curset" > "$CACHE_DIR/sandboxes.state" \
    && chmod 600 "$CACHE_DIR/sandboxes.state" 2>/dev/null
}

friendly_what() { # raw login WHAT → short name
  local w=$1 first
  first=${w%% *}
  case "$w" in
    -bash|bash|-zsh|zsh) printf 'shell' ;;
    tmux*) printf 'tmux' ;;
    *node_modules/*) local pkg=${w##*node_modules/}; printf '%s (npx)' "${pkg%%[/ ]*}" ;;
    */venv/bin/*) local exe=${w##*/venv/bin/}; printf '%s' "${exe%% *}" ;;
    *) printf '%s' "$(basename "$first" 2>/dev/null || printf '%s' "$first")" ;;
  esac
}

emit_json() {
  {
    local i m port proc pid cwd flags
    for i in "${!R_PID[@]}"; do
      printf 'A\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "${R_KIND[i]}" "${R_PID[i]}" "${R_TTY[i]}" "${R_GLYPH[i]}" "${R_NEED[i]}" \
        "${R_ETS[i]}" "${R_LAST[i]}" "${R_MODEL[i]}" "${R_TOKENS[i]}" "${R_CTX[i]}" \
        "${R_STATUS[i]}" "${R_CWD[i]}" "${R_TASK[i]}"
    done
    for m in "${PORT_INFO[@]}"; do
      IFS='|' read -r port proc pid cwd flags <<< "$m"
      printf 'P\t%s\t%s\t%s\t%s\t%s\n' "$port" "$proc" "$pid" "$cwd" "$flags"
    done
  } | python3 /dev/fd/3 3<<'JEOF'
import sys, json
GLYPH = {'●': 'attached', '○': 'detached', ' ': None}
agents, ports = [], []
for ln in sys.stdin:
    f = ln.rstrip('\n').split('\t')
    if f[0] == 'A' and len(f) >= 14:
        agents.append({'agent': f[1], 'pid': int(f[2]), 'tty': f[3],
                       'tmux': GLYPH.get(f[4], None), 'needs_you': f[5] == 'yes',
                       'uptime_s': int(f[6]),
                       'last_write': None if f[7] == '-' else f[7],
                       'model': None if f[8] == '-' else f[8],
                       'tokens': None if f[9] == '-' else f[9],
                       'ctx': None if f[10] == '-' else f[10],
                       'status': None if f[11] == '-' else f[11],
                       'cwd': f[12], 'task': f[13]})
    elif f[0] == 'P' and len(f) >= 6:
        ports.append({'port': int(f[1]), 'process': f[2], 'pid': int(f[3]),
                      'cwd': f[4] or None,
                      'flags': [s.replace('SUSPECT:', '') for s in f[5].split(',') if s]})
print(json.dumps({'schema_version': 1, 'agents': agents, 'ports': ports}, indent=2))
JEOF
}

status_cell() { # status_cell <status> <changed>: colored, char-padded to 10
  local s=$1 changed=$2 c=""
  case "$s" in
    working) c=$G ;;
    waiting) c=$Y ;;
    'stuck?'|respawn*) c=$R ;;
    idle|-) c=$D ;;
  esac
  [ "$changed" = yes ] && c="$B$c"
  printf '%s' "$c"
  pad "$s" 10
  printf '%s' "$N"
}

gather() { # the expensive half of a frame: every data source, no output
  prefetch_docker
  collect_agents
  collect_ports
  collect_sandboxes
}

render() { # the cheap half: draw the collected state (watch mode re-renders
           # on cursor movement without re-gathering)
  WIDTH=${COLUMNS:-$(tput cols 2>/dev/null || echo 120)}
  [ "$WIDTH" -lt 80 ] 2>/dev/null && WIDTH=80

  # ---- header: the only place for aggregates ------------------------------
  local i nneed=0 nwork=0 nidle=0 idlectx=0 needc=""
  for i in "${!R_PID[@]}"; do
    [ "${R_NEED[i]}" = yes ] && nneed=$((nneed + 1))
    case "${R_STATUS[i]}" in
      working) nwork=$((nwork + 1)) ;;
      idle) nidle=$((nidle + 1)); idlectx=$((idlectx + ${R_CTXTOK[i]:-0})) ;;
    esac
  done
  [ "$nneed" -gt 0 ] && needc=$R
  local idlectx_h=""
  if [ "$idlectx" -gt 0 ]; then
    idlectx_h=" · $(hum_ctx "$idlectx") ctx held idle"
  fi
  printf '%s%s %s%s · %s%s need you%s · %s working · %s idle%s · load %s\n' \
    "$B" "$(hostname)" "$(date +%H:%M)" "$N" \
    "$needc" "$nneed" "${needc:+$N}" "$nwork" "$nidle" "$idlectx_h" \
    "$(cut -d' ' -f1 /proc/loadavg)"

  # ---- agent table ---------------------------------------------------------
  local fixed=87 longcols=""
  [ -n "$LONGVIEW" ] && { fixed=109; longcols=1; }
  local taskw=$((WIDTH - fixed)); [ "$taskw" -lt 16 ] && taskw=16
  printf '\n'
  if [ -n "$longcols" ]; then
    printf '  %s%-6s   %-7s %-7s %-5s %-5s %-10s %-10s %-10s %8s %-10s %-16s %s%s\n' "$D" \
      "AGENT" "PID" "TTY" "UP" "LAST" "MODEL" "TOKENS" "CTX" "ACT" "STATUS" "CWD" "TASK" "$N"
  else
    printf '  %s%-6s   %-5s %-10s %-10s %-10s %8s %-10s %-16s %s%s\n' "$D" \
      "AGENT" "LAST" "MODEL" "TOKENS" "CTX" "ACT" "STATUS" "CWD" "TASK" "$N"
  fi
  if [ "${#R_PID[@]}" -eq 0 ] && [ -z "$COLLAPSED_NOTE" ]; then
    echo "  No agents running (looks for claude, codex, hermes processes)."
  fi
  local gc dim changed mark
  for i in "${!R_PID[@]}"; do
    gc=""
    if [ "${R_GLYPH[i]}" = "○" ] && [ "${R_NEED[i]}" = yes ]; then gc=$R; fi
    dim=""; case "${R_STATUS[i]}" in idle|-) dim=$D ;; esac
    changed=no
    if [ -n "${WATCHING:-}" ]; then
      [ -n "${PREV_STATUS[${R_PID[i]}]:-}" ] \
        && [ "${PREV_STATUS[${R_PID[i]}]}" != "${R_STATUS[i]}" ] && changed=yes
    fi
    mark=" "
    [ -n "${WATCHING:-}" ] && [ "${R_PID[i]}" = "${SELPID:-}" ] && mark="$B▸$N"
    printf '%s%s%s%-6s%s %s%s%s ' "$mark" "${R_TREECH[i]:- }" "$dim" "${R_KIND[i]}" "${dim:+$N}" \
      "$gc" "${R_GLYPH[i]}" "${gc:+$N}"
    [ -n "$longcols" ] && printf '%-7s %-7s %-5s ' \
      "${R_PID[i]}" "$(trunc "${R_TTY[i]}" 7)" "$(fmt_up "${R_ETS[i]}")"
    printf '%-5s %s%-10s%s %-10s ' "${R_LAST[i]}" \
      "$dim" "$(trunc "${R_MODEL[i]}" 10)" "${dim:+$N}" "${R_TOKENS[i]}"
    ctx_cell "${R_CTX[i]}"
    printf ' %s ' "${R_SPARK[i]}"
    status_cell "${R_STATUS[i]}" "$changed"
    printf ' %s %s%s%s\n' "$(pad "$(fish_path "${R_CWD[i]}" 16)" 16)" \
      "$dim" "$(trunc "${R_TASK[i]}" "$taskw")" "${dim:+$N}"
  done
  [ -n "$COLLAPSED_NOTE" ] && printf '  %s%s%s\n' "$D" "$COLLAPSED_NOTE" "$N"
  printf '  %s● tmux attached  ○ detached (red ○ = needs you, nobody watching)  ? = heuristic pairing%s\n' "$D" "$N"

  # ---- secondary sections: collapse when healthy ---------------------------
  local m port proc pid cwd flags name prof up mem flag
  local ports_hot="" sand_hot="" Z
  for m in "${PORT_INFO[@]}"; do
    flags=${m##*|}; [ -n "$flags" ] && ports_hot=1
  done
  for m in "${SANDBOX_INFO[@]}"; do
    flag=${m##*|}; [ -n "$flag" ] && sand_hot=1
  done
  Z=$(ps -eo pid,stat,args | awk '$2 ~ /^Z/ {print}')

  local TMUX_LINES LOGIN_LINES tn=0 ln_n=0
  TMUX_LINES=$(tmux ls -F '#{session_name}|#{?session_attached,attached,detached}|#{session_created}' 2>/dev/null)
  [ -n "$TMUX_LINES" ] && tn=$(wc -l <<< "$TMUX_LINES")
  local agent_tty_re
  agent_tty_re=$(printf '%s|' "${AGENT_TTYS[@]}")
  agent_tty_re=${agent_tty_re%|}
  LOGIN_LINES=$(w -h 2>/dev/null | awk -v re="^(${agent_tty_re:-NONE})$" '$2 !~ re')
  [ -n "$LOGIN_LINES" ] && ln_n=$(wc -l <<< "$LOGIN_LINES")

  local ok_parts=()

  # tmux
  if [ -n "$EXPAND" ] && [ -n "$TMUX_LINES" ]; then
    printf '\n%sTMUX SESSIONS%s\n' "$B" "$N"
    local now state created
    now=$(date +%s)
    while IFS='|' read -r name state created; do
      printf '  %-14s %-10s %s\n' "$name" "$state" "$(fmt_up $((now - created)))"
    done <<< "$TMUX_LINES"
  else
    ok_parts+=("tmux ×$tn")
  fi

  # logins (those not already shown as agent rows)
  if [ -n "$EXPAND" ] && [ -n "$LOGIN_LINES" ]; then
    printf '\n%sLOGIN SESSIONS%s\n' "$B" "$N"
    while read -r user tty _ _ idle _ _ rest; do
      [ -z "$user" ] && continue
      printf '  %-8s %-8s %-8s %s\n' "$user" "$tty" "$idle" "$(friendly_what "$rest")"
    done <<< "$LOGIN_LINES"
  else
    ok_parts+=("logins ×$ln_n")
  fi

  # sandboxes
  if [ -n "$SANDBOX_OK" ]; then
    if [ -n "$EXPAND" ] || [ -n "$sand_hot" ]; then
      printf '\n%sSANDBOXES%s\n' "$B" "$N"
      for m in "${SANDBOX_INFO[@]}"; do
        IFS='|' read -r name prof up mem flag <<< "$m"
        printf '  %-18s %-12s %-5s %-7s %s%s%s\n' "$name" "$prof" "$up" "$mem" \
          "${flag:+$Y}" "${flag:++ new}" "${flag:+$N}"
      done
      [ "${#SANDBOX_INFO[@]}" -eq 0 ] && echo "  (none)"
    else
      ok_parts+=("sandboxes ×${#SANDBOX_INFO[@]}")
    fi
  fi

  # ports
  if [ -n "$EXPAND" ] || [ -n "$ports_hot" ]; then
    printf '\n%sLISTENING PORTS%s\n' "$B" "$N"
    for m in "${PORT_INFO[@]}"; do
      IFS='|' read -r port proc pid cwd flags <<< "$m"
      local fc=""
      case "$flags" in *SUSPECT*) fc=$R ;; *NEW*) fc=$Y ;; esac
      printf '  %-6s %-14s %-8s %-34s %s%s%s\n' \
        "$port" "$(trunc "$proc" 14)" "$pid" "$(fish_path "${cwd:-?}" 34)" \
        "$fc" "$flags" "${fc:+$N}"
    done
    [ "${#PORT_INFO[@]}" -eq 0 ] && echo "  (none)"
  else
    ok_parts+=("ports ×${#PORT_INFO[@]}")
  fi

  # zombies: only ever printed when present
  if [ -n "$Z" ]; then
    printf '\n%sZOMBIES%s\n' "$B" "$N"
    while read -r line; do printf '  %szombie:%s %s\n' "$R" "$N" "$(trunc "$line" 100)"; done <<< "$Z"
  else
    ok_parts+=("no zombies")
  fi

  if [ "${#ok_parts[@]}" -gt 0 ]; then
    local ok_line
    ok_line=$(printf '%s · ' "${ok_parts[@]}")
    printf '\n  %sok: %s%s\n' "$D" "${ok_line% · }" "$N"
  fi
}

draw() { gather; render; }

notify_flips() { # OSC 9 when a session newly needs you (watch mode only)
  local i pid s prev
  for i in "${!R_PID[@]}"; do
    pid=${R_PID[i]}; s=${R_STATUS[i]}; prev=${PREV_STATUS[$pid]:-}
    if [ "${R_NEED[i]}" = yes ] && [ -n "$prev" ] && [ "$prev" = working ]; then
      [ -n "$NOTIFY" ] && osc9 "agentdash: ${R_KIND[i]} $pid $s: ${R_TASK[i]}"
    fi
    PREV_STATUS[$pid]=$s
  done
}

arg_to_pid() { # row number (1-based, current sort) or literal pid
  local a=$1
  if [ -n "$a" ] && [ "$a" -le "${#R_PID[@]}" ] 2>/dev/null && [ "$a" -ge 1 ] 2>/dev/null; then
    printf '%s' "${R_PID[$((a - 1))]}"
  else
    printf '%s' "$a"
  fi
}

cmd_go() {
  local pid i
  collect_agents
  pid=$(arg_to_pid "$1")
  if [ -z "$pid" ]; then  # no arg: first agent that needs a human
    for i in "${!R_PID[@]}"; do
      [ "${R_NEED[i]}" = yes ] && { pid=${R_PID[i]}; break; }
    done
    [ -z "$pid" ] && { echo "agentdash: nothing is waiting on you"; exit 0; }
  fi
  local tty pinfo sess win pane
  tty=$(ps -o tty= -p "$pid" 2>/dev/null | tr -d ' ')
  pinfo=${PANE_BY_TTY[/dev/$tty]:-}
  if [ -z "$pinfo" ]; then
    echo "agentdash: pid $pid (tty ${tty:-gone}) is not in a tmux pane" >&2
    exit 1
  fi
  IFS='|' read -r _ sess win pane <<< "$pinfo"
  if [ -n "${TMUX:-}" ]; then
    tmux switch-client -t "$sess" \; select-window -t "$sess:$win" \; select-pane -t "$pane"
  else
    printf 'tmux attach -t %s \\; select-window -t %s:%s \\; select-pane -t %s\n' \
      "$sess" "$sess" "$win" "$pane"
  fi
}

cmd_recap() {
  local spec=$1 now epoch="" out
  now=$(date +%s)
  spec=${spec#--since}
  spec=${spec# }
  if [[ $spec =~ ^([0-9]+)([mhd])$ ]]; then
    case ${BASH_REMATCH[2]} in
      m) epoch=$((now - BASH_REMATCH[1] * 60)) ;;
      h) epoch=$((now - BASH_REMATCH[1] * 3600)) ;;
      d) epoch=$((now - BASH_REMATCH[1] * 86400)) ;;
    esac
  elif [ -n "$spec" ]; then
    echo "agentdash: recap takes a window like 30m, 4h, 2d" >&2; exit 2
  fi
  printf '%sRECAP%s: sessions changed since %s\n' "$B" "$N" "${spec:-"last recap (≤7d)"}"
  out=$(AGENTDASH_MODE=recap AGENTDASH_SINCE=$epoch resolve_tasks </dev/null)
  if [ -z "$out" ]; then echo "  (nothing changed)"; return; fi
  local state age title last rcmd c
  while IFS=$'\t' read -r state age title last rcmd; do
    case $state in
      WAITING) c=$R ;; died?) c=$Y ;; working) c=$G ;; *) c=$D ;;
    esac
    printf '  %s%-8s%s %-4s %s%s%s\n' "$c" "$state" "$N" "$age" "$B" "$title" "$N"
    [ -n "$last" ] && printf '             %s%s%s\n' "$D" "$last" "$N"
    [ -n "$rcmd" ] && printf '             resume: %s\n' "$rcmd"
  done <<< "$out"
  printf '\n  %sresume lines are paste-ready · agentdash for the live board%s\n' "$D" "$N"
}

pid_mode() { # pid_mode <mode> <row|pid> [label]
  local mode=$1 pid
  [ -z "$2" ] && { echo "agentdash: $mode needs a row number or pid" >&2; exit 2; }
  collect_agents >/dev/null 2>&1 || true
  pid=$(arg_to_pid "$2")
  AGENTDASH_MODE=$mode AGENTDASH_PID=$pid AGENTDASH_LABEL=${3:-} \
    AD_B=$B AD_D=$D AD_Y=$Y AD_R=$R AD_N=$N resolve_tasks </dev/null
}

case "$ACTION" in
  go) cmd_go "$ACTION_ARG"; exit $? ;;
  recap) cmd_recap "$ACTION_ARG"; exit $? ;;
  resume|show|why) pid_mode "$ACTION" "$ACTION_ARG"; exit $? ;;
  label) pid_mode label "$ACTION_ARG" "$ACTION_ARG2"; exit $? ;;
esac
if [ -n "$ANYWAIT" ]; then
  collect_agents
  for i in "${!R_PID[@]}"; do
    [ "${R_NEED[i]}" = yes ] && exit 0
  done
  exit 1
fi
if [ -n "$JSON_MODE" ]; then
  collect_agents
  collect_ports
  emit_json
  exit 0
fi
if [ -z "$INTERVAL" ]; then draw; exit 0; fi

# ---- watch mode: an interactive loop over gather/render --------------------
# The cursor (▸) selects a row; keys act on it. Cursor movement only
# re-renders; data is re-gathered on the refresh tick and after actions
# that change what the board shows.
declare -A PREV_STATUS=()
WATCHING=1
SEL=1 SELPID="" FLASH="" KEY=""
FRAME=$(mktemp)
cleanup() { rm -f "$FRAME"; tput cnorm 2>/dev/null; tput rmcup 2>/dev/null; exit 0; }
trap cleanup INT TERM
tput smcup 2>/dev/null
tput civis 2>/dev/null

refresh() { gather; notify_flips; }

overlay() { # full-screen panel (show/why/resume); any key returns
  printf '\033[H\033[2J'
  AGENTDASH_MODE=$1 AGENTDASH_PID=$2 AD_B=$B AD_D=$D AD_Y=$Y AD_R=$R AD_N=$N \
    resolve_tasks </dev/null 2>&1 || true
  printf '\n%sany key to go back%s' "$D" "$N"
  read -rsn1 2>/dev/null || true
}

watch_go() { # jump to the agent's tmux pane without leaving the board
  local pid=$1 tty pinfo sess win pane
  tty=$(ps -o tty= -p "$pid" 2>/dev/null | tr -d ' ')
  pinfo=${PANE_BY_TTY[/dev/$tty]:-}
  if [ -z "$pinfo" ]; then FLASH="pid $pid is not in a tmux pane"; return; fi
  IFS='|' read -r _ sess win pane <<< "$pinfo"
  if [ -n "${TMUX:-}" ]; then
    tmux switch-client -t "$sess" \; select-window -t "$sess:$win" \; select-pane -t "$pane"
  else
    printf '\033[H\033[2J'
    printf 'this terminal is not inside tmux; attach with:\n\n'
    printf '  tmux attach -t %s \\; select-window -t %s:%s \\; select-pane -t %s\n' \
      "$sess" "$sess" "$win" "$pane"
    printf '\n%sany key to go back%s' "$D" "$N"
    read -rsn1 2>/dev/null || true
  fi
}

prompt_label() { # line-edit a task label without leaving watch mode
  local pid=$1 txt=""
  tput cnorm 2>/dev/null
  printf '\033[H\033[2J'
  read -rep "label for pid $pid (empty clears): " txt || txt=""
  AGENTDASH_MODE=label AGENTDASH_PID=$pid AGENTDASH_LABEL=$txt \
    resolve_tasks </dev/null >/dev/null 2>&1 || true
  tput civis 2>/dev/null
}

read_key() { # one key into KEY, arrow keys folded to j/k; empty on timeout
  KEY=""
  read -rsn1 -t "$INTERVAL" KEY 2>/dev/null || KEY=""
  if [ "$KEY" = $'\033' ]; then
    local k2=""
    read -rsn2 -t 0.01 k2 2>/dev/null || k2=""
    case "$k2" in '[A') KEY=k ;; '[B') KEY=j ;; *) KEY="" ;; esac
  fi
}

refresh
while true; do
  # the cursor follows its pid across re-sorts; if the pid left the board,
  # it falls back to the same row position
  NR=${#R_PID[@]}
  if [ "$NR" -eq 0 ]; then
    SELPID=""
  else
    IDX=""
    for i in "${!R_PID[@]}"; do
      [ "${R_PID[i]}" = "$SELPID" ] && { IDX=$((i + 1)); break; }
    done
    if [ -n "$IDX" ]; then
      SEL=$IDX
    else
      [ "$SEL" -gt "$NR" ] && SEL=$NR
      [ "$SEL" -lt 1 ] && SEL=1
      SELPID=${R_PID[SEL - 1]}
    fi
  fi
  render > "$FRAME"   # redirection, not a pipe: R_* arrays stay visible below
  ROWS=$(tput lines 2>/dev/null || echo 40)
  TOTAL=$(wc -l < "$FRAME")
  printf '\033[H'
  if [ "$TOTAL" -gt $((ROWS - 2)) ]; then
    # never overflow the screen: scrolling in the alt buffer pushes the
    # header out of view
    head -n $((ROWS - 3)) "$FRAME" | sed $'s/$/\033[K/'
    printf '%s↓ %s more below%s\033[K\n' "$D" $((TOTAL - ROWS + 3)) "$N"
  else
    sed $'s/$/\033[K/' "$FRAME"
  fi
  printf '\033[0J'
  if [ -n "$FLASH" ]; then
    printf '%s%s%s\033[K\n' "$Y" "$FLASH" "$N"
    FLASH=""
  else
    printf '%sj/k move · g go · s show · y why · L label · r resume · t tree · a all · q quit%s\033[K\n' \
      "$D" "$N"
  fi
  if [ -t 0 ]; then
    read_key
  else
    sleep "$INTERVAL"
    KEY=""
  fi
  case "$KEY" in
    q) cleanup ;;
    j) [ "$SEL" -lt "$NR" ] && SEL=$((SEL + 1)); SELPID=${R_PID[SEL - 1]:-} ;;
    k) [ "$SEL" -gt 1 ] && SEL=$((SEL - 1)); SELPID=${R_PID[SEL - 1]:-} ;;
    g) [ -n "$SELPID" ] && watch_go "$SELPID" ;;
    s) [ -n "$SELPID" ] && overlay show "$SELPID" ;;
    y) [ -n "$SELPID" ] && overlay why "$SELPID" ;;
    r) [ -n "$SELPID" ] && overlay resume "$SELPID" ;;
    L) [ -n "$SELPID" ] && { prompt_label "$SELPID"; refresh; } ;;
    l) if [ -n "$LONGVIEW" ]; then LONGVIEW=""; else LONGVIEW=1; fi ;;
    t) if [ -n "$TREE" ]; then TREE=""; else TREE=1; fi; refresh ;;
    a) if [ -n "$EXPAND" ]; then EXPAND=""; else EXPAND=1; fi; refresh ;;
    "") refresh ;;
  esac
done
