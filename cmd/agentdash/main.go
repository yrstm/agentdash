// agentdash: `w` for your AI agents. Linux and macOS, single static binary.
// Observes agents started any way (terminal, tmux, ssh, cron): read-only,
// zero-config, no daemon, zero API calls. It never launches or owns
// sessions. README.md documents every heuristic.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/config"
	"github.com/yrstm/agentdash/internal/drift"
	"github.com/yrstm/agentdash/internal/du"
	"github.com/yrstm/agentdash/internal/eventlog"
	"github.com/yrstm/agentdash/internal/grep"
	"github.com/yrstm/agentdash/internal/jsonout"
	"github.com/yrstm/agentdash/internal/memory"
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
	"github.com/yrstm/agentdash/internal/render"
	"github.com/yrstm/agentdash/internal/ui"
	"github.com/yrstm/agentdash/internal/usage"
)

var version = "2.3.1-dev"

// pseudoTSRe pulls the 14-digit UTC timestamp and 12-char hash out of a module
// pseudo-version (e.g. v0.0.0-20260619123456-abcdef012345), the form that
// `go install pkg@main` stamps when there is no VCS tree to read.
var pseudoTSRe = regexp.MustCompile(`-(\d{14})-([0-9a-f]{12})`)

// buildStamp reports the binary's build age in seconds, its short revision and
// the dirty flag, read from the embedded VCS stamp (a `go build`/`install` from
// a checkout) or, failing that, the module pseudo-version. ageSecs is -1 when
// the binary carries no build provenance at all. Local read only — no network,
// so the default board keeps its zero-network guarantee.
func buildStamp(now time.Time) (ageSecs int64, rev string, dirty bool) {
	ageSecs = -1
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var built time.Time
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				built = t
			}
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if built.IsZero() { // module-mode install: recover date+hash from the pseudo-version
		if m := pseudoTSRe.FindStringSubmatch(bi.Main.Version); m != nil {
			if t, err := time.Parse("20060102150405", m[1]); err == nil {
				built, rev = t, m[2]
			}
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if built.IsZero() {
		return
	}
	return int64(now.Sub(built).Seconds()), rev, dirty
}

// staleDays is the build-age threshold for the reinstall nudge (0 disables it).
func staleDays() int {
	if v := os.Getenv("AGENTDASH_STALE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 14
}

const usageText = `agentdash: ` + "`w`" + ` for your AI agents (Linux/macOS, read-only, no daemon)

usage: agentdash [flags | subcommand]
  (no args)          render the board once
  -w [secs]          watch mode (default 5s; q or Ctrl-C exits)
                     keys: j/k or arrows move the cursor · g go · s show
                     y why · L label · r resume · t tree · a all
                     / filter · o sort · ? help · q quit
  -a                 expand everything: collapsed rows and healthy sections
  -l                 long view: adds PID, TTY, UP columns
  -t, --tree         group agent rows under the wrapper that spawned them
  --json             machine-readable agents + ports (schema_version 1)
  --plain            no color, no glyphs (NO_COLOR is honored too)
  --notify           with -w: OSC 9 notification when an *unwatched* agent
                     (no one attached to its pane) flips to needs-you
                     to waiting (needs tmux 3.3+ ` + "`allow-passthrough on`" + `)
  --on-needs-you <cmd>  with -w: run <cmd> when an agent enters a needs-you
                        state (edge-triggered, debounced 60s per session); the
                        agent row (schema_version 1) arrives on stdin, with
                        AGENTDASH_EVENT/PID/TASK/AGENT/CWD/STATUS in the env
  --on-stuck <cmd>      with -w: like --on-needs-you, when status hits stuck?
  --any-waiting      exit 0 if any session needs you, 1 otherwise (for scripts)
  go [row|pid]       jump to the agent's tmux pane (no arg: first that needs you)
  show <row|pid>     drill-down: task, recent turns, session path, resume command
  why <row|pid>      provenance per cell: pairing evidence, value sources
  label <row|pid> <text>   set a persistent TASK label ("" clears)
  resume <row|pid>   print the ` + "`claude --resume`" + ` command (with cwd)
  recap [4h|30m|2d]  what changed since you last looked (default: last recap)
  docs [repo|.] [--json]
                     CLAUDE.md/AGENTS.md health: no arg = cross-project board;
                     a repo = its memory file change log (local, sampled;
                     --json emits schema_version 1 for tooling)
  inspect [--global] [--tree] [why <file>] [--json]
                     inventory all config files shaping agent behaviour:
                     CLAUDE.md, AGENTS.md, .cursor/rules, hooks, slash commands
  log [tail [N]] [--json]
                     event log: structural observations about live sessions
                     (AGENTDASH_MEM=0 disables · AGENTDASH_MEM_NO_PROMPTS=1
                     omits prompt excerpts for shared/screen-shared boxes)
  audit [--days N] [--min N] [--global] [--json]
                     config audit: finds instructions repeated in prompts but
                     absent from config (missing_rule), and config references
                     to paths that no longer exist (stale_rule)
  grep <pattern> [--role user|assistant] [--project <dir>] [--since 7d]
       [-n N] [--tools] [--json]
                     search past sessions of both agents (regexp over message
                     text; --tools widens it to tool payloads). One line per
                     matching session, newest first, with a resume command
  du [--json]        disk triage: agent file sizes by category, largest first,
                     with what each is and a suggested cleanup (never deletes)
  usage [--limit N] [--json]
                     local token-spend estimate from transcripts: 5h/7d totals
                     per model, 30m burn rate, per-session attribution, cache
                     stats. Estimate only — never provider-reported. --limit N
                     sets a 5h cap so it can project when the window fills
  --help | --version

config (~/.config/agentdash/context-windows.conf):
  <model-id-substring> <window-tokens>   # first match wins; self-learned
                                         # entries are appended automatically
environment:
  AGENTDASH_SKIP_DOCKER=1    skip the docker sandboxes section
  AGENTDASH_WORKING_SECS=60  file younger than this -> "working"
  AGENTDASH_IDLE_SECS=600    file older than this -> "idle"
  AGENTDASH_STUCK_SECS=90    quiet past this with no reply -> "stuck?", else "busy?"
  AGENTDASH_PROC_TICK=1      watch mode: seconds between /proc rescans
  AGENTDASH_EXPAND=1         behave as if -a was passed (always expand sections)
  AGENTDASH_STALE_DAYS=14    nudge to reinstall once the binary is older than
                             this many days (0 disables; no-network, build-age)
`

func main() {
	args := os.Args[1:]

	// subcommands take the front slot, like v1
	if len(args) > 0 {
		switch args[0] {
		case "go", "recap", "resume", "show", "why", "label":
			runAction(args[0], args[1:])
			return
		case "docs":
			runDocs(args[1:])
			return
		case "inspect":
			runInspect(args[1:])
			return
		case "log":
			runLog(args[1:])
			return
		case "audit":
			runAudit(args[1:])
			return
		case "grep":
			runGrep(args[1:])
			return
		case "du":
			runDu(args[1:])
			return
		case "usage":
			runUsage(args[1:])
			return
		}
	}

	var interval time.Duration
	var jsonMode, plain, notify, longView, expand, anyWait, tree, watch bool
	var onNeedsYou, onStuck string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			fmt.Print(usageText)
			return
		case "--version":
			fmt.Println("agentdash", version)
			return
		case "--json":
			jsonMode = true
		case "--plain", "--no-color":
			plain = true
		case "--notify":
			notify = true
		case "--on-needs-you":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "agentdash: --on-needs-you needs a command")
				os.Exit(2)
			}
			i++
			onNeedsYou = args[i]
		case "--on-stuck":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "agentdash: --on-stuck needs a command")
				os.Exit(2)
			}
			i++
			onStuck = args[i]
		case "--any-waiting":
			anyWait = true
		case "-a", "--all":
			expand = true
		case "-l", "--long":
			longView = true
		case "-t", "--tree":
			tree = true
		case "-w", "--watch":
			watch = true
			interval = 5 * time.Second
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					interval = time.Duration(n) * time.Second
					i++
				}
			}
		default:
			fmt.Fprintf(os.Stderr, "agentdash: unknown argument: %s (try --help)\n", args[i])
			os.Exit(2)
		}
	}

	if os.Getenv("AGENTDASH_EXPAND") != "" {
		expand = true
	}

	hooks := ui.Hooks{OnNeedsYou: onNeedsYou, OnStuck: onStuck}
	if hooks.Any() && !watch {
		fmt.Fprintln(os.Stderr, "agentdash: --on-needs-you/--on-stuck require -w (watch mode); ignored")
	}

	theme := render.NewTheme(plain || !term.IsTerminal(int(os.Stdout.Fd())))
	home, _ := os.UserHomeDir()
	now := time.Now().Unix()

	switch {
	case anyWait:
		b := board.Collect(now, board.Options{})
		if b.NNeed > 0 {
			os.Exit(0)
		}
		os.Exit(1)

	case jsonMode:
		b := board.Collect(now, board.Options{Expand: expand, Tree: tree})
		out, err := jsonout.Emit(b)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agentdash:", err)
			os.Exit(1)
		}
		fmt.Println(string(out))

	case watch:
		cfg := ui.Config{
			// match the one-shot path: also drop color when stdout isn't a
			// terminal, so piping `-w` (headless) doesn't leak escape codes.
			Interval: interval, Theme: render.NewTheme(plain || !term.IsTerminal(int(os.Stdout.Fd()))),
			Long: longView, Tree: tree, Expand: expand,
			Notify: notify, Hooks: hooks, Plain: plain, Home: home,
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			ui.Headless(cfg)
			return
		}
		if err := ui.Run(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "agentdash:", err)
			os.Exit(1)
		}

	default:
		b := board.Collect(now, board.Options{Expand: expand, Tree: tree, Sections: true})
		if !expand {
			b.Rows = board.CollapseRuns(b.Rows)
		}
		width := 120
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
			width = w
		} else if c := os.Getenv("COLUMNS"); c != "" {
			if n, err := strconv.Atoi(c); err == nil {
				width = n
			}
		}
		if !plain && term.IsTerminal(int(os.Stdout.Fd())) { // TTY only — never in --json/--plain/pipes
			fmt.Print(render.Banner(b, theme, width))
			age, rev, dirty := buildStamp(time.Now())
			fmt.Print(render.UpdateHint(theme, rev, dirty, age, staleDays()))
		}
		fmt.Print(render.Table(b, theme, render.Opts{
			Long: longView, Expand: expand, Width: width, Home: home}))
	}
}

// runDocs drives the `agentdash docs` subcommand: with no argument it shows
// the cross-project memory-health board (most-stale first); with a repo path or
// "." it shows that project's memory change log. Both sample fresh first, so an
// explicit inspection always reflects the current files. --json emits a stable
// schema_version 1 document for tooling instead of the table.
func runDocs(rest []string) {
	jsonMode := false
	repoArg := ""
	for _, a := range rest {
		if a == "--json" {
			jsonMode = true
		} else if repoArg == "" && a != "" {
			repoArg = a
		}
	}
	theme := render.NewTheme(jsonMode || !term.IsTerminal(int(os.Stdout.Fd())))
	now := time.Now()
	logPath := memory.LogPath()
	live := board.MemoryProjects(now.Unix())

	if repoArg != "" {
		proj := resolveProject(repoArg)
		memory.Sample(logPath, map[string]int{proj: live[proj]}, now)
		entries := memory.ProjectLog(logPath, proj)
		if jsonMode {
			out, err := memory.LogJSON(proj, entries, now)
			emitJSON(out, err)
			return
		}
		if len(entries) == 0 && len(memory.Locate(proj)) == 0 {
			fmt.Printf("  no CLAUDE.md / AGENTS.md found at %s\n", proj)
			return
		}
		fmt.Print(render.MemoryLog(proj, entries, theme))
		return
	}
	memory.Sample(logPath, live, now)
	rows := memory.BuildBoard(logPath, live, now)
	if jsonMode {
		out, err := memory.BoardJSON(rows, now)
		emitJSON(out, err)
		return
	}
	fmt.Print(render.MemoryBoard(rows, theme))
}

func emitJSON(out []byte, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentdash:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// resolveProject maps a memory argument ("." or a path) to a project root.
func resolveProject(arg string) string {
	if arg == "." {
		if wd, err := os.Getwd(); err == nil {
			arg = wd
		}
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		abs = arg
	}
	if r := paths.RepoRoot(abs); r != "" {
		return r
	}
	return abs
}

// runAction handles the pid-addressed subcommands and recap.
func runAction(action string, rest []string) {
	plainOut := !term.IsTerminal(int(os.Stdout.Fd()))
	theme := render.NewTheme(plainOut)
	now := time.Now().Unix()
	home, _ := os.UserHomeDir()

	if action == "recap" {
		runRecap(strings.Join(rest, " "), theme, now)
		return
	}

	// row numbers resolve against the current board order
	b := board.Collect(now, board.Options{})
	argToPid := func(a string) int {
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= len(b.Rows) {
			return b.Rows[n-1].PID
		}
		n, _ := strconv.Atoi(a)
		return n
	}

	if action == "go" {
		pid := 0
		if len(rest) > 0 && rest[0] != "" {
			pid = argToPid(rest[0])
		} else {
			for _, r := range b.Rows {
				if r.Need {
					pid = r.PID
					break
				}
			}
			if pid == 0 {
				fmt.Println("agentdash: nothing is waiting on you")
				return
			}
		}
		tty := ""
		for _, r := range b.Rows {
			if r.PID == pid {
				tty = r.TTY
			}
		}
		pane, ok := board.PaneForTTY("/dev/" + tty)
		if !ok {
			if tty == "" {
				tty = "gone"
			}
			fmt.Fprintf(os.Stderr, "agentdash: pid %d (tty %s) is not in a tmux pane\n", pid, tty)
			os.Exit(1)
		}
		if os.Getenv("TMUX") != "" {
			cmd := exec.Command("tmux", "switch-client", "-t", pane.Session, ";",
				"select-window", "-t", pane.Session+":"+pane.Window, ";",
				"select-pane", "-t", pane.PaneID)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "agentdash: tmux jump failed: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("tmux attach -t %s \\; select-window -t %s:%s \\; select-pane -t %s\n",
				pane.Session, pane.Session, pane.Window, pane.PaneID)
		}
		return
	}

	if len(rest) == 0 || rest[0] == "" {
		fmt.Fprintf(os.Stderr, "agentdash: %s needs a row number or pid\n", action)
		os.Exit(2)
	}
	pid := argToPid(rest[0])
	cache := board.LoadCacheForActions()
	var out string
	var err error
	switch action {
	case "resume":
		var m interface{ Path() string }
		_ = m
		mi, _, e := board.PidEntry(cache, pid)
		if e != nil {
			err = e
		} else {
			out = board.ResumeCmd(mi) + "\n"
		}
	case "show":
		out, err = render.Show(cache, pid, theme, float64(now))
	case "why":
		out, err = render.Why(cache, pid, theme, float64(now))
	case "label":
		label := ""
		if len(rest) > 1 {
			label = rest[1]
		}
		out, err = board.SetLabel(cache, pid, label, float64(now))
		out += "\n"
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(out)
	_ = home
}

// runInspect drives the `agentdash inspect` subcommand.
func runInspect(rest []string) {
	jsonMode := false
	treeView := false
	global := false
	whyFile := ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			jsonMode = true
		case "--tree":
			treeView = true
		case "--global":
			global = true
		case "why":
			if i+1 < len(rest) {
				i++
				whyFile = rest[i]
			}
		}
	}
	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	proj := paths.RepoRoot(wd)
	if proj == "" {
		proj = wd
	}
	theme := render.NewTheme(jsonMode || !term.IsTerminal(int(os.Stdout.Fd())))
	inv := config.Scan(proj, home, global)
	if jsonMode {
		out, err := config.JSON(inv)
		emitJSON(out, err)
		return
	}
	if whyFile != "" {
		fmt.Print(render.ConfigWhy(inv, whyFile, theme))
		return
	}
	fmt.Print(render.ConfigInventory(inv, theme, treeView))
}

// runLog drives the `agentdash log` subcommand.
func runLog(rest []string) {
	jsonMode := false
	tailN := 0
	doTail := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			jsonMode = true
		case "tail":
			doTail = true
			if i+1 < len(rest) {
				if n, err := strconv.Atoi(rest[i+1]); err == nil && n > 0 {
					tailN = n
					i++
				}
			}
			if tailN == 0 {
				tailN = 40
			}
		case "clear":
			fmt.Fprintln(os.Stderr, "agentdash mem clear: not implemented (delete the file manually to clear)")
			os.Exit(2)
		case "off":
			fmt.Fprintln(os.Stderr, "agentdash mem off: set AGENTDASH_MEM=0 in your shell to disable recording")
			os.Exit(2)
		}
	}
	theme := render.NewTheme(jsonMode || !term.IsTerminal(int(os.Stdout.Fd())))
	logPath := eventlog.LogPath()
	if doTail {
		events := eventlog.Tail(logPath, tailN)
		if jsonMode {
			out, err := eventlog.MarshalJSON(events)
			emitJSON(out, err)
			return
		}
		fmt.Print(render.EventLogTail(events, theme))
		return
	}
	sum := eventlog.Summarize(logPath)
	if jsonMode {
		out, err := eventlog.SummarizeJSON(sum)
		emitJSON(out, err)
		return
	}
	fmt.Print(render.EventLogSummary(sum, theme))
}

// runAudit drives the `agentdash audit` subcommand.
func runAudit(rest []string) {
	jsonMode := false
	global := false
	days := 7
	minN := 3
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			jsonMode = true
		case "--global":
			global = true
		case "--days":
			if i+1 < len(rest) {
				if n, err := strconv.Atoi(rest[i+1]); err == nil && n > 0 {
					days = n
					i++
				}
			}
		case "--min":
			if i+1 < len(rest) {
				if n, err := strconv.Atoi(rest[i+1]); err == nil && n > 0 {
					minN = n
					i++
				}
			}
		}
	}
	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	proj := paths.RepoRoot(wd)
	if proj == "" {
		proj = wd
	}
	theme := render.NewTheme(jsonMode || !term.IsTerminal(int(os.Stdout.Fd())))
	opt := drift.Options{
		Project:       proj,
		Home:          home,
		MinOccurrence: minN,
		WindowDays:    days,
		IncludeGlobal: global,
	}
	findings := drift.Detect(opt)
	if jsonMode {
		out, err := drift.JSON(findings)
		emitJSON(out, err)
		return
	}
	fmt.Print(render.DriftFindings(findings, proj, theme))
}

// runUsage drives the `agentdash usage` subcommand: a local, credential-free
// token-spend estimate from transcripts. It never reports provider figures.
func runUsage(rest []string) {
	jsonMode := false
	var limit int64
	if v := os.Getenv("AGENTDASH_USAGE_LIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			limit = n
		}
	}
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			jsonMode = true
		case "--limit":
			if i+1 < len(rest) {
				if n, err := strconv.ParseInt(rest[i+1], 10, 64); err == nil && n > 0 {
					limit = n
					i++
				}
			}
		}
	}
	home, _ := os.UserHomeDir()
	rep := usage.Collect(usage.Options{Home: home, Now: time.Now().Unix(), Limit: limit})
	if jsonMode {
		out, err := usage.JSON(rep)
		emitJSON(out, err)
		return
	}
	theme := render.NewTheme(!term.IsTerminal(int(os.Stdout.Fd())))
	printUsage(rep, theme)
}

func printUsage(rep usage.Report, t render.Theme) {
	fmt.Printf("%sagentdash usage%s · %sestimate from local transcripts, not provider-reported%s\n",
		t.B, t.N, t.D, t.N)
	fmt.Printf("%scannot see provider-side limits, other machines, or spend the transcripts don't record%s\n\n", t.D, t.N)

	fmt.Printf("  5h %s%s%s · 7d %s%s%s · burn %s%s%s/min (last 30m)\n",
		t.B, parse.Hum(rep.Total5h), t.N, t.B, parse.Hum(rep.Total7d), t.N,
		t.B, parse.Hum(int64(rep.BurnPerMin)), t.N)
	switch {
	case rep.Limit > 0 && rep.ProjFillSecs > 0:
		fmt.Printf("  at this rate the 5h window (cap %s) fills in ~%s\n",
			parse.Hum(rep.Limit), parse.Ago(rep.ProjFillSecs))
	case rep.Limit > 0:
		fmt.Printf("  5h window is %s of the %s cap; not burning right now\n",
			parse.Hum(rep.Total5h), parse.Hum(rep.Limit))
	default:
		fmt.Printf("  %spass --limit N (or AGENTDASH_USAGE_LIMIT) to project when the 5h window fills%s\n", t.D, t.N)
	}

	if len(rep.Models) > 0 {
		fmt.Printf("\n  %sby model%s (in incl. cache / out)\n", t.B, t.N)
		for _, m := range rep.Models {
			fmt.Printf("    %-14s 5h %s/%s · 7d %s/%s\n", m.Model,
				parse.Hum(m.In5h), parse.Hum(m.Out5h), parse.Hum(m.In7d), parse.Hum(m.Out7d))
		}
	}

	if len(rep.Sessions) > 0 {
		fmt.Printf("\n  %stop sessions, 5h window%s (share · agent · model · in/out · task)\n", t.B, t.N)
		for _, s := range rep.Sessions {
			tag := ""
			if s.IsSubagent {
				tag = " (subagent)"
			}
			fmt.Printf("    %s%4.0f%%%s %-6s %-12s %s/%s  %s%s%s%s\n",
				t.Y, s.SharePct, t.N, s.Agent, s.Model,
				parse.Hum(s.In), parse.Hum(s.Out), t.B, parse.Clean(s.Title, 48), tag, t.N)
		}
	}

	if len(rep.Projects) > 0 {
		fmt.Printf("\n  %scache hit ratio, 7d%s (cache read / (read+creation))\n", t.B, t.N)
		for _, p := range rep.Projects {
			line := fmt.Sprintf("    %-28s %3.0f%%", shortenHomeAbs(p.Project), 100*p.HitRatio)
			if p.Dropped {
				line += fmt.Sprintf("  %s⚠ dropped: last-day %.0f%% vs prior %.0f%% — an always-loaded file likely changed%s",
					t.Y, 100*p.RecentRatio, 100*p.PriorRatio, t.N)
			}
			fmt.Println(line)
		}
	}
}

// shortenHomeAbs shortens a project path with ~ using the current home.
func shortenHomeAbs(p string) string {
	home, _ := os.UserHomeDir()
	return shortenHome(p, home)
}

// runDu drives the `agentdash du` subcommand: a size breakdown of the files
// the agent CLIs accumulate, largest first, with cleanup guidance. Read-only —
// it prints suggested commands but never deletes.
func runDu(rest []string) {
	jsonMode := false
	for _, a := range rest {
		if a == "--json" {
			jsonMode = true
		}
	}
	home, _ := os.UserHomeDir()
	res := du.Collect(home, time.Now().Unix())
	if jsonMode {
		out, err := du.JSON(res)
		emitJSON(out, err)
		return
	}
	theme := render.NewTheme(!term.IsTerminal(int(os.Stdout.Fd())))
	printDu(res, theme, home)
}

func printDu(res du.Result, t render.Theme, home string) {
	fmt.Printf("%sagentdash du%s · total %s%s%s · nothing is deleted, these are suggestions\n\n",
		t.B, t.N, t.B, du.HumanBytes(res.Total), t.N)
	for _, c := range res.Categories {
		if !c.Present {
			fmt.Printf("  %s%6s%s  %s%-22s%s %s(absent)%s\n",
				t.D, "-", t.N, t.D, c.Name, t.N, t.D, t.N)
			continue
		}
		fmt.Printf("  %s%6s%s  %s%-22s%s %s%s (%d files)%s\n",
			t.B, du.HumanBytes(c.Bytes), t.N, t.B, c.Name, t.N, t.D, shortenHome(c.Path, home), c.Files, t.N)
		fmt.Printf("          %s%s%s\n", t.D, c.What, t.N)
		for _, it := range c.Top {
			fmt.Printf("            %s%6s  %s%s\n", t.D, du.HumanBytes(it.Bytes), shortenHome(it.Path, home), t.N)
		}
		if c.Cleanup != "" {
			fmt.Printf("          %scleanup:%s %s\n", t.Y, t.N, c.Cleanup)
		}
	}
}

// runGrep drives the `agentdash grep` subcommand: a structured regexp search
// across both agents' transcripts, newest session first.
func runGrep(rest []string) {
	var patStr, role, project, since string
	var jsonMode, tools bool
	maxN := 0
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; a {
		case "--json":
			jsonMode = true
		case "--tools":
			tools = true
		case "--role":
			if i+1 < len(rest) {
				i++
				role = rest[i]
			}
		case "--project":
			if i+1 < len(rest) {
				i++
				project = rest[i]
			}
		case "--since":
			if i+1 < len(rest) {
				i++
				since = rest[i]
			}
		case "-n":
			if i+1 < len(rest) {
				if n, err := strconv.Atoi(rest[i+1]); err == nil && n > 0 {
					maxN = n
					i++
				}
			}
		default:
			if strings.HasPrefix(a, "-") && patStr != "" {
				fmt.Fprintf(os.Stderr, "agentdash: grep: unknown flag %s\n", a)
				os.Exit(2)
			}
			if patStr == "" {
				patStr = a
			}
		}
	}
	if patStr == "" {
		fmt.Fprintln(os.Stderr, "agentdash: grep needs a pattern")
		os.Exit(2)
	}
	if role != "" && role != "user" && role != "assistant" {
		fmt.Fprintln(os.Stderr, "agentdash: grep --role takes user or assistant")
		os.Exit(2)
	}
	re, err := regexp.Compile(patStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentdash: grep: bad pattern: %v\n", err)
		os.Exit(2)
	}
	now := time.Now().Unix()
	var sinceTS int64
	if since != "" {
		d, ok := parseSince(since)
		if !ok {
			fmt.Fprintln(os.Stderr, "agentdash: grep --since takes a window like 30m, 4h, 7d")
			os.Exit(2)
		}
		sinceTS = now - d
	}
	home, _ := os.UserHomeDir()
	res := grep.Search(grep.Options{
		Home: home, Pattern: re, Role: role, Project: project,
		Since: sinceTS, Max: maxN, Tools: tools, Now: now,
	})
	if jsonMode {
		out, err := grep.JSON(res, patStr)
		emitJSON(out, err)
		return
	}
	theme := render.NewTheme(!term.IsTerminal(int(os.Stdout.Fd())))
	printGrep(res, theme, home)
}

func printGrep(res grep.Result, t render.Theme, home string) {
	if len(res.Hits) == 0 {
		fmt.Println("  no matching sessions")
		return
	}
	for _, h := range res.Hits {
		where := shortenHome(h.Cwd, home)
		if where == "" {
			where = "-"
		}
		fmt.Printf("  %s%-4s%s %-6s %s%s%s  %s%d×%s  %s%s%s\n",
			t.D, parse.Ago(h.AgeSecs), t.N, h.Agent,
			t.V, where, t.N, t.Y, h.Matches, t.N, t.B, h.Title, t.N)
		if h.Snippet != "" {
			fmt.Printf("       %s%s%s\n", t.D, h.Snippet, t.N)
		}
		fmt.Printf("       resume: %s\n", h.Resume)
	}
	if res.Truncated {
		fmt.Printf("\n  %s(stopped at -n; pass a larger -n or narrow with --project/--since)%s\n", t.D, t.N)
	}
}

// shortenHome renders an absolute path with ~ for the home dir, matching the
// board's cwd column.
func shortenHome(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

var sinceSpecRe = regexp.MustCompile(`^([0-9]+)([mhd])$`)

// parseSince turns a 30m/4h/7d window into seconds.
func parseSince(spec string) (int64, bool) {
	m := sinceSpecRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch m[2] {
	case "m":
		return n * 60, true
	case "h":
		return n * 3600, true
	case "d":
		return n * 86400, true
	}
	return 0, false
}

var recapSpecRe = regexp.MustCompile(`^([0-9]+)([mhd])$`)

func runRecap(spec string, t render.Theme, now int64) {
	spec = strings.TrimPrefix(spec, "--since")
	spec = strings.TrimSpace(spec)
	var since float64
	if m := recapSpecRe.FindStringSubmatch(spec); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		switch m[2] {
		case "m":
			since = float64(now - n*60)
		case "h":
			since = float64(now - n*3600)
		case "d":
			since = float64(now - n*86400)
		}
	} else if spec != "" {
		fmt.Fprintln(os.Stderr, "agentdash: recap takes a window like 30m, 4h, 2d")
		os.Exit(2)
	}
	label := spec
	if label == "" {
		label = "last recap (≤7d)"
	}
	fmt.Printf("%sRECAP%s: sessions changed since %s\n", t.B, t.N, label)
	items := board.Recap(since, float64(now))
	if len(items) == 0 {
		fmt.Println("  (nothing changed)")
		return
	}
	for _, it := range items {
		c := t.D
		switch it.State {
		case "WAITING":
			c = t.R
		case "died?":
			c = t.Y
		case "working":
			c = t.G
		}
		fmt.Printf("  %s%-8s%s %-4s %s%s%s\n", c, it.State, t.N, parse.Ago(it.AgeS), t.B, it.Title, t.N)
		if it.Last != "" {
			fmt.Printf("             %s%s%s\n", t.D, it.Last, t.N)
		}
		if it.Resume != "" {
			fmt.Printf("             resume: %s\n", it.Resume)
		}
	}
	fmt.Printf("\n  %sresume lines are paste-ready · agentdash for the live board%s\n", t.D, t.N)
}
