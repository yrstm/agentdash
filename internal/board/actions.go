package board

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/procs"
)

var codexResumeRe = regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-(.+)$`)

// ResumeCmd builds the paste-ready resume command for a paired session.
func ResumeCmd(m parse.PidInfo) string {
	sid := strings.TrimSuffix(filepath.Base(m.Path), ".jsonl")
	cd := ""
	if m.Cwd != "" {
		cd = "cd " + m.Cwd + " && "
	}
	if externalResume != nil {
		if r, ok := externalResume(m); ok {
			return r
		}
	}
	if m.Kind == "codex" {
		// rollout-<ts>-<id>.jsonl: codex resumes by the trailing id
		id := sid
		if r := codexResumeRe.FindStringSubmatch(sid); r != nil {
			id = r[1]
		}
		return cd + "codex resume " + id
	}
	return cd + "claude --resume " + sid
}

// PidEntry resolves a pid to its pairing and cached entry; the error
// message matches v1.
func PidEntry(cache *parse.Cache, pid int) (parse.PidInfo, *parse.Entry, error) {
	m, ok := cache.PidMap[strconv.Itoa(pid)]
	if !ok {
		return m, nil, fmt.Errorf("agentdash: no session known for pid %d (is it on the board?)", pid)
	}
	ent := cache.Entries[m.Path]
	if ent == nil {
		ent = &parse.Entry{}
	}
	return m, ent, nil
}

// SetLabel pins or clears a task label for the pid's session.
func SetLabel(cache *parse.Cache, pid int, label string, now float64) (string, error) {
	m, _, err := PidEntry(cache, pid)
	if err != nil {
		return "", err
	}
	sid := strings.TrimSuffix(filepath.Base(m.Path), ".jsonl")
	label = strings.TrimSpace(label)
	verb := "set"
	if label == "" {
		delete(cache.Labels, m.Path)
		verb = "cleared"
	} else {
		if cache.Labels == nil {
			cache.Labels = map[string]string{}
		}
		cache.Labels[m.Path] = label
	}
	if err := cache.Save(cachePath(), now); err != nil {
		return "", err
	}
	return fmt.Sprintf("label %s for %s", verb, sid), nil
}

// RecapItem is one changed session since the last look.
type RecapItem struct {
	State  string
	AgeS   int64
	Title  string
	Last   string
	Resume string
}

// Recap lists sessions changed since the given epoch (0 = last recap,
// capped at 7 days) and advances the recap clock.
func Recap(since float64, now float64) []RecapItem {
	cache := parse.LoadCache(cachePath())
	if since == 0 {
		since = cache.RecapTS
	}
	if min := now - 7*86400; since < min {
		since = min
	}
	th := parse.Thresholds{
		WorkingSecs: envInt("AGENTDASH_WORKING_SECS", 60),
		StuckSecs:   envInt("AGENTDASH_STUCK_SECS", 90),
		IdleSecs:    envInt("AGENTDASH_IDLE_SECS", 600),
	}
	live := map[string]bool{}
	for pid, v := range cache.PidMap {
		if procs.Alive(pid) {
			live[v.Path] = true
		}
	}
	var items []RecapItem
	dirs, _ := filepath.Glob(filepath.Join(home(), ".claude", "projects", "*"))
	for _, d := range dirs {
		paths, _ := filepath.Glob(filepath.Join(d, "*.jsonl"))
		for _, p := range paths {
			if strings.HasPrefix(filepath.Base(p), "agent-") { // subagent transcripts
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			mt := float64(st.ModTime().UnixNano()) / 1e9
			if mt <= since {
				continue
			}
			ent := parse.ScanSession(p, cache, "claude", now)
			if ent == nil || (ent.TitleUser == "" && ent.Summary == "") {
				continue
			}
			title := parse.TitleOf(ent, p, cache.Labels)
			last := parse.Clean(ent.LastText, 100)
			state, rcmd := "", ""
			if live[p] {
				state = parse.StatusOf(ent, 0, now, th)
				if state == "waiting" || state == "stuck?" {
					state = "WAITING"
				}
			} else if ent.LastType == "assistant" {
				state = "finished"
			} else {
				state = "died?"
				sid := strings.TrimSuffix(filepath.Base(p), ".jsonl")
				cd := ""
				if ent.Cwd != "" {
					cd = "cd " + ent.Cwd + " && "
				}
				rcmd = cd + "claude --resume " + sid
			}
			items = append(items, RecapItem{state, int64(now - mt), title, last, rcmd})
		}
	}
	order := map[string]int{"WAITING": 0, "died?": 1, "finished": 2, "working": 3, "idle": 4}
	sort.SliceStable(items, func(i, j int) bool {
		oi, ok := order[items[i].State]
		if !ok {
			oi = 5
		}
		oj, ok := order[items[j].State]
		if !ok {
			oj = 5
		}
		if oi != oj {
			return oi < oj
		}
		return items[i].AgeS < items[j].AgeS
	})
	cache.RecapTS = now
	_ = cache.Save(cachePath(), now)
	return items
}

// LoadCacheForActions exposes the cache for the pid-addressed modes.
func LoadCacheForActions() *parse.Cache { return parse.LoadCache(cachePath()) }
