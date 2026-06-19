// agentdash: `w` for your AI agents. Linux-only, single static binary.
// Observes agents started any way (terminal, tmux, ssh, cron): read-only,
// zero-config, no daemon, zero API calls. It never launches or owns
// sessions. README.md documents every heuristic.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/jsonout"
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/render"
	"github.com/yrstm/agentdash/internal/ui"
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

const usageText = `agentdash: ` + "`w`" + ` for your AI agents (Linux-only, read-only, no daemon)

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
                        state; the agent row (schema_version 1) arrives on
                        stdin, AGENTDASH_EVENT/PID/TASK in the env
  --on-stuck <cmd>      with -w: like --on-needs-you, when status hits stuck?
  --any-waiting      exit 0 if any session needs you, 1 otherwise (for scripts)
  go [row|pid]       jump to the agent's tmux pane (no arg: first that needs you)
  show <row|pid>     drill-down: task, recent turns, session path, resume command
  why <row|pid>      provenance per cell: pairing evidence, value sources
  label <row|pid> <text>   set a persistent TASK label ("" clears)
  resume <row|pid>   print the ` + "`claude --resume`" + ` command (with cwd)
  recap [4h|30m|2d]  what changed since you last looked (default: last recap)
  update             reinstall the latest from @main — the ONE networked
                     command (the board itself never touches the network);
                     keeps your build tags, so Hermes stays Hermes
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
		case "update":
			runUpdate()
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

// runUpdate is the one deliberately networked path. The board and every
// observation mode stay zero-network (enforced by the no-network CI job); this
// shells out to the Go toolchain to reinstall agentdash, and only ever when the
// user explicitly types `agentdash update`. It reuses the running binary's build
// tags, so a Hermes build self-updates with -tags=hermes and keeps monitoring.
func runUpdate() {
	args := render.UpdateArgs()
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "agentdash: the `go` toolchain isn't on PATH; reinstall manually:\n  %s\n", render.UpdateCmd())
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "agentdash: %s\n", render.UpdateCmd())
	cmd := exec.Command("go", args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "agentdash: update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "agentdash: updated · run `agentdash --version` to confirm")
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
