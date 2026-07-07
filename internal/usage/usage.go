// Package usage estimates token spend locally from the per-message usage
// blocks the agent CLIs already write — no credentials, no provider API, no
// network. Everything it reports is an estimate from transcripts on this
// machine: it cannot see provider-side limits, spend on other machines, or
// anything the transcripts do not record. Rolling 5-hour and 7-day windows,
// a 30-minute burn rate, a per-session attribution table, and per-project
// cache-hit stats, all derived read-only.
package usage

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
)

// SchemaVersion is the --json contract version for `agentdash usage`. Additive
// only, independent of the other commands' versions.
const SchemaVersion = 1

const (
	win5h   = int64(5 * 3600)
	win7d   = int64(7 * 86400)
	burnWin = int64(30 * 60)
	dayWin  = int64(86400)
)

// Options configure a report.
type Options struct {
	Home  string
	Now   int64
	Limit int64 // optional 5h-window token cap for the projection; 0 = unknown
	TopN  int   // attribution rows (0 -> 10)
}

// ModelUse is one model's windowed totals (in includes cache tokens, matching
// the board's accounting; out is output tokens).
type ModelUse struct {
	Model string `json:"model"`
	In5h  int64  `json:"in_5h"`
	Out5h int64  `json:"out_5h"`
	In7d  int64  `json:"in_7d"`
	Out7d int64  `json:"out_7d"`
}

// SessionUse is one session's spend inside the 5h window, for attribution.
type SessionUse struct {
	Title      string  `json:"title"`
	Agent      string  `json:"agent"`
	Model      string  `json:"model"`
	In         int64   `json:"in"`
	Out        int64   `json:"out"`
	SharePct   float64 `json:"share_pct"`
	IsSubagent bool    `json:"is_subagent"`
}

// ProjectCache is one project's cache-hit accounting over 7 days, with a flag
// when the hit ratio dropped sharply in the last day.
type ProjectCache struct {
	Project       string  `json:"project"`
	CacheRead     int64   `json:"cache_read"`
	CacheCreation int64   `json:"cache_creation"`
	HitRatio      float64 `json:"hit_ratio"`
	RecentRatio   float64 `json:"recent_ratio"`
	PriorRatio    float64 `json:"prior_ratio"`
	Dropped       bool    `json:"dropped"`
}

// Report is a completed usage estimate.
type Report struct {
	Now          int64          `json:"-"`
	Limit        int64          `json:"limit"`
	Models       []ModelUse     `json:"models"`
	BurnPerMin   float64        `json:"burn_per_min"`
	Total5h      int64          `json:"total_5h"`
	Total7d      int64          `json:"total_7d"`
	ProjFillSecs int64          `json:"proj_fill_secs"` // >0 only when Limit>0 and burning
	Sessions     []SessionUse   `json:"sessions"`
	Projects     []ProjectCache `json:"projects"`
}

// event is one usage-bearing message, flattened across both agents.
type event struct {
	ts            int64
	model         string
	in            int64 // input + cache read + cache creation
	out           int64
	cacheRead     int64
	cacheCreation int64
}

// sessionAgg accumulates one transcript's identity and 5h spend.
type sessionAgg struct {
	title      string
	agent      string
	model      string
	project    string
	in5h       int64
	out5h      int64
	isSubagent bool
}

// Collect scans both transcript stores and builds the report.
func Collect(opt Options) Report {
	if opt.TopN == 0 {
		opt.TopN = 10
	}
	rep := Report{Now: opt.Now, Limit: opt.Limit}

	models := map[string]*ModelUse{}     // by short model
	sessions := map[string]*sessionAgg{} // by transcript path
	projCache := map[string]*ProjectCache{}
	// per-project cache read/creation split by recency, for the drop flag
	type split struct{ recentRead, recentTot, priorRead, priorTot int64 }
	projSplit := map[string]*split{}
	var burnTokens int64

	add := func(path, agent, model, project string, sub bool, evs []event, title string) {
		sm := parse.ShortModel(model)
		s := sessions[path]
		if s == nil {
			s = &sessionAgg{agent: agent, project: project, isSubagent: sub}
			sessions[path] = s
		}
		if title != "" && s.title == "" {
			s.title = title
		}
		if sm != "-" {
			s.model = sm
		}
		for _, e := range evs {
			age := opt.Now - e.ts
			if age < 0 {
				age = 0
			}
			em := parse.ShortModel(e.model)
			if em == "-" {
				em = sm
			}
			if age <= win7d {
				mu := models[em]
				if mu == nil {
					mu = &ModelUse{Model: em}
					models[em] = mu
				}
				mu.In7d += e.in
				mu.Out7d += e.out
				rep.Total7d += e.in + e.out
				if age <= win5h {
					mu.In5h += e.in
					mu.Out5h += e.out
					rep.Total5h += e.in + e.out
					s.in5h += e.in
					s.out5h += e.out
				}
				if age <= burnWin {
					burnTokens += e.in + e.out
				}
				// cache accounting (claude only populates the split)
				if e.cacheRead != 0 || e.cacheCreation != 0 {
					pc := projCache[project]
					if pc == nil {
						pc = &ProjectCache{Project: project}
						projCache[project] = pc
					}
					pc.CacheRead += e.cacheRead
					pc.CacheCreation += e.cacheCreation
					sp := projSplit[project]
					if sp == nil {
						sp = &split{}
						projSplit[project] = sp
					}
					tot := e.cacheRead + e.cacheCreation
					if age <= dayWin {
						sp.recentRead += e.cacheRead
						sp.recentTot += tot
					} else {
						sp.priorRead += e.cacheRead
						sp.priorTot += tot
					}
				}
			}
		}
	}

	scanClaude(opt.Home, add)
	scanCodex(opt.Home, add)

	rep.BurnPerMin = float64(burnTokens) / float64(burnWin/60)
	if opt.Limit > 0 && burnTokens > 0 {
		remaining := opt.Limit - rep.Total5h
		if remaining < 0 {
			remaining = 0
		}
		rep.ProjFillSecs = int64(float64(remaining) / (rep.BurnPerMin / 60))
	}

	for _, mu := range models {
		rep.Models = append(rep.Models, *mu)
	}
	sort.Slice(rep.Models, func(i, j int) bool {
		return rep.Models[i].In7d+rep.Models[i].Out7d > rep.Models[j].In7d+rep.Models[j].Out7d
	})

	// attribution: top sessions by 5h spend, share of the 5h total
	for _, s := range sessions {
		if s.in5h+s.out5h == 0 {
			continue
		}
		share := 0.0
		if rep.Total5h > 0 {
			share = 100 * float64(s.in5h+s.out5h) / float64(rep.Total5h)
		}
		title := s.title
		if title == "" {
			title = "(untitled)"
		}
		rep.Sessions = append(rep.Sessions, SessionUse{
			Title: title, Agent: s.agent, Model: s.model,
			In: s.in5h, Out: s.out5h, SharePct: share, IsSubagent: s.isSubagent,
		})
	}
	sort.Slice(rep.Sessions, func(i, j int) bool {
		return rep.Sessions[i].In+rep.Sessions[i].Out > rep.Sessions[j].In+rep.Sessions[j].Out
	})
	if len(rep.Sessions) > opt.TopN {
		rep.Sessions = rep.Sessions[:opt.TopN]
	}

	for proj, pc := range projCache {
		if tot := pc.CacheRead + pc.CacheCreation; tot > 0 {
			pc.HitRatio = float64(pc.CacheRead) / float64(tot)
		}
		if sp := projSplit[proj]; sp != nil {
			if sp.recentTot > 0 {
				pc.RecentRatio = float64(sp.recentRead) / float64(sp.recentTot)
			}
			if sp.priorTot > 0 {
				pc.PriorRatio = float64(sp.priorRead) / float64(sp.priorTot)
			}
			// flag a sharp drop: enough recent volume and a >20pt fall vs prior
			if sp.recentTot > 10000 && sp.priorTot > 10000 && pc.PriorRatio-pc.RecentRatio > 0.20 {
				pc.Dropped = true
			}
		}
		rep.Projects = append(rep.Projects, *pc)
	}
	sort.Slice(rep.Projects, func(i, j int) bool {
		return rep.Projects[i].CacheRead+rep.Projects[i].CacheCreation >
			rep.Projects[j].CacheRead+rep.Projects[j].CacheCreation
	})
	return rep
}

func repoOf(cwd string, cache map[string]string) string {
	if cwd == "" {
		return ""
	}
	if r, ok := cache[cwd]; ok {
		return r
	}
	r := paths.RepoRoot(cwd)
	if r == "" {
		r = cwd
	}
	cache[cwd] = r
	return r
}

// scanLines streams a JSONL file, reassembling oversized lines. Fail-soft:
// unreadable files are skipped.
func scanLines(path string, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<20)
	var overflow []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			overflow = append(overflow, chunk...)
			continue
		}
		line := chunk
		if len(overflow) > 0 {
			overflow = append(overflow, chunk...)
			line = overflow
		}
		if ln := bytes.TrimSpace(line); len(ln) > 0 {
			fn(ln)
		}
		overflow = overflow[:0]
		if err != nil {
			return
		}
	}
}

func iso(ts string) int64 {
	if len(ts) < 19 {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05", ts[:19])
	if err != nil {
		return 0
	}
	return t.Unix()
}

func walkJSONL(root string, fn func(path string)) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		fn(path)
		return nil
	})
}
