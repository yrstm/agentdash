// Package health is a one-screen roll-up of per-agent warning signals, so a
// glance (or a cron job) can tell whether any live session needs attention.
// Every signal is derived read-only from data agentdash already has — the
// board's status/context, the session Entry's compaction count, the event
// log's status history, and the process table — and each flag carries the
// evidence behind it. Nothing here makes a network call.
package health

import (
	"strconv"
	"time"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/eventlog"
	"github.com/yrstm/agentdash/internal/parse"
)

// SchemaVersion is the --json contract version for `agentdash health`.
const SchemaVersion = 1

// Defaults for the flag thresholds; all overridable via Options.
const (
	defCtxPct        = 85
	defCompactionsHr = 2.0
	defTurnWindow    = 30
	defErrPct        = 20 // % of the last N turns
)

// Options configure a health check.
type Options struct {
	Home          string
	Now           int64
	CtxPct        int     // context-full flag threshold (default 85)
	CompactionsHr float64 // compactions/hour flag threshold (default 2)
	TurnWindow    int     // turns to scan for errors/interrupts (default 30)
	ErrPct        int     // error/interrupt rate flag threshold, % (default 20)
}

// Signal is one pass/flag check with the evidence behind it.
type Signal struct {
	Name   string `json:"name"`
	Flag   bool   `json:"flag"`
	Detail string `json:"detail"`
}

// Agent is one live agent's signals.
type Agent struct {
	PID     int      `json:"pid"`
	Kind    string   `json:"agent"`
	Task    string   `json:"task"`
	Cwd     string   `json:"cwd"`
	Flagged bool     `json:"flagged"`
	Signals []Signal `json:"signals"`
}

// Report is the completed roll-up. Flagged is true when any agent has a flag
// or any zombie MCP process was found — the process exit code keys off it.
type Report struct {
	Now       int64    `json:"-"`
	Agents    []Agent  `json:"agents"`
	ZombieMCP []string `json:"zombie_mcp"`
	Flagged   bool     `json:"flagged"`
}

func (o *Options) defaults() {
	if o.CtxPct == 0 {
		o.CtxPct = defCtxPct
	}
	if o.CompactionsHr == 0 {
		o.CompactionsHr = defCompactionsHr
	}
	if o.TurnWindow == 0 {
		o.TurnWindow = defTurnWindow
	}
	if o.ErrPct == 0 {
		o.ErrPct = defErrPct
	}
}

// Collect builds the report from a fresh board plus the derived signals.
func Collect(opt Options) Report {
	opt.defaults()
	b := board.Collect(opt.Now, board.Options{})
	cache := board.LoadCacheForActions()
	waiting := waitingTodayByPath(opt.Now) // session path -> seconds in `waiting` today

	rep := Report{Now: opt.Now}
	for _, r := range b.Rows {
		a := Agent{PID: r.PID, Kind: r.Kind, Task: r.Task, Cwd: r.Cwd}

		// stuck / respawn come straight off the board status.
		a.add("stuck", r.Status == "stuck?", "status is stuck? (quiet past the stuck threshold with no reply)")
		a.add("respawn", hasPrefix(r.Status, "respawn"), "status is "+r.Status+" (crash-looping)")

		path := ""
		var ent *parse.Entry
		if pi, e, err := board.PidEntry(cache, r.PID); err == nil {
			path, ent = pi.Path, e
		}

		// context near full.
		if ent != nil && ent.Win > 0 {
			pct := int(100 * ent.Ctx / ent.Win)
			a.add("ctx_high", pct >= opt.CtxPct, itoa(pct)+"% of the "+parse.Hum(ent.Win)+"-token window used")
		}

		// compaction frequency: summaries per hour over the session's lifetime.
		if ent != nil && ent.CompactionN > 0 {
			hrs := 0.0
			if path != "" {
				if first := parse.FirstTS(path); first > 0 {
					last := int64(ent.Mtime)
					if last < first {
						last = first
					}
					hrs = float64(last-first) / 3600
				}
			}
			rate := float64(ent.CompactionN)
			if hrs > 0.1 {
				rate = float64(ent.CompactionN) / hrs
			}
			a.add("compaction", rate >= opt.CompactionsHr,
				itoa(ent.CompactionN)+" compactions in "+dur(hrs)+" (memory keeps getting summarized away)")
		}

		// api errors / user interrupts in the last N turns (Claude transcripts).
		if path != "" {
			t := scanTail(path, opt.TurnWindow)
			if t.turns > 0 {
				a.add("api_errors", pct(t.apiErrors, t.turns) >= opt.ErrPct,
					itoa(t.apiErrors)+" API errors in the last "+itoa(t.turns)+" turns")
				a.add("interrupts", pct(t.interrupts, t.turns) >= opt.ErrPct,
					itoa(t.interrupts)+" user interrupts in the last "+itoa(t.turns)+" turns")
			}
		}

		// minutes spent waiting today (from the event log's status history).
		if secs, ok := waiting[path]; ok && path != "" {
			mins := secs / 60
			a.add("waiting_today", mins >= 30, itoa(int(mins))+"m spent waiting on you today")
		}

		for _, s := range a.Signals {
			if s.Flag {
				a.Flagged = true
			}
		}
		rep.Agents = append(rep.Agents, a)
		if a.Flagged {
			rep.Flagged = true
		}
	}

	rep.ZombieMCP = zombieMCP()
	if len(rep.ZombieMCP) > 0 {
		rep.Flagged = true
	}
	return rep
}

func (a *Agent) add(name string, flag bool, detail string) {
	a.Signals = append(a.Signals, Signal{Name: name, Flag: flag, Detail: detail})
}

// waitingTodayByPath sums, per session, the seconds spent in `waiting` since
// local midnight, reconstructed from the event log's status_change events. A
// session still waiting is counted up to now. Empty when the log is disabled
// or holds nothing for today.
func waitingTodayByPath(now int64) map[string]int64 {
	events := eventlog.Load(eventlog.LogPath())
	if len(events) == 0 {
		return nil
	}
	midnight := time.Unix(now, 0).Truncate(24 * time.Hour).Unix()
	// last-known status entry time per session
	type state struct {
		status string
		since  int64
	}
	cur := map[string]state{}
	out := map[string]int64{}
	addWaiting := func(path string, from, to int64) {
		if from < midnight {
			from = midnight
		}
		if to > from {
			out[path] += to - from
		}
	}
	for _, e := range events {
		if e.Type != "status_change" || e.SessionPath == "" {
			continue
		}
		ts := epochOf(e.TS)
		if ts == 0 {
			continue
		}
		if s, ok := cur[e.SessionPath]; ok && s.status == "waiting" {
			addWaiting(e.SessionPath, s.since, ts)
		}
		cur[e.SessionPath] = state{status: e.ToStatus, since: ts}
	}
	// sessions still waiting: count up to now
	for path, s := range cur {
		if s.status == "waiting" {
			addWaiting(path, s.since, now)
		}
	}
	return out
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func pct(n, total int) int {
	if total == 0 {
		return 0
	}
	return 100 * n / total
}

func itoa(n int) string { return strconv.Itoa(n) }

// epochOf parses an event log RFC3339 timestamp to epoch seconds (0 on failure).
func epochOf(ts string) int64 {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func dur(hrs float64) string {
	if hrs <= 0 {
		return "this session"
	}
	if hrs < 1 {
		return itoa(int(hrs*60)) + "m"
	}
	return itoa(int(hrs)) + "h"
}
