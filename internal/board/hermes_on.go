//go:build hermes

package board

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/hermesdb"
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/procs"
)

func init() {
	RegisterExternalKind("hermes")
	RegisterExternalBatch(hermesBatch)
	RegisterExternalResume(hermesResumeCmd)
}

// hermesBatch pairs every live Hermes process to a distinct session in its
// read-only state.db. Without it, several processes that don't export
// HERMES_SESSION_ID would each independently resolve to the newest active
// session and show as duplicate rows. Processes with an exact session id are
// resolved first (so a heuristic can't claim a session another process
// definitively owns); the rest claim the best still-unclaimed session in PID
// order, and a process with no distinct session left is simply left unpaired —
// honest over duplicated.
func hermesBatch(agents []procs.Proc, h string, cache *parse.Cache, newPidMap map[string]parse.PidInfo) map[int]procs.Pairing {
	var hermes []procs.Proc
	for _, p := range agents {
		if p.Kind == "hermes" {
			hermes = append(hermes, p)
		}
	}
	sort.SliceStable(hermes, func(i, j int) bool {
		ei := hermes[i].Extra["HERMES_SESSION_ID"] != ""
		ej := hermes[j].Extra["HERMES_SESSION_ID"] != ""
		if ei != ej {
			return ei // exact session-id processes first
		}
		return hermes[i].PID < hermes[j].PID
	})

	res := map[int]procs.Pairing{}
	claimed := map[string]map[string]bool{} // dbPath -> claimed session ids
	for _, p := range hermes {
		profile := p.Extra["HERMES_PROFILE"]
		dbPath := hermesdb.StateDB(hermesdb.ResolveHome(h, p.Extra["HERMES_HOME"], profile))
		sess, ok := hermesdb.Find(dbPath, hermesdb.Query{
			SessionID: p.Extra["HERMES_SESSION_ID"], Cwd: p.Cwd, Start: p.Start,
			Exclude: claimed[dbPath]})
		if !ok {
			continue // no distinct session left: leave unpaired, don't duplicate
		}
		if claimed[dbPath] == nil {
			claimed[dbPath] = map[string]bool{}
		}
		claimed[dbPath][sess.ID] = true
		key := hermesdb.Key(dbPath, sess.ID)
		cache.Entries[key] = &sess.Entry
		newPidMap[strconv.Itoa(p.PID)] = parse.PidInfo{
			Path: key, Start: float64(p.Start), Sure: sess.Sure,
			Cwd: sess.Entry.Cwd, How: sess.How, Kind: p.Kind, Profile: profile}
		res[p.PID] = procs.Pairing{Path: key, Sure: sess.Sure, How: sess.How}
	}
	return res
}

func hermesResumeCmd(m parse.PidInfo) (string, bool) {
	if m.Kind != "hermes" {
		return "", false
	}
	cd := ""
	if m.Cwd != "" {
		cd = "cd " + m.Cwd + " && "
	}
	_, id, ok := hermesdb.SplitKey(m.Path)
	if !ok {
		id = strings.TrimSuffix(filepath.Base(m.Path), ".jsonl")
	}
	prof := ""
	if m.Profile != "" && m.Profile != "default" {
		prof = " -p " + m.Profile
	}
	return cd + "hermes" + prof + " --resume " + id, true
}
