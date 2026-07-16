package render

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/parse"
)

// Opts steers one frame's rendering.
type Opts struct {
	Long        bool
	Expand      bool
	Width       int
	Home        string         // for ~ abbreviation in paths
	SelPID      int            // watch-mode cursor; 0 = none
	PrevStatus  map[int]string // watch mode: bold a status that flipped
	Watching    bool
	Filter      string // watch mode: the active row filter, "" when none
	FilterTotal int    // watch mode: unfiltered row count behind the filter
}

// Table renders the whole frame (header, agent table, sections) exactly
// like the v1 render(); the TUI and the one-shot path share it.
func Table(b *board.Board, t Theme, o Opts) string {
	var w strings.Builder
	width := o.Width
	if width < 80 {
		width = 80
	}

	// ---- header: the only place for aggregates -----------------------------
	// Decompose "need you" into looping (crash-loop, red) and blocked
	// (waiting on you, yellow) so the count says which kind at a glance.
	var states []string
	if b.NLoop > 0 {
		states = append(states, fmt.Sprintf("%s%d looping%s", t.R, b.NLoop, t.N))
	}
	if b.NBlocked > 0 {
		states = append(states, fmt.Sprintf("%s%d blocked%s", t.Y, b.NBlocked, t.N))
	}
	states = append(states, fmt.Sprintf("%d working", b.NWork))
	states = append(states, fmt.Sprintf("%s%d idle%s", t.D, b.NIdle, t.N))
	idleH := ""
	if b.IdleCtx > 0 {
		held := fmt.Sprintf("%s ctx held idle", parse.Hum(b.IdleCtx))
		if b.NWork == 0 { // context piling up while nothing progresses
			held = t.Y + held + t.N
		}
		idleH = " · " + held
	}
	burnH := ""
	if b.BurnCtx > 0 {
		burn := fmt.Sprintf("%s ctx burning", parse.Hum(b.BurnCtx))
		if b.NLoop > 0 { // a crash-loop climbing tokens is the overnight-bill case
			burn = t.R + burn + t.N
		}
		burnH = " · " + burn
	}
	fmt.Fprintf(&w, "%s%s %s%s · %s%s%s · load %s\n",
		t.B, b.Host, time.Unix(b.Now, 0).Format("15:04"), t.N,
		strings.Join(states, " · "), idleH, burnH, b.Load)

	// ---- agent table --------------------------------------------------------
	fixed := 90
	if o.Long {
		fixed = 112
	}
	taskw := width - fixed
	if taskw < 16 {
		taskw = 16
	}
	w.WriteString("\n")
	if o.Long {
		fmt.Fprintf(&w, "  %s%-9s   %-7s %-7s %-5s %-5s %-10s %-10s %-10s %8s %-10s %-16s %s%s\n", t.D,
			"AGENT", "PID", "TTY", "UP", "LAST", "MODEL", "TOKENS", "CTX", "ACT", "STATUS", "REPO", "WORK", t.N)
	} else {
		fmt.Fprintf(&w, "  %s%-9s   %-5s %-10s %-10s %-10s %8s %-10s %-16s %s%s\n", t.D,
			"AGENT", "LAST", "MODEL", "TOKENS", "CTX", "ACT", "STATUS", "REPO", "WORK", t.N)
	}
	if len(b.Rows) == 0 && b.CollapsedNote == "" {
		if o.Filter != "" {
			// an active filter emptied the table: say so, or the summary line
			// above (which always counts the whole board) reads as a lie
			fmt.Fprintf(&w, "  0 of %d agents match %s%q%s — esc clears the filter.\n",
				o.FilterTotal, t.B, o.Filter, t.N)
		} else {
			w.WriteString("  No agents running (looks for claude, codex, hermes processes).\n")
		}
	}
	for _, r := range b.Rows {
		gc := ""
		if r.Glyph == "○" && r.Need {
			gc = t.R
		}
		dim := ""
		if r.Status == "idle" || r.Status == "-" {
			dim = t.D
		}
		changed := false
		if o.Watching && o.PrevStatus != nil {
			if prev, ok := o.PrevStatus[r.PID]; ok && prev != r.Status {
				changed = true
			}
		}
		mark := " "
		if o.Watching && r.PID == o.SelPID {
			mark = t.B + "▸" + t.N
		}
		dimN := ""
		if dim != "" {
			dimN = t.N
		}
		gcN := ""
		if gc != "" {
			gcN = t.N
		}
		name := r.Kind
		if r.Count > 1 {
			name = fmt.Sprintf("%s ×%d", r.Kind, r.Count)
		}
		fmt.Fprintf(&w, "%s%s%s%s%s %s%s%s ", mark, r.TreeCh, dim, Pad(name, 9), dimN, gc, r.Glyph, gcN)
		if o.Long {
			fmt.Fprintf(&w, "%-7d %-7s %-5s ", r.PID, Trunc(r.TTY, 7), FmtUp(r.Uptime))
		}
		fmt.Fprintf(&w, "%-5s %s%-10s%s %-10s ", r.Last, dim, Trunc(r.Model, 10), dimN, r.Tokens)
		w.WriteString(CtxCell(r.Ctx, t))
		fmt.Fprintf(&w, " %s ", r.Spark)
		w.WriteString(StatusCell(r.Status, changed, t))
		repo := r.Repo
		if repo == "" {
			repo = r.Cwd
		}
		fmt.Fprintf(&w, " %s %s%s%s\n", Pad(FishPath(repo, o.Home, 16), 16),
			dim, Trunc(CleanTask(r.Task), taskw), dimN)
	}
	if b.CollapsedNote != "" {
		fmt.Fprintf(&w, "  %s%s%s\n", t.D, b.CollapsedNote, t.N)
	}
	fmt.Fprintf(&w, "  %s● tmux attached  ○ detached (red ○ = needs you, nobody watching)  ? = heuristic pairing%s\n", t.D, t.N)

	// ---- secondary sections: collapse when healthy --------------------------
	portsHot := false
	for _, p := range b.Ports {
		if len(p.Flags) > 0 {
			portsHot = true
		}
	}
	sandHot := false
	for _, s := range b.Sandboxes {
		if s.New {
			sandHot = true
		}
	}

	var okParts []string

	if o.Expand && len(b.TmuxSessions) > 0 {
		fmt.Fprintf(&w, "\n%sTMUX SESSIONS%s\n", t.B, t.N)
		for _, s := range b.TmuxSessions {
			state := "detached"
			if s.Attached {
				state = "attached"
			}
			fmt.Fprintf(&w, "  %-14s %-10s %s\n", s.Name, state, FmtUp(b.Now-s.Created))
		}
	} else {
		okParts = append(okParts, fmt.Sprintf("tmux ×%d", len(b.TmuxSessions)))
	}

	if o.Expand && len(b.Logins) > 0 {
		fmt.Fprintf(&w, "\n%sLOGIN SESSIONS%s\n", t.B, t.N)
		for _, l := range b.Logins {
			from := l.From
			if from == "" {
				from = "local"
			}
			what, dim, dimN := FriendlyWhat(l.What), "", ""
			switch {
			case l.Stale: // utmp entry, no live process: a dropped login
				what, dim, dimN = "(stale)", t.D, t.N
			case l.Tmux != "": // name the tmux work this shell is driving
				what = "tmux·" + l.Tmux
			}
			fmt.Fprintf(&w, "  %s%-8s %-7s %-16s %-6s %s%s\n",
				dim, l.User, Trunc(l.TTY, 7), Trunc(from, 16), l.Idle, what, dimN)
		}
	} else {
		okParts = append(okParts, fmt.Sprintf("logins ×%d", len(b.Logins)))
	}

	if b.SandboxOK {
		if o.Expand || sandHot {
			fmt.Fprintf(&w, "\n%sSANDBOXES%s\n", t.B, t.N)
			for _, s := range b.Sandboxes {
				flag, fc, fn := "", "", ""
				if s.New {
					flag, fc, fn = "+ new", t.Y, t.N
				}
				fmt.Fprintf(&w, "  %-18s %-12s %-5s %-7s %s%s%s\n",
					s.Name, s.Profile, FmtUp(s.UpSecs), FmtMem(s.MemMiB), fc, flag, fn)
			}
			if len(b.Sandboxes) == 0 {
				w.WriteString("  (none)\n")
			}
		} else {
			okParts = append(okParts, fmt.Sprintf("sandboxes ×%d", len(b.Sandboxes)))
		}
	}

	if o.Expand || portsHot {
		fmt.Fprintf(&w, "\n%sLISTENING PORTS%s\n", t.B, t.N)
		for _, p := range b.Ports {
			flags := strings.Join(p.Flags, ",")
			fc := ""
			switch {
			case strings.Contains(flags, "SUSPECT"):
				fc = t.R
			case strings.Contains(flags, "NEW"):
				fc = t.Y
			}
			fcN := ""
			if fc != "" {
				fcN = t.N
			}
			cwd := p.Cwd
			if cwd == "" {
				cwd = "?"
			}
			fmt.Fprintf(&w, "  %-6d %-14s %-8d %-34s %s%s%s\n",
				p.Port, Trunc(p.Proc, 14), p.PID, FishPath(cwd, o.Home, 34), fc, flags, fcN)
		}
		if len(b.Ports) == 0 {
			w.WriteString("  (none)\n")
		}
	} else {
		okParts = append(okParts, fmt.Sprintf("ports ×%d", len(b.Ports)))
	}

	if len(b.Zombies) > 0 || len(b.Orphans) > 0 {
		fmt.Fprintf(&w, "\n%sZOMBIES & ORPHANS%s\n", t.B, t.N)
		for _, z := range b.Zombies {
			fmt.Fprintf(&w, "  %szombie:%s %s\n", t.R, t.N, Trunc(z, 100))
		}
		for _, o := range b.Orphans {
			fmt.Fprintf(&w, "  %sorphan wrapper (children dead):%s %s\n", t.Y, t.N, Trunc(o, 70))
		}
	} else {
		okParts = append(okParts, "no zombies")
	}

	if len(okParts) > 0 {
		fmt.Fprintf(&w, "\n  %sok: %s%s\n", t.D, strings.Join(okParts, " · "), t.N)
	}
	return w.String()
}

// CtxCell renders "40%" as a 10-cell bar+percent: yellow at 70, red at 85.
func CtxCell(raw string, t Theme) string {
	pct := -1
	if strings.HasSuffix(raw, "%") {
		if n, err := parsePct(raw); err == nil {
			pct = n
		}
	}
	if pct < 0 {
		return fmt.Sprintf("%-10s", "-")
	}
	if t.N == "" { // plain
		return fmt.Sprintf("%-10s", fmt.Sprintf("%d%%", pct))
	}
	var bar strings.Builder
	for i := 0; i < 5; i++ {
		if i*20 < pct {
			bar.WriteString("▓")
		} else {
			bar.WriteString("░")
		}
	}
	c := ""
	if pct >= 85 {
		c = t.R
	} else if pct >= 70 {
		c = t.Y
	}
	return fmt.Sprintf("%s%s %3d%%%s", c, bar.String(), pct, t.N)
}

func parsePct(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d%%", &n)
	return n, err
}

// StatusCell colors a status and pads it to 10 chars; changed adds bold.
func StatusCell(s string, changed bool, t Theme) string {
	c := ""
	switch {
	case s == "working":
		c = t.G
	case s == "waiting":
		c = t.Y
	case s == "stuck?" || strings.HasPrefix(s, "respawn"):
		c = t.R
	case s == "busy?" || s == "idle" || s == "-":
		c = t.D
	}
	if changed {
		c = t.B + c
	}
	return c + Pad(s, 10) + t.N
}

// FmtMem renders container memory in MiB compactly: 28M / 1.1G.
func FmtMem(mib float64) string {
	if mib <= 0 {
		return "-"
	}
	if mib >= 1024 {
		g := mib / 1024
		if g >= 10 {
			return fmt.Sprintf("%.0fG", g)
		}
		return fmt.Sprintf("%.1fG", g)
	}
	if mib >= 10 {
		return fmt.Sprintf("%.0fM", mib)
	}
	return fmt.Sprintf("%.1fM", mib)
}

var venvRe = regexp.MustCompile(`/venv/bin/([^ ]+)`)

// FriendlyWhat shortens a login's WHAT command to a recognizable name.
func FriendlyWhat(what string) string {
	first, _, _ := strings.Cut(what, " ")
	switch {
	case what == "-bash" || what == "bash" || what == "-zsh" || what == "zsh":
		return "shell"
	case strings.HasPrefix(what, "tmux"):
		return "tmux"
	case strings.Contains(what, "node_modules/"):
		pkg := what[strings.LastIndex(what, "node_modules/")+len("node_modules/"):]
		pkg = strings.FieldsFunc(pkg, func(r rune) bool { return r == '/' || r == ' ' })[0]
		return pkg + " (npx)"
	case strings.Contains(what, "/venv/bin/"):
		if m := venvRe.FindStringSubmatch(what); m != nil {
			return m[1]
		}
		return what
	default:
		return filepath.Base(first)
	}
}
