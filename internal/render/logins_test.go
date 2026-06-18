package render

import (
	"strings"
	"testing"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/procs"
)

func TestLoginSection(t *testing.T) {
	b := &board.Board{Logins: []procs.Login{
		{User: "dev", TTY: "pts/7", From: "10.0.0.1", Idle: "12h",
			What: "tmux new-session -s apidashboard", Tmux: "apidashboard"},
		{User: "dev", TTY: "pts/6", From: "10.0.0.1", Idle: "5h", Stale: true},
		{User: "dev", TTY: "pts/0", From: "", Idle: "1d", What: "-bash"},
	}}
	out := Table(b, NewTheme(true), Opts{Expand: true, Width: 120}) // plain theme: no SGR

	for _, want := range []string{
		"LOGIN SESSIONS",
		"tmux·apidashboard", // names the tmux work, not a bare "tmux"
		"(stale)",           // dropped login is labeled, not a cryptic "."
		"10.0.0.1",          // FROM column
		"local",             // empty host renders as "local", never blank
		"shell",             // -bash stays friendly
	} {
		if !strings.Contains(out, want) {
			t.Errorf("login section missing %q\n%s", want, out)
		}
	}
	// the old bug rendered an empty WHAT as filepath.Base("")=="." — never again
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "dev") && strings.HasSuffix(ln, " .") {
			t.Errorf("login row still renders a bare %q: %q", ".", ln)
		}
	}
}
