// Package ui is the interactive watch mode: a small raw-terminal loop over the
// shared board collector and table renderer. It samples foreground state on
// ticks and key actions; it installs no file watchers or daemons, and depends on
// nothing beyond golang.org/x/term (raw mode) and the standard library.
package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/history"
	"github.com/yrstm/agentdash/internal/procs"
	"github.com/yrstm/agentdash/internal/render"
)

// Config carries the flags into the program.
type Config struct {
	Interval time.Duration
	Theme    render.Theme
	Long     bool
	Tree     bool
	Expand   bool
	Notify   bool
	Hooks    Hooks
	Plain    bool
	Home     string
}

const (
	sortUrgency = iota
	sortLastWrite
	sortTokens
	sortUptime
	sortModes
)

var sortNames = []string{"urgency", "last-write", "tokens", "uptime"}

const (
	viewAgents = iota
	viewHistory
)

const (
	hSortLast = iota
	hSortStart
	hSortRoot
	hSortDuration
	hSortModes
)

var hSortNames = []string{"last-activity", "start-time", "root", "duration"}

// lineInput is a minimal single-line editor (append + backspace) for the filter
// and label prompts — enough to replace bubbles/textinput without the dependency.
type lineInput struct {
	prompt string
	value  []rune
}

func (li *lineInput) Value() string     { return string(li.value) }
func (li *lineInput) SetValue(s string) { li.value = []rune(s) }
func (li *lineInput) insert(r rune)     { li.value = append(li.value, r) }
func (li *lineInput) backspace() {
	if n := len(li.value); n > 0 {
		li.value = li.value[:n-1]
	}
}
func (li *lineInput) View() string { return li.prompt + string(li.value) }

// key is one decoded keypress: name is a stable token ("j", "up", "enter",
// "esc", "tab", "ctrl+c", or a single printable char); r/printable carry text.
type key struct {
	name      string
	r         rune
	printable bool
}

type model struct {
	cfg        Config
	b          *board.Board
	rows       []board.Row // filtered + sorted view of b.Rows
	hist       history.Result
	hrows      []history.Session
	viewMode   int
	hsel       int
	selPID     int
	sel        int
	scroll     int
	width      int
	height     int
	overlay    string
	help       bool
	flash      string
	filter     lineInput
	filtering  bool
	label      lineInput
	labeling   bool
	labelPID   int
	sortMode   int
	hSortMode  int
	prevStatus map[int]string
	hookPrev   map[int]string // last tick's status, for event-hook transitions
	procPids   string
}

// action tells the run loop what to do after a key is handled.
type action int

const (
	actNone action = iota
	actQuit
	actCollect // re-collect the board
	actHistory // re-collect the history view
)

// Run starts watch mode in a raw terminal; it returns when the user quits.
func Run(cfg Config) error {
	inFd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(inFd)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(inFd, old) }()
	out := os.Stdout
	_, _ = io.WriteString(out, "\x1b[?1049h\x1b[?25l")        // alt screen, hide cursor
	defer func() { _, _ = io.WriteString(out, "\x1b[?25h\x1b[?1049l") }()

	m := &model{
		cfg:        cfg,
		filter:     lineInput{prompt: "/"},
		label:      lineInput{prompt: "label: "},
		prevStatus: map[int]string{},
	}
	m.width, m.height = termSize()

	keys := make(chan key, 32)
	go readKeys(os.Stdin, keys)
	boards := make(chan *board.Board, 1)
	hists := make(chan history.Result, 1)
	collect := func() {
		exp, tree := m.cfg.Expand, m.cfg.Tree // snapshot before the goroutine
		go func() {
			boards <- board.Collect(time.Now().Unix(),
				board.Options{Expand: exp, Tree: tree, Sections: true})
		}()
	}
	collectHist := func() {
		home := m.cfg.Home
		go func() { hists <- history.Collect(home, liveSessionPaths()) }()
	}

	interval := time.NewTicker(cfg.Interval)
	defer interval.Stop()
	ptick := time.NewTicker(procTickDuration())
	defer ptick.Stop()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	collect()
	m.repaint(out)
	for {
		select {
		case b := <-boards:
			m.refresh(b)
			m.repaint(out)
		case h := <-hists:
			m.hist = h
			m.applyHistoryView()
			m.repaint(out)
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			switch m.handleKey(k) {
			case actQuit:
				return nil
			case actCollect:
				collect()
			case actHistory:
				collectHist()
			}
			m.repaint(out)
		case <-interval.C:
			collect()
			if m.viewMode == viewHistory {
				collectHist()
			}
		case <-ptick.C:
			if cur := pidSet(); cur != m.procPids {
				m.procPids = cur
				collect()
				if m.viewMode == viewHistory {
					collectHist()
				}
			}
		case <-winch:
			m.width, m.height = termSize()
			m.repaint(out)
		}
	}
}

func termSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

func procTickDuration() time.Duration {
	d := time.Second
	if v := os.Getenv("AGENTDASH_PROC_TICK"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			d = time.Duration(n) * time.Second
		}
	}
	return d
}

// readKeys decodes stdin bytes into key tokens. Terminals deliver an escape
// sequence (arrows) in a single read, so decoding the whole buffer is enough.
func readKeys(r io.Reader, ch chan<- key) {
	buf := make([]byte, 64)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			decodeKeys(buf[:n], ch)
		}
		if err != nil {
			close(ch)
			return
		}
	}
}

func decodeKeys(b []byte, ch chan<- key) {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b: // escape
			if i+2 < len(b) && b[i+1] == '[' {
				switch b[i+2] {
				case 'A':
					ch <- key{name: "up"}
				case 'B':
					ch <- key{name: "down"}
				case 'C':
					ch <- key{name: "right"}
				case 'D':
					ch <- key{name: "left"}
				}
				if b[i+2] >= 'A' && b[i+2] <= 'D' {
					i += 3
					continue
				}
				// other CSI: skip to the final byte
				j := i + 2
				for j < len(b) && !(b[j] >= 0x40 && b[j] <= 0x7e) {
					j++
				}
				i = j + 1
				continue
			}
			ch <- key{name: "esc"}
			i++
		case c == '\r' || c == '\n':
			ch <- key{name: "enter"}
			i++
		case c == 0x7f || c == 0x08:
			ch <- key{name: "backspace"}
			i++
		case c == 0x03:
			ch <- key{name: "ctrl+c"}
			i++
		case c == '\t':
			ch <- key{name: "tab"}
			i++
		case c >= 0x20 && c < 0x7f:
			ch <- key{name: string(rune(c)), r: rune(c), printable: true}
			i++
		default: // UTF-8 multibyte (printable) or unknown control
			r, size := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError && size > 1 {
				ch <- key{name: string(r), r: r, printable: true}
				i += size
			} else {
				i++
			}
		}
	}
}

// repaint writes the current frame, translating to raw-mode line endings and
// clearing stale cells, so no alt-screen diffing library is needed.
func (m *model) repaint(w io.Writer) {
	lines := strings.Split(m.View(), "\n")
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, ln := range lines {
		b.WriteString(ln)
		b.WriteString("\x1b[K") // clear to end of line
		if i < len(lines)-1 {
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\x1b[J") // clear everything below
	_, _ = io.WriteString(w, b.String())
}

// refresh folds a freshly collected board into the model (notify, hooks, the
// previous-status maps), mirroring the old refreshMsg handler.
func (m *model) refresh(b *board.Board) {
	if m.cfg.Notify && m.b != nil {
		m.notifyFlips(b)
	}
	if m.cfg.Hooks.Any() {
		// hookPrev is the immediately-previous board, so a transition fires
		// exactly once — unlike prevStatus, which lags for the changed-row render.
		fireHooks(m.cfg.Hooks, m.hookPrev, b)
	}
	if m.b != nil {
		for _, r := range m.b.Rows {
			m.prevStatus[r.PID] = r.Status
		}
	}
	m.hookPrev = statusMap(b)
	m.b = b
	m.applyView()
}

func (m *model) notifyFlips(nb *board.Board) {
	for _, r := range nb.Rows {
		// only when nobody's attached to its pane — if you're watching, no ping
		if r.Need && m.prevStatus[r.PID] == "working" && r.Glyph != "●" {
			osc9(fmt.Sprintf("agentdash: %s %d %s: %s", r.Kind, r.PID, r.Status, r.Task))
		}
	}
}

// osc9 sends a desktop notification; inside tmux it needs the
// passthrough wrapping (and tmux 3.3+ `allow-passthrough on`).
func osc9(msg string) {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = tty.Close() }()
	if os.Getenv("TMUX") != "" {
		_, _ = fmt.Fprintf(tty, "\x1bPtmux;\x1b\x1b]9;%s\x07\x1b\\", msg)
	} else {
		_, _ = fmt.Fprintf(tty, "\x1b]9;%s\x07", msg)
	}
}

// pidSet is the cheap discovery-only scan the 1s tick compares.
func pidSet() string {
	now := time.Now().Unix()
	ps := make([]string, 0, 8)
	for _, p := range board.DiscoverPids(now) {
		ps = append(ps, strconv.Itoa(p))
	}
	sort.Strings(ps)
	return strings.Join(ps, ",")
}

func liveSessionPaths() map[string]bool {
	cache := board.LoadCacheForActions()
	live := map[string]bool{}
	for pid, p := range cache.PidMap {
		if _, err := os.Stat(filepath.Join(procs.Root(), pid)); err == nil && p.Path != "" {
			live[p.Path] = true
		}
	}
	return live
}

// applyView rebuilds the filtered, sorted row view and re-anchors the
// cursor to its pid (falling back to the same row position).
func (m *model) applyView() {
	if m.b == nil {
		return
	}
	rows := make([]board.Row, 0, len(m.b.Rows))
	q := strings.ToLower(m.filter.Value())
	for _, r := range m.b.Rows {
		if q == "" ||
			strings.Contains(strings.ToLower(r.Task), q) ||
			strings.Contains(strings.ToLower(r.Cwd), q) ||
			strings.Contains(strings.ToLower(r.Model), q) ||
			strings.Contains(strings.ToLower(r.Status), q) {
			rows = append(rows, r)
		}
	}
	switch m.sortMode {
	case sortLastWrite:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Last < rows[j].Last })
	case sortTokens:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].CtxTok > rows[j].CtxTok })
	case sortUptime:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Uptime > rows[j].Uptime })
	}
	if !m.cfg.Expand {
		rows = board.CollapseRuns(rows)
	}
	m.rows = rows
	if len(rows) == 0 {
		m.selPID = 0
		m.sel = 0
		return
	}
	for i, r := range rows {
		if r.PID == m.selPID {
			m.sel = i
			return
		}
	}
	if m.sel >= len(rows) {
		m.sel = len(rows) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
	m.selPID = rows[m.sel].PID
}

func (m *model) applyHistoryView() {
	rows := make([]history.Session, 0, len(m.hist.Sessions))
	q := strings.ToLower(m.filter.Value())
	for _, r := range m.hist.Sessions {
		if q == "" ||
			strings.Contains(strings.ToLower(r.Cwd), q) ||
			strings.Contains(strings.ToLower(r.Title), q) ||
			strings.Contains(strings.ToLower(r.Agent), q) ||
			strings.Contains(strings.ToLower(r.Model), q) ||
			strings.Contains(strings.ToLower(r.GitBranch), q) {
			rows = append(rows, r)
		}
	}
	switch m.hSortMode {
	case hSortStart:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Start > rows[j].Start })
	case hSortRoot:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Cwd < rows[j].Cwd })
	case hSortDuration:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Duration > rows[j].Duration })
	default:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Last > rows[j].Last })
	}
	m.hrows = rows
	if m.hsel >= len(rows) {
		m.hsel = len(rows) - 1
	}
	if m.hsel < 0 {
		m.hsel = 0
	}
}

// handleKey mutates model state for one keypress and returns what the run loop
// should do next.
func (m *model) handleKey(k key) action {
	if m.overlay != "" || m.help {
		m.overlay, m.help = "", false
		return actNone
	}
	if m.filtering {
		switch k.name {
		case "enter":
			m.filtering = false
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.reapplyFilter()
		case "backspace":
			m.filter.backspace()
			m.reapplyFilter()
		default:
			if k.printable {
				m.filter.insert(k.r)
				m.reapplyFilter()
			}
		}
		return actNone
	}
	if m.labeling {
		switch k.name {
		case "enter":
			m.labeling = false
			cache := board.LoadCacheForActions()
			out, err := board.SetLabel(cache, m.labelPID, m.label.Value(), float64(time.Now().Unix()))
			if err != nil {
				m.flash = err.Error()
			} else {
				m.flash = out
			}
			return actCollect
		case "esc":
			m.labeling = false
		case "backspace":
			m.label.backspace()
		default:
			if k.printable {
				m.label.insert(k.r)
			}
		}
		return actNone
	}

	switch k.name {
	case "q", "ctrl+c":
		return actQuit
	case "tab":
		if m.viewMode == viewAgents {
			m.viewMode = viewHistory
			m.scroll = 0
			return actHistory
		}
		m.viewMode = viewAgents
		m.scroll = 0
	case "j", "down":
		if m.viewMode == viewHistory {
			if m.hsel < len(m.hrows)-1 {
				m.hsel++
			}
		} else if m.sel < len(m.rows)-1 {
			m.sel++
			m.selPID = m.rows[m.sel].PID
		}
	case "k", "up":
		if m.viewMode == viewHistory {
			if m.hsel > 0 {
				m.hsel--
			}
		} else if m.sel > 0 {
			m.sel--
			m.selPID = m.rows[m.sel].PID
		}
	case "g":
		if m.viewMode == viewAgents && m.selPID != 0 {
			m.jump(m.selPID)
		}
	case "s":
		if m.viewMode == viewHistory {
			m.showHistoryDetail()
		} else {
			m.showOverlay("show")
		}
	case "y":
		if m.viewMode == viewAgents {
			m.showOverlay("why")
		}
	case "r":
		if m.viewMode == viewHistory {
			m.showHistoryResume()
		} else {
			m.showOverlay("resume")
		}
	case "L":
		if m.viewMode == viewAgents && m.selPID != 0 {
			m.labeling = true
			m.labelPID = m.selPID
			m.label.SetValue("")
		}
	case "l":
		m.cfg.Long = !m.cfg.Long
	case "t":
		if m.viewMode == viewHistory {
			return actNone
		}
		m.cfg.Tree = !m.cfg.Tree
		return actCollect
	case "a":
		if m.viewMode == viewHistory {
			return actNone
		}
		m.cfg.Expand = !m.cfg.Expand
		return actCollect
	case "/":
		m.filtering = true
	case "o":
		if m.viewMode == viewHistory {
			m.hSortMode = (m.hSortMode + 1) % hSortModes
			m.flash = "history sort: " + hSortNames[m.hSortMode]
			m.applyHistoryView()
		} else {
			m.sortMode = (m.sortMode + 1) % sortModes
			m.flash = "sort: " + sortNames[m.sortMode]
			m.applyView()
		}
	case "i":
		if m.viewMode == viewHistory {
			m.overlay = history.Disclosure + "\n" + m.cfg.Theme.D + "any key to go back" + m.cfg.Theme.N
		}
	case "?":
		m.help = true
	}
	return actNone
}

func (m *model) reapplyFilter() {
	if m.viewMode == viewHistory {
		m.applyHistoryView()
	} else {
		m.applyView()
	}
}

func (m *model) showOverlay(mode string) {
	if m.selPID == 0 {
		return
	}
	cache := board.LoadCacheForActions()
	now := float64(time.Now().Unix())
	var out string
	var err error
	switch mode {
	case "show":
		out, err = render.Show(cache, m.selPID, m.cfg.Theme, now)
	case "why":
		out, err = render.Why(cache, m.selPID, m.cfg.Theme, now)
	case "resume":
		mi, _, e := board.PidEntry(cache, m.selPID)
		if e != nil {
			err = e
		} else {
			out = board.ResumeCmd(mi) + "\n"
		}
	}
	if err != nil {
		m.flash = err.Error()
		return
	}
	t := m.cfg.Theme
	m.overlay = out + "\n" + t.D + "any key to go back" + t.N
}

func (m *model) selectedHistory() (history.Session, bool) {
	if m.hsel < 0 || m.hsel >= len(m.hrows) {
		return history.Session{}, false
	}
	return m.hrows[m.hsel], true
}

func (m *model) showHistoryDetail() {
	s, ok := m.selectedHistory()
	if !ok {
		return
	}
	state := "ended"
	if s.Live {
		state = "live"
	}
	t := m.cfg.Theme
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s%s\n\n", t.B, s.Title, t.N)
	fmt.Fprintf(&b, "  Agent:    %s (%s)\n", s.Agent, state)
	fmt.Fprintf(&b, "  Root:     %s\n", s.Cwd)
	fmt.Fprintf(&b, "  Repo:     %s\n", dash(s.Repo))
	fmt.Fprintf(&b, "  Session:  %s\n", s.SessionID)
	fmt.Fprintf(&b, "  Path:     %s\n", s.Path)
	fmt.Fprintf(&b, "  Model:    %s\n", dash(s.Model))
	fmt.Fprintf(&b, "  Branch:   %s\n", dash(s.GitBranch))
	fmt.Fprintf(&b, "  Tokens:   %s\n", dash(s.Tokens))
	fmt.Fprintf(&b, "  Context:  %s\n", dash(s.Ctx))
	fmt.Fprintf(&b, "  Start:    %s\n", time.Unix(s.Start, 0).Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "  Last:     %s\n", time.Unix(s.Last, 0).Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "  Duration: %s\n", render.FmtUp(s.Duration))
	fmt.Fprintf(&b, "  Messages: %d\n", s.Messages)
	fmt.Fprintf(&b, "  Resume:   %s\n", s.Resume)
	m.overlay = b.String() + "\n" + t.D + "any key to go back" + t.N
}

func (m *model) showHistoryResume() {
	s, ok := m.selectedHistory()
	if !ok {
		return
	}
	m.overlay = s.Resume + "\n\n" + m.cfg.Theme.D + "any key to go back" + m.cfg.Theme.N
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// jump switches the client to the agent's tmux pane.
func (m *model) jump(pid int) {
	var tty string
	for _, r := range m.rows {
		if r.PID == pid {
			tty = r.TTY
			break
		}
	}
	pane, ok := board.PaneForTTY("/dev/" + tty)
	if !ok {
		m.flash = fmt.Sprintf("pid %d is not in a tmux pane", pid)
		return
	}
	if os.Getenv("TMUX") != "" {
		if err := exec.Command("tmux", "switch-client", "-t", pane.Session, ";",
			"select-window", "-t", pane.Session+":"+pane.Window, ";",
			"select-pane", "-t", pane.PaneID).Run(); err != nil {
			m.flash = fmt.Sprintf("tmux jump failed: %v", err)
		}
		return
	}
	t := m.cfg.Theme
	m.overlay = fmt.Sprintf(
		"this terminal is not inside tmux; attach with:\n\n  tmux attach -t %s \\; select-window -t %s:%s \\; select-pane -t %s\n\n%sany key to go back%s",
		pane.Session, pane.Session, pane.Window, pane.PaneID, t.D, t.N)
}

const helpText = `agentdash watch mode

  tab             switch Agents / History
  j/k or arrows   move the cursor        g   jump to the agent's tmux pane
  s               drill-down panel       y   provenance panel
  r               resume command         L   edit the task label
  t               toggle tree view       l   toggle long view
  a               toggle expanded view   /   filter rows (Esc clears)
  o               cycle sort: urgency, last-write, tokens, uptime
  history: s details · r resume · i what this reads
  ?               this help              q   quit

any key to go back`

func (m *model) View() string {
	if m.b == nil {
		return "collecting…"
	}
	if m.help {
		return helpText
	}
	if m.overlay != "" {
		return m.overlay
	}
	if m.viewMode == viewHistory {
		return m.historyView()
	}

	view := *m.b
	view.Rows = m.rows
	banner := m.banner()
	frame := render.Table(&view, m.cfg.Theme, render.Opts{
		Long: m.cfg.Long, Expand: m.cfg.Expand, Width: m.width,
		Home: m.cfg.Home, SelPID: m.selPID, PrevStatus: m.prevStatus,
		Watching: true,
	})
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")

	// keep the cursor row inside the window
	avail := m.height - 2 - bannerLines(banner)
	if avail < 3 {
		avail = 3
	}
	selLine := 3 + m.sel
	if selLine-m.scroll >= avail {
		m.scroll = selLine - avail + 1
	}
	if selLine < m.scroll {
		m.scroll = selLine
	}
	if m.scroll > 0 && m.scroll > len(lines)-avail {
		m.scroll = max(0, len(lines)-avail)
	}
	end := m.scroll + avail
	more := 0
	if end < len(lines) {
		more = len(lines) - end
	} else {
		end = len(lines)
	}
	out := banner + strings.Join(lines[m.scroll:end], "\n") + "\n"
	t := m.cfg.Theme
	if more > 0 {
		out += fmt.Sprintf("%s↓ %d more below%s\n", t.D, more, t.N)
	}

	switch {
	case m.filtering:
		out += m.filter.View()
	case m.labeling:
		out += m.label.View()
	case m.flash != "":
		out += t.Y + m.flash + t.N
		m.flash = ""
	default:
		out += t.D + "j/k move · g go · s show · y why · L label · r resume · t tree · a all · / filter · o sort · ? help · q quit" + t.N
	}
	return clipLines(out, m.width)
}

func (m *model) historyView() string {
	banner := m.banner()
	frame := renderHistory(m.hrows, m.hist.Skipped, m.hsel, m.cfg.Theme, m.width, m.cfg.Home)
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	avail := m.height - 2 - bannerLines(banner)
	if avail < 3 {
		avail = 3
	}
	selLine := 3 + m.hsel
	if selLine-m.scroll >= avail {
		m.scroll = selLine - avail + 1
	}
	if selLine < m.scroll {
		m.scroll = selLine
	}
	if m.scroll > 0 && m.scroll > len(lines)-avail {
		m.scroll = max(0, len(lines)-avail)
	}
	end := m.scroll + avail
	more := 0
	if end < len(lines) {
		more = len(lines) - end
	} else {
		end = len(lines)
	}
	out := banner + strings.Join(lines[m.scroll:end], "\n") + "\n"
	t := m.cfg.Theme
	if more > 0 {
		out += fmt.Sprintf("%s↓ %d more below%s\n", t.D, more, t.N)
	}
	switch {
	case m.filtering:
		out += m.filter.View()
	case m.flash != "":
		out += t.Y + m.flash + t.N
		m.flash = ""
	default:
		out += t.D + "tab agents · j/k move · s details · r resume · / filter root/title · o sort · i disclosure · ? help · q quit" + t.N
	}
	return clipLines(out, m.width)
}

// clipLines clips every line to the pane width. A line wider than the terminal
// wraps onto a second row, which would desync the cursor-home repaint and stack
// frames on resize. ANSI-aware so colors survive the cut.
func clipLines(out string, width int) string {
	if width <= 0 {
		return out
	}
	ls := strings.Split(out, "\n")
	for i, ln := range ls {
		ls[i] = render.ClipANSI(ln, width)
	}
	return strings.Join(ls, "\n")
}

func (m *model) banner() string {
	if m.b == nil || m.cfg.Plain {
		return ""
	}
	return render.Banner(m.b, m.cfg.Theme, m.width) + "\n"
}

func bannerLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(strings.TrimRight(s, "\n"), "\n"))
}

func renderHistory(rows []history.Session, skipped []history.Session, sel int, t render.Theme, width int, home string) string {
	if width < 80 {
		width = 80
	}
	titleW := width - 112
	if titleW < 18 {
		titleW = 18
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%sHistory%s · %d conversations · %d skipped\n\n", t.B, t.N, len(rows), len(skipped))
	fmt.Fprintf(&b, "  %s%-8s %-5s %-4s %-6s %-6s %-10s %-7s %-18s %-24s %s%s\n",
		t.D, "AGENT", "STATE", "MSG", "AGE", "DUR", "TOKENS", "CTX", "REPO", "STARTED", "WORK", t.N)
	if len(rows) == 0 {
		b.WriteString("  No Claude or Codex session files found.\n")
	}
	now := time.Now().Unix()
	for i, r := range rows {
		mark := " "
		if i == sel {
			mark = t.B + "▸" + t.N
		}
		live := "ended"
		if r.Live {
			live = "live"
		}
		age := "-"
		if r.Last != 0 {
			age = render.FmtUp(now - r.Last)
		}
		repo := r.Repo
		if repo == "" {
			repo = r.Cwd
		}
		fmt.Fprintf(&b, "%s %-8s %-5s %-4d %-6s %-6s %-10s %-7s %-18s %-24s %s\n",
			mark, r.Agent, live, r.Messages, age, render.FmtUp(r.Duration),
			render.Trunc(dash(r.Tokens), 10), render.Trunc(dash(r.Ctx), 7),
			render.Trunc(render.FishPath(repo, home, 18), 18),
			render.Trunc(render.FishPath(r.Cwd, home, 24), 24),
			render.Trunc(r.Title, titleW))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\n  %s%d malformed/unusable session files skipped; press i for read/command disclosure%s\n", t.D, len(skipped), t.N)
	} else {
		fmt.Fprintf(&b, "\n  %sreads JSONL only; shells out to nothing; press i for disclosure%s\n", t.D, t.N)
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Headless runs watch mode without a terminal (CI, benchmarks, piping):
// the v1 behavior of re-rendering every interval.
func Headless(cfg Config) {
	var prev map[int]string
	for {
		b := board.Collect(time.Now().Unix(), board.Options{
			Expand: cfg.Expand, Tree: cfg.Tree, Sections: true})
		if cfg.Notify && prev != nil {
			for _, r := range b.Rows {
				if r.Need && prev[r.PID] == "working" && r.Glyph != "●" {
					osc9(fmt.Sprintf("agentdash: %s %d %s: %s", r.Kind, r.PID, r.Status, r.Task))
				}
			}
		}
		if cfg.Hooks.Any() {
			fireHooks(cfg.Hooks, prev, b)
		}
		prev = statusMap(b)
		if !cfg.Expand {
			b.Rows = board.CollapseRuns(b.Rows)
		}
		frame := render.Table(b, cfg.Theme, render.Opts{
			Long: cfg.Long, Expand: cfg.Expand, Width: 120, Home: cfg.Home})
		fmt.Print("\x1b[H\x1b[2J" + frame)
		time.Sleep(cfg.Interval)
	}
}
