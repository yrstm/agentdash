//go:build hermes

package board

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/hermesdb"
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
	"github.com/yrstm/agentdash/internal/procs"
)

func init() {
	RegisterExternalKind("hermes")
	RegisterExternalPair(hermesPairing)
	RegisterExternalResume(hermesResumeCmd)
}

// hermesPairing pairs a live Hermes process to a session in its read-only
// state.db, by exact session id (HERMES_SESSION_ID) or cwd/start heuristic.
func hermesPairing(p procs.Proc, h string, cache *parse.Cache, newPidMap map[string]parse.PidInfo, repos map[string]string, row *Row) (procs.Pairing, bool) {
	profile := p.Extra["HERMES_PROFILE"]
	homeDir := hermesdb.ResolveHome(h, p.Extra["HERMES_HOME"], profile)
	dbPath := hermesdb.StateDB(homeDir)
	sess, ok := hermesdb.Find(dbPath, hermesdb.Query{SessionID: p.Extra["HERMES_SESSION_ID"], Cwd: p.Cwd, Start: p.Start})
	if !ok {
		return procs.Pairing{}, false
	}
	key := hermesdb.Key(dbPath, sess.ID)
	cache.Entries[key] = &sess.Entry
	newPidMap[strconv.Itoa(p.PID)] = parse.PidInfo{
		Path: key, Start: float64(p.Start), Sure: sess.Sure,
		Cwd: sess.Entry.Cwd, How: sess.How, Kind: p.Kind, Profile: profile}
	if sess.Entry.Cwd != "" {
		row.Cwd = sess.Entry.Cwd
		if repo, ok := repos[row.Cwd]; ok {
			row.Repo = repo
		} else {
			row.Repo = paths.RepoRoot(row.Cwd)
			repos[row.Cwd] = row.Repo
		}
	}
	return procs.Pairing{Path: key, Sure: sess.Sure, How: sess.How}, true
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
