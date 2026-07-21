//go:build hermes

package history

import (
	"os"
	"path/filepath"

	"github.com/yrstm/agentdash/internal/hermesdb"
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
)

var extraHistoryReads = `  ~/.hermes/state.db
  ~/.hermes/profiles/*/state.db
`

func init() {
	RegisterSource(hermesSessions)
	RegisterResume(hermesResume)
}

func hermesSessions(home string, livePaths map[string]bool, repos map[string]string) ([]Session, []string) {
	var sessions []Session
	var roots []string
	for _, dbPath := range hermesDBs(home) {
		roots = append(roots, dbPath)
		for _, hs := range hermesdb.List(dbPath) {
			key := hermesdb.Key(dbPath, hs.ID)
			s := Session{Agent: "hermes", Path: key, SessionID: hs.ID,
				Cwd: hs.Entry.Cwd, Title: parse.TitleOf(&hs.Entry, key, nil),
				Start: int64(hs.Entry.Seen), Last: int64(hs.Entry.Mtime),
				Messages: 0, Tokens: parse.Hum(hs.Entry.In) + "/" + parse.Hum(hs.Entry.Out),
				Ctx: parse.Hum(hs.Entry.Ctx), CtxTok: hs.Entry.Ctx, Live: livePaths[key],
				Model: parse.ShortModel(hs.Entry.Model)}
			s.Cwd = normalizePath(s.Cwd)
			if repo, ok := repos[s.Cwd]; ok {
				s.Repo = repo
			} else {
				s.Repo = paths.RepoRoot(s.Cwd)
				repos[s.Cwd] = s.Repo
			}
			s.Duration = s.Last - s.Start
			if s.Duration < 0 {
				s.Duration = 0
			}
			s.Resume = resumeCmd(s)
			sessions = append(sessions, s)
		}
	}
	return sessions, roots
}

func hermesResume(s Session) (string, bool) {
	if s.Agent != "hermes" {
		return "", false
	}
	cd := ""
	if s.Cwd != "" {
		cd = "cd " + s.Cwd + " && "
	}
	_, id, ok := hermesdb.SplitKey(s.Path)
	if !ok {
		id = s.SessionID
	}
	return cd + "hermes --resume " + id, true
}

func hermesDBs(home string) []string {
	var out []string
	if p := filepath.Join(home, ".hermes", "state.db"); fileExists(p) {
		out = append(out, p)
	}
	profiles, _ := filepath.Glob(filepath.Join(home, ".hermes", "profiles", "*", "state.db"))
	for _, p := range profiles {
		if fileExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
