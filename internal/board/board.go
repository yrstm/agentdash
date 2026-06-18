// Package board assembles the data model one frame shows: discovered
// agents enriched from their session files, plus the secondary sections.
// render draws it, jsonout serializes it; neither collects anything.
package board

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
	"github.com/yrstm/agentdash/internal/procs"
)

const noSession = "(no session found)"

// Row is one agent line with every cell already derived.
type Row struct {
	Kind   string
	PID    int
	TTY    string
	Glyph  string // ● attached, ○ detached, " " no pane
	Need   bool
	Uptime int64
	Last   string
	Model  string
	Tokens string
	Ctx    string // "45%" or "-"
	CtxTok int64
	Spark  string
	Status string
	Cwd    string
	Repo   string
	Task   string
	TreeCh string
	Count  int // >1 when this row stands in for N identical collapsed agents
}

// SandboxRow is a docker container plus its NEW flag.
type SandboxRow struct {
	procs.Sandbox
	New bool
}

// CollapseRuns merges consecutive rows that render identically (same kind,
// model, tokens, status, task, cwd) into one row carrying a Count, so e.g.
// four identical respawns show as a single "codex ×4" line. Rows arrive
// urgency-sorted, so identical agents are already adjacent. The kept row is
// the first of each run (its PID drives g/s/y/r actions). Display-only: the
// table and TUI call this; --json keeps every row.
func CollapseRuns(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if n := len(out); n > 0 {
			p := &out[n-1]
			if p.Kind == r.Kind && p.Model == r.Model && p.Tokens == r.Tokens &&
				p.Status == r.Status && p.Task == r.Task && p.Cwd == r.Cwd {
				if p.Count == 0 {
					p.Count = 1
				}
				p.Count++
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// Board is everything a frame needs.
type Board struct {
	Rows          []Row
	CollapsedNote string
	Ports         []procs.Port
	Sandboxes     []SandboxRow
	SandboxOK     bool
	TmuxSessions  []procs.Session
	Logins        []procs.Login
	Zombies       []string
	Orphans       []string
	Host          string
	Load          string
	Now           int64
	NNeed         int
	NLoop         int // need-you subset: crash-looping (respawn)
	NBlocked      int // need-you subset: waiting/stuck on you
	NWork         int
	NIdle         int
	IdleCtx       int64 // context locked by idle agents (reclaimable)
	BurnCtx       int64 // context held by actively-running agents (working/loop)
}

// Options selects what to collect and how to filter rows.
type Options struct {
	Expand   bool
	Tree     bool
	Sections bool // tmux/logins/sandboxes/zombies (the table and TUI; --json skips them)
}

func envInt(name string, dflt float64) float64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return float64(n)
		}
	}
	return dflt
}

func home() string { h, _ := os.UserHomeDir(); return h }

func cachePath() string {
	return filepath.Join(home(), ".cache", "agentdash", "usage.json")
}

func confPath() string {
	return filepath.Join(home(), ".config", "agentdash", "context-windows.conf")
}

// ConfPath exposes the context-windows conf location for the panels.
func ConfPath() string { return confPath() }

var profileArgRe = regexp.MustCompile(` -p +([^ ]+)`)

// Collect performs the expensive half of a frame.
func Collect(now int64, opt Options) *Board {
	// start docker first so its socket round-trip overlaps the scan
	var sandboxes []procs.Sandbox
	sandboxOK := false
	sandboxDone := make(chan struct{})
	if opt.Sections && procs.DockerAvailable() {
		sandboxOK = true
		go func() { sandboxes = procs.Sandboxes(now); close(sandboxDone) }()
	} else {
		close(sandboxDone)
	}

	th := parse.Thresholds{
		WorkingSecs: envInt("AGENTDASH_WORKING_SECS", 60),
		StuckSecs:   envInt("AGENTDASH_STUCK_SECS", 90),
		IdleSecs:    envInt("AGENTDASH_IDLE_SECS", 600),
	}
	fnow := float64(now)
	h := home()
	overrides := parse.LoadOverrides(confPath())
	cache := parse.LoadCache(cachePath())
	agents := procs.Discover(now)
	sort.Slice(agents, func(i, j int) bool { return agents[i].PID < agents[j].PID })
	panes := procs.PanesByTTY()

	newPidMap := map[string]parse.PidInfo{}
	pairings := procs.PairClaude(agents, h, cache.PidMap, newPidMap)
	codexCands := map[string][]procs.CodexRollout{}
	codexRollouts := func(cwd string) []procs.CodexRollout {
		c, ok := codexCands[cwd]
		if !ok {
			c = procs.CodexRollouts(h, cwd)
			codexCands[cwd] = c
		}
		return c
	}

	// resolve every pairing first so the session files can be scanned in
	// one parallel pass; on a cold cache the wall time is the largest
	// file instead of the sum
	scanJobs := map[string]string{}
	for _, p := range agents {
		if procs.WrapperKinds[p.Kind] {
			continue
		}
		switch {
		case p.Kind == "claude":
			if pr := pairings[p.PID]; pr.Path != "" {
				scanJobs[pr.Path] = p.Kind
			}
		case isExternalKind(p.Kind):
			// read directly from its own store below, not the codex locator
		default:
			if path, ok := procs.MatchCodex(codexRollouts(p.Cwd), p.Start); ok {
				scanJobs[path] = p.Kind
			}
		}
	}
	parse.ScanMany(scanJobs, cache, fnow)

	// respawn detection: many fresh pids on one session file in a short
	// window means something keeps relaunching the same task
	seenPids := cache.PidsByPath
	if seenPids == nil {
		seenPids = map[string]map[string]float64{}
	}

	type built struct {
		row  Row
		rank int
	}
	var rows []built
	wrappers, unmatched := 0, 0
	repos := map[string]string{}

	for _, p := range agents {
		row := Row{Kind: p.Kind, PID: p.PID, TTY: p.TTY, Uptime: p.Uptime,
			Cwd: p.Cwd, Model: "-", Tokens: "-", Ctx: "-", Status: "-",
			Last: "-", Spark: strings.Repeat(" ", 8), Task: "-", TreeCh: " "}
		if repo, ok := repos[row.Cwd]; ok {
			row.Repo = repo
		} else {
			row.Repo = paths.RepoRoot(row.Cwd)
			repos[row.Cwd] = row.Repo
		}

		if procs.WrapperKinds[p.Kind] && !isExternalKind(p.Kind) {
			prof := ""
			if m := profileArgRe.FindStringSubmatch(p.Args); m != nil {
				prof = " -p " + m[1]
			}
			row.Task = "wrapper: " + p.Kind + prof
			if pane, ok := panes["/dev/"+p.TTY]; ok && pane.Session != "" {
				row.Task += " · tmux:" + pane.Session
			}
			if !opt.Expand && !opt.Tree {
				wrappers++
				continue
			}
		} else {
			var pairing procs.Pairing
			switch {
			case p.Kind == "claude":
				pairing = pairings[p.PID]
			case isExternalKind(p.Kind):
				if externalPair != nil {
					pairing, _ = externalPair(p, h, cache, newPidMap, repos, &row)
				}
			default:
				// codex pairs only when a rollout's start lines up with this
				// process; a same-cwd-only match is rejected so old processes in a
				// busy cwd (e.g. ~) don't inherit the newest unrelated rollout.
				if path, sure := procs.MatchCodex(codexRollouts(p.Cwd), p.Start); path != "" {
					pairing = procs.Pairing{Path: path, Sure: sure, How: "meta"}
					newPidMap[strconv.Itoa(p.PID)] = parse.PidInfo{
						Path: path, Start: float64(p.Start), Sure: sure,
						Cwd: p.Cwd, How: "meta", Kind: p.Kind}
				}
			}
			row.Task = noSession
			respawnN := 0
			if pairing.Path != "" {
				// only reliable pairings feed respawn detection, so an unsure or
				// heuristic match can't be miscounted as a crash loop
				if pairing.Sure {
					rec := seenPids[pairing.Path]
					if rec == nil {
						rec = map[string]float64{}
					}
					pidKey := strconv.Itoa(p.PID)
					if _, ok := rec[pidKey]; !ok {
						rec[pidKey] = fnow
					}
					for k, v := range rec {
						if fnow-v > 900 {
							delete(rec, k)
						}
					}
					seenPids[pairing.Path] = rec
					for _, v := range rec {
						if fnow-v <= 600 {
							respawnN++
						}
					}
				}
				// already scanned by the parallel pass above (external kinds are
				// read from their own store, which is authoritative for cwd)
				if ent := cache.Entries[pairing.Path]; ent != nil && ent.Kind == p.Kind {
					if !isExternalKind(p.Kind) {
						ent.Cwd = p.Cwd
					}
					fillCells(&row, ent, pairing, respawnN, fnow, th, overrides, cache)
				}
			}
			if row.Task == noSession && !opt.Expand {
				unmatched++
				continue
			}
		}

		switch {
		case row.Status == "stuck?" || strings.HasPrefix(row.Status, "respawn"):
			row.Need = true
		case row.Status == "waiting":
			row.Need = true
		}
		if pane, ok := panes["/dev/"+p.TTY]; ok {
			if pane.Attached {
				row.Glyph = "●"
			} else {
				row.Glyph = "○"
			}
		} else {
			row.Glyph = " "
		}
		rows = append(rows, built{row, rankOf(row.Status)})
	}

	cache.PidMap = newPidMap
	cache.PidsByPath = seenPids
	_ = cache.Save(cachePath(), fnow)

	// urgency sort: respawn/stuck > waiting > working > unenriched > idle;
	// stable by pid so rows keep their order between refreshes
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].rank != rows[j].rank {
			return rows[i].rank < rows[j].rank
		}
		return rows[i].row.PID < rows[j].row.PID
	})

	b := &Board{Load: procs.LoadAvg()}
	b.Host, _ = os.Hostname()
	for _, r := range rows {
		b.Rows = append(b.Rows, r.row)
		if r.row.Need {
			b.NNeed++
			if strings.HasPrefix(r.row.Status, "respawn") {
				b.NLoop++
				b.BurnCtx += r.row.CtxTok
			} else {
				b.NBlocked++
			}
		}
		switch r.row.Status {
		case "working", "busy?":
			b.NWork++
			b.BurnCtx += r.row.CtxTok
		case "idle":
			b.NIdle++
			b.IdleCtx += r.row.CtxTok
		}
	}
	var notes []string
	if wrappers > 0 {
		s := ""
		if wrappers != 1 {
			s = "s"
		}
		notes = append(notes, fmt.Sprintf("+ %d wrapper%s", wrappers, s))
	}
	if unmatched > 0 {
		notes = append(notes, fmt.Sprintf("%d unmatched", unmatched))
	}
	if len(notes) > 0 {
		b.CollapsedNote = strings.Join(notes, " · ") + " (-a to list)"
	}

	b.Ports = collectPorts(agents, h)
	b.Now = now

	if opt.Sections {
		b.TmuxSessions = procs.Sessions()
		excl := map[string]bool{}
		for _, a := range agents {
			excl[a.TTY] = true
		}
		b.Logins = procs.Logins(now, excl)
		b.Zombies = procs.Zombies()
		b.Orphans = procs.Orphans()
		<-sandboxDone
		b.SandboxOK = sandboxOK
		if sandboxOK {
			b.Sandboxes = flagNewSandboxes(sandboxes, h)
		}
	}
	if opt.Tree {
		treeOrder(b)
	}
	return b
}

// flagNewSandboxes marks containers not present in the previous run.
func flagNewSandboxes(sb []procs.Sandbox, h string) []SandboxRow {
	stateFile := filepath.Join(h, ".cache", "agentdash", "sandboxes.state")
	prev := map[string]bool{}
	hadState := false
	if b, err := os.ReadFile(stateFile); err == nil {
		hadState = true
		for _, n := range strings.Fields(string(b)) {
			prev[n] = true
		}
	}
	var out []SandboxRow
	var cur []string
	for _, s := range sb {
		out = append(out, SandboxRow{s, hadState && !prev[s.Name]})
		cur = append(cur, s.Name)
	}
	_ = os.MkdirAll(filepath.Dir(stateFile), 0o755) // best-effort state cache; WriteFile below surfaces a real failure
	if err := os.WriteFile(stateFile, []byte(strings.Join(cur, " ")+" \n"), 0o600); err == nil {
		_ = os.Chmod(stateFile, 0o600) // best-effort tighten past umask
	}
	return out
}

// treeOrder regroups rows so each agent sits under the wrapper that
// spawned it (walks the ppid chain); top-level rows keep urgency order.
func treeOrder(b *Board) {
	if len(b.Rows) == 0 {
		return
	}
	pp := procs.ParentMap()
	wrapAt := map[int]bool{}
	for _, r := range b.Rows {
		if procs.WrapperKinds[r.Kind] {
			wrapAt[r.PID] = true
		}
	}
	if len(wrapAt) == 0 {
		return
	}
	parent := map[int]int{}
	for i := range b.Rows {
		if wrapAt[b.Rows[i].PID] {
			continue
		}
		p := pp[b.Rows[i].PID]
		for n := 0; p > 1 && n < 32; n++ {
			if wrapAt[p] {
				parent[b.Rows[i].PID] = p
				break
			}
			p = pp[p]
		}
	}
	var out []Row
	for i := range b.Rows {
		if parent[b.Rows[i].PID] != 0 {
			continue // children render under their wrapper
		}
		out = append(out, b.Rows[i])
		if wrapAt[b.Rows[i].PID] {
			for j := range b.Rows {
				if parent[b.Rows[j].PID] == b.Rows[i].PID {
					child := b.Rows[j]
					child.TreeCh = "└"
					out = append(out, child)
				}
			}
		}
	}
	b.Rows = out
}

// DiscoverPids is the cheap discovery-only scan for the TUI's proc tick.
func DiscoverPids(now int64) []int {
	var out []int
	for _, p := range procs.Discover(now) {
		out = append(out, p.PID)
	}
	return out
}

// PaneForTTY resolves a /dev/pts/N to its tmux pane.
func PaneForTTY(tty string) (procs.Pane, bool) {
	p, ok := procs.PanesByTTY()[tty]
	return p, ok
}

func rankOf(status string) int {
	switch {
	case status == "stuck?" || strings.HasPrefix(status, "respawn"):
		return 0
	case status == "waiting":
		return 1
	case status == "working":
		return 2
	case status == "idle":
		return 4
	default:
		return 3
	}
}

func fillCells(row *Row, ent *parse.Entry, pairing procs.Pairing, respawnN int,
	now float64, th parse.Thresholds, overrides []parse.Override, cache *parse.Cache) {

	row.Model = parse.ShortModel(ent.Model)
	if ent.In != 0 || ent.Out != 0 {
		row.Tokens = parse.Hum(ent.In) + "/" + parse.Hum(ent.Out)
	}
	win, _ := parse.WindowFor(ent.Model, overrides)
	if ent.Win != 0 {
		win = ent.Win
	}
	// session files don't record 1M-context mode: if the measured context
	// already exceeds the assumed window, adopt the larger tier and remember it
	if win != 0 && ent.Ctx > win {
		win = 1_000_000
		parse.LearnWindow(confPath(), ent.Model, win, &overrides)
	}
	row.CtxTok = ent.Ctx
	if ent.Ctx != 0 && win != 0 {
		pct := math.RoundToEven(float64(ent.Ctx) * 100 / float64(win))
		if pct > 100 {
			pct = 100
		}
		row.Ctx = strconv.Itoa(int(pct)) + "%"
	}
	if title := parse.TaskOf(ent, pairing.Path, cache.Labels); title != "" {
		if pairing.Sure {
			row.Task = title
		} else {
			row.Task = title + " ?"
		}
	}
	row.Status = parse.StatusOf(ent, respawnN, now, th)
	row.Last = parse.Ago(int64(now - ent.Mtime))
	row.Spark = parse.SparkOf(ent.Hist)
}

func collectPorts(agents []procs.Proc, h string) []procs.Port {
	// agents sit in ~ but do project work via subshells: descendant cwds
	// count as agent-used
	var pids []int
	dirs := map[string]bool{}
	for _, a := range agents {
		pids = append(pids, a.PID)
		dirs[a.Cwd] = true
	}
	for _, k := range procs.Descendants(pids) {
		if c := procs.Cwd(k); c != "" {
			dirs[c] = true
		}
	}
	for _, p := range procs.PanePaths() {
		dirs[p] = true
	}
	var active []string
	for d := range dirs {
		active = append(active, d)
	}

	stateFile := filepath.Join(h, ".cache", "agentdash", "ports.state")
	var prev map[int]bool
	if b, err := os.ReadFile(stateFile); err == nil {
		prev = map[int]bool{}
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				prev[n] = true
			}
		}
	}
	ports := procs.CollectPorts(active, prev, h)
	var cur []string
	for _, p := range ports {
		cur = append(cur, strconv.Itoa(p.Port))
	}
	_ = os.MkdirAll(filepath.Dir(stateFile), 0o755) // best-effort state cache; WriteFile below surfaces a real failure
	if err := os.WriteFile(stateFile, []byte(strings.Join(cur, " ")+" \n"), 0o600); err == nil {
		_ = os.Chmod(stateFile, 0o600) // best-effort tighten past umask
	}
	return ports
}
