package procs

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/parse"
)

// TSSlack is the tolerance for timestamp evidence: a session's first
// entry within this many seconds of process start counts as a match.
const TSSlack = 300

// Pairing is a pid-to-session-file decision plus its evidence tier.
type Pairing struct {
	Path string
	Sure bool
	How  string
}

// ProjDir encodes a cwd to its ~/.claude/projects directory.
func ProjDir(home, cwd string) string {
	enc := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
	return filepath.Join(home, ".claude", "projects", enc)
}

func mtimeOf(p string) float64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return float64(st.ModTime().UnixNano()) / 1e9
}

// claudePathsFor lists the project dir's sessions, newest first.
func claudePathsFor(home, cwd string) []string {
	m, err := filepath.Glob(filepath.Join(ProjDir(home, cwd), "*.jsonl"))
	if err != nil {
		return nil
	}
	sort.Slice(m, func(i, j int) bool { return mtimeOf(m[i]) > mtimeOf(m[j]) })
	return m
}

// fdSession is evidence tier 1: the process holds a session jsonl open
// under its project dir. Exact when it hits, but claude does not normally
// keep the file open. openFDs is the per-OS fd primitive (/proc on Linux,
// lsof on macOS).
func fdSession(pid int, proj string) string {
	for _, t := range openFDs(pid) {
		if strings.HasPrefix(t, proj+string(os.PathSeparator)) && strings.HasSuffix(t, ".jsonl") {
			return t
		}
	}
	return ""
}

// PairClaude pairs every claude pid with a session file, walking the
// evidence chain per cwd group (several processes can share one project
// dir, so this is a batch pass):
//
//  1. fd      : a jsonl open under /proc/<pid>/fd (exact)
//  2. cwd     : the project dir holds exactly one live candidate (exact)
//  3. start-ts: first entry timestamp ~ process start, ±5min (confident;
//     re-derived each draw, twin ties go to the freshest file)
//  4. sticky  : last draw's guess (heuristic, marked ?)
//  5. recency : newest unclaimed file written since proc start (marked ?)
//
// prevPidMap is last draw's _pidmap; newPidMap collects this draw's.
func PairClaude(agents []Proc, home string, prevPidMap map[string]parse.PidInfo,
	newPidMap map[string]parse.PidInfo) map[int]Pairing {

	byCwd := map[string][]Proc{}
	for _, p := range agents {
		if p.Kind == "claude" {
			byCwd[p.Cwd] = append(byCwd[p.Cwd], p)
		}
	}

	res := map[int]Pairing{}
	firstTS := map[string]int64{}
	fts := func(p string) int64 {
		if v, ok := firstTS[p]; ok {
			return v
		}
		v := parse.FirstTS(p)
		firstTS[p] = v
		return v
	}

	for cwd, procs := range byCwd {
		paths := claudePathsFor(home, cwd)
		proj := ProjDir(home, cwd)
		claimed := map[string]bool{}

		assign := func(pid int, path string, start int64, sure bool, how string) {
			res[pid] = Pairing{path, sure, how}
			claimed[path] = true
			newPidMap[strconv.Itoa(pid)] = parse.PidInfo{
				Path: path, Start: float64(start), Sure: sure, Cwd: cwd, How: how}
		}
		liveCandidates := func(start int64) []string {
			// a live session's file must have been written since its process began
			var out []string
			for _, p := range paths {
				if !claimed[p] && mtimeOf(p) >= float64(start-60) {
					out = append(out, p)
				}
			}
			return out
		}

		sort.Slice(procs, func(i, j int) bool { return procs[i].Uptime < procs[j].Uptime })
		var pending []Proc
		for _, pr := range procs { // newest process first
			if p := fdSession(pr.PID, proj); p != "" && !claimed[p] {
				assign(pr.PID, p, pr.Start, true, "fd")
			} else {
				pending = append(pending, pr)
			}
		}
		var rest []Proc
		for _, pr := range pending {
			if c := liveCandidates(pr.Start); len(c) == 1 {
				assign(pr.PID, c[0], pr.Start, true, "cwd")
			} else {
				rest = append(rest, pr)
			}
		}
		var rest2 []Proc
		for _, pr := range rest {
			best, bestDiff, bestMtime := "", int64(math.MaxInt64), float64(0)
			for _, p := range paths {
				t := fts(p)
				if claimed[p] || t == 0 {
					continue
				}
				diff := t - pr.Start
				if diff < 0 {
					diff = -diff
				}
				if diff > TSSlack {
					continue
				}
				if m := mtimeOf(p); diff < bestDiff || (diff == bestDiff && m > bestMtime) {
					best, bestDiff, bestMtime = p, diff, m
				}
			}
			if best != "" {
				assign(pr.PID, best, pr.Start, true, "start-ts")
			} else {
				rest2 = append(rest2, pr)
			}
		}
		var rest3 []Proc
		for _, pr := range rest2 {
			prev, ok := prevPidMap[strconv.Itoa(pr.PID)]
			drift := prev.Start - float64(pr.Start)
			if ok && math.Abs(drift) <= 5 && // not a reused pid
				contains(paths, prev.Path) && !claimed[prev.Path] {
				assign(pr.PID, prev.Path, pr.Start, false, "sticky")
			} else {
				rest3 = append(rest3, pr)
			}
		}
		for _, pr := range rest3 {
			if c := liveCandidates(pr.Start); len(c) > 0 {
				assign(pr.PID, c[0], pr.Start, false, "recency")
			}
		}
	}
	return res
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

var codexTSRe = regexp.MustCompile(`rollout-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2})`)

// CodexRollout is a rollout file whose session_meta records a given cwd,
// carrying the session start parsed from its filename (0 if unparseable).
type CodexRollout struct {
	Path string
	TS   int64
}

// CodexRollouts returns the rollouts under ~/.codex/sessions whose session_meta
// records this cwd, scanning only the newest 40 files. This is the expensive
// part (stat + first-line read); callers cache it per cwd and then do the cheap
// per-process MatchCodex.
func CodexRollouts(home, cwd string) []CodexRollout {
	root := filepath.Join(home, ".codex", "sessions")
	type cand struct {
		mtime float64
		path  string
	}
	var files []cand
	// A missing/unreadable sessions root just yields no candidates; per-entry
	// errors are skipped in the callback, so any error here is the root failing
	// to open.
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		files = append(files, cand{mtimeOf(p), p})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mtime > files[j].mtime })
	if len(files) > 40 { // newest rollouts only, keep the scan fast
		files = files[:40]
	}
	var out []CodexRollout
	for _, f := range files {
		if metaCwd(f.path) != cwd {
			continue
		}
		ts := int64(0)
		if m := codexTSRe.FindStringSubmatch(filepath.Base(f.path)); m != nil {
			if t, err := time.Parse("2006-01-02T15-04-05", m[1]); err == nil {
				ts = t.Unix()
			}
		}
		out = append(out, CodexRollout{Path: f.path, TS: ts})
	}
	return out
}

// CodexFDSession is exact evidence: the process holds a codex rollout jsonl
// open under ~/.codex/sessions. This is how *resumed* sessions pair — a
// `codex resume` rollout keeps its original creation timestamp in the filename,
// far from the resume process's start, so MatchCodex's start-time window misses
// it; the held fd does not. Mirrors fdSession but matches any rollout, since a
// resumed session's cwd need not equal the rollout's recorded cwd.
func CodexFDSession(pid int) string {
	for _, t := range openFDs(pid) {
		if strings.Contains(t, "/.codex/sessions/") &&
			strings.HasPrefix(filepath.Base(t), "rollout-") &&
			strings.HasSuffix(t, ".jsonl") {
			return t
		}
	}
	return ""
}

// MatchCodex pairs a process to the cwd's rollout whose filename timestamp is
// closest to the process start, within TSSlack. A same-cwd match WITHOUT a
// timestamp match is rejected: an old process in a busy cwd (e.g. ~, shared by
// many sessions) must not inherit the newest unrelated rollout's task, tokens
// or status, and must not feed respawn detection. Returns ("", false) when no
// rollout's start lines up with this process. The bool is the pairing's
// reliability, so it doubles as Pairing.Sure.
func MatchCodex(rollouts []CodexRollout, start int64) (string, bool) {
	best, bestDiff := "", int64(math.MaxInt64)
	for _, r := range rollouts {
		if r.TS == 0 {
			continue
		}
		d := r.TS - start
		if d < 0 {
			d = -d
		}
		if d <= TSSlack && d < bestDiff {
			best, bestDiff = r.Path, d
		}
	}
	return best, best != ""
}

// LocateCodex pairs a single process to its rollout by cwd plus a start-time
// match. Equivalent to MatchCodex(CodexRollouts(home, cwd), start); the board
// caches the rollout scan per cwd and calls MatchCodex per process.
func LocateCodex(home, cwd string, start int64) (string, bool) {
	return MatchCodex(CodexRollouts(home, cwd), start)
}

// PairCodex pairs a batch of codex processes with rollouts, claiming each
// rollout at most once — the same rule PairClaude applies to session files.
// Resolved per process, two `codex resume`s of one session rendered as
// identical clone rows and the session double-counted in the header tallies.
// Evidence tiers, newer process first within each tier (as PairClaude):
//
//  1. fd   : an open rollout under ~/.codex/sessions (exact). A process whose
//     held rollout is already claimed is a second attach to the same session:
//     it stays unpaired outright — exact evidence names its session, so
//     falling through to a timestamp match would fabricate a different one.
//  2. meta : the cwd rollout with the closest filename timestamp within
//     TSSlack, among rollouts not yet claimed.
//
// Anything left unpaired surfaces through the existing `+N unmatched` footer
// (and -a), never as a duplicate row.
func PairCodex(agents []Proc, home string) map[int]Pairing {
	res := map[int]Pairing{}
	if len(agents) == 0 {
		return res
	}
	procs := append([]Proc(nil), agents...)
	sort.Slice(procs, func(i, j int) bool { return procs[i].Uptime < procs[j].Uptime })

	claimed := map[string]bool{}
	cands := map[string][]CodexRollout{}
	rolloutsFor := func(cwd string) []CodexRollout {
		c, ok := cands[cwd]
		if !ok {
			c = CodexRollouts(home, cwd)
			cands[cwd] = c
		}
		return c
	}

	var pending []Proc
	for _, pr := range procs {
		switch fp := CodexFDSession(pr.PID); {
		case fp != "" && !claimed[fp]:
			res[pr.PID] = Pairing{fp, true, "fd"}
			claimed[fp] = true
		case fp != "":
			// second attach to a claimed session: leave unpaired (see above)
		default:
			pending = append(pending, pr)
		}
	}
	for _, pr := range pending {
		var free []CodexRollout
		for _, r := range rolloutsFor(pr.Cwd) {
			if !claimed[r.Path] {
				free = append(free, r)
			}
		}
		if mp, ok := MatchCodex(free, pr.Start); ok {
			res[pr.PID] = Pairing{mp, true, "meta"}
			claimed[mp] = true
		}
	}
	return res
}

func metaCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // read-only
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if !sc.Scan() {
		return ""
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(sc.Bytes(), &meta) != nil || meta.Type != "session_meta" {
		return ""
	}
	return meta.Payload.Cwd
}
