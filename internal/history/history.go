// Package history samples past agent conversations from the transcript files
// the agent CLIs already write. It is read-only and runs on demand.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
)

// extraSessions and extraResumers are extension points for optional,
// build-tagged agent adapters; both are empty in the default build.
var (
	extraSessions []func(home string, livePaths map[string]bool, repos map[string]string) ([]Session, []string)
	extraResumers []func(Session) (string, bool)
)

// RegisterSource adds an optional session source (e.g. a DB-backed agent).
func RegisterSource(f func(home string, livePaths map[string]bool, repos map[string]string) ([]Session, []string)) {
	extraSessions = append(extraSessions, f)
}

// RegisterResume adds a resume-command builder for an optional agent kind.
func RegisterResume(f func(Session) (string, bool)) { extraResumers = append(extraResumers, f) }

// Disclosure is the user-facing accounting of side effects for this tab.
// extraHistoryReads is empty by default and lists any extra stores a
// build-tagged adapter reads.
var Disclosure = `History reads:
  ~/.claude/projects/**/*.jsonl
  ~/.codex/sessions/**/*.jsonl
` + extraHistoryReads + `
History shells out to:
  (none)

It opens transcript files read-only, parses them on demand, and keeps no
daemon, watcher, socket, database, cron job, service, helper script, or
process alive after AgentDash exits.`

// Session is one Claude or Codex conversation as rendered in the History tab.
type Session struct {
	Agent      string `json:"agent"`
	Path       string `json:"path"`
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	Repo       string `json:"repo,omitempty"`
	Title      string `json:"title"`
	Start      int64  `json:"start"`
	Last       int64  `json:"last"`
	Duration   int64  `json:"duration_secs"`
	Messages   int    `json:"messages"`
	Tokens     string `json:"tokens,omitempty"`
	Ctx        string `json:"ctx,omitempty"`
	CtxTok     int64  `json:"ctx_tokens,omitempty"`
	Live       bool   `json:"live"`
	Model      string `json:"model,omitempty"`
	GitBranch  string `json:"git_branch,omitempty"`
	Resume     string `json:"resume"`
	SkipReason string `json:"-"`
}

// Result is a snapshot of the session stores.
type Result struct {
	Sessions []Session
	Skipped  []Session
	Roots    []string
}

type parser struct {
	kind string
	root string
	fn   func(string) (Session, string)
}

// Collect samples both known session stores. livePaths keys are full transcript
// paths currently paired to live agent processes.
func Collect(home string, livePaths map[string]bool) Result {
	parsers := []parser{
		{kind: "claude", root: filepath.Join(home, ".claude", "projects"), fn: parseClaude},
		{kind: "codex", root: filepath.Join(home, ".codex", "sessions"), fn: parseCodex},
	}
	var res Result
	repos := map[string]string{}
	for _, p := range parsers {
		res.Roots = append(res.Roots, p.root)
		_ = filepath.WalkDir(p.root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if p.kind == "claude" && isClaudeSubagent(path) {
				return nil
			}
			s, why := p.fn(path)
			if why != "" {
				res.Skipped = append(res.Skipped, Session{Agent: p.kind, Path: path, SkipReason: why})
				return nil
			}
			s.Live = livePaths[path]
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
			res.Sessions = append(res.Sessions, s)
			return nil
		})
	}
	for _, src := range extraSessions {
		sessions, roots := src(home, livePaths, repos)
		res.Sessions = append(res.Sessions, sessions...)
		res.Roots = append(res.Roots, roots...)
	}
	sort.SliceStable(res.Sessions, func(i, j int) bool {
		return res.Sessions[i].Last > res.Sessions[j].Last
	})
	return res
}

func isClaudeSubagent(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "subagents" ||
		strings.HasPrefix(filepath.Base(path), "agent-")
}

func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func resumeCmd(s Session) string {
	cd := ""
	if s.Cwd != "" {
		cd = "cd " + s.Cwd + " && "
	}
	if s.Agent == "codex" {
		id := s.SessionID
		if id == "" {
			id = codexIDFromPath(s.Path)
		}
		return cd + "codex resume " + id
	}
	for _, f := range extraResumers {
		if r, ok := f(s); ok {
			return r
		}
	}
	id := s.SessionID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
	}
	return cd + "claude --resume " + id
}

func parseClaude(path string) (Session, string) {
	s := Session{Agent: "claude", Path: path, SessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	valid := 0
	var inTok, outTok int64
	lastMid := ""
	if err := scanJSONL(path, func(line []byte) {
		var obj struct {
			Type          string          `json:"type"`
			Timestamp     string          `json:"timestamp"`
			SessionID     string          `json:"sessionId"`
			Cwd           string          `json:"cwd"`
			GitBranch     string          `json:"gitBranch"`
			Summary       string          `json:"summary"`
			IsMeta        bool            `json:"isMeta"`
			ToolUseResult json.RawMessage `json:"toolUseResult"`
			AITitle       string          `json:"aiTitle"`
			LastPrompt    string          `json:"lastPrompt"`
			Message       struct {
				ID      string          `json:"id"`
				Model   string          `json:"model"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Usage   tokenUsage      `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &obj) != nil {
			return
		}
		valid++
		if obj.SessionID != "" {
			s.SessionID = obj.SessionID
		}
		if obj.Cwd != "" && s.Cwd == "" {
			s.Cwd = obj.Cwd
		}
		if obj.GitBranch != "" && s.GitBranch == "" {
			s.GitBranch = obj.GitBranch
		}
		if ts := iso(obj.Timestamp); ts != 0 {
			if s.Start == 0 || ts < s.Start {
				s.Start = ts
			}
			if ts > s.Last {
				s.Last = ts
			}
		}
		if obj.Message.Model != "" {
			s.Model = obj.Message.Model
		}
		if obj.Message.Usage.present && (obj.Message.ID == "" || obj.Message.ID != lastMid) {
			lastMid = obj.Message.ID
			in := obj.Message.Usage.InputTokens + obj.Message.Usage.CacheCreationInputTokens + obj.Message.Usage.CacheReadInputTokens
			inTok += in
			outTok += obj.Message.Usage.OutputTokens
			if in != 0 {
				s.CtxTok = in
			}
		}
		switch obj.Type {
		case "summary":
			if s.Title == "" {
				if title := clean(obj.Summary, 120); usableTitle(title) {
					s.Title = title
				}
			}
		case "user":
			if obj.IsMeta || len(obj.ToolUseResult) > 0 {
				return
			}
			s.Messages++
			if s.Title == "" {
				if title := clean(contentText(obj.Message.Content), 120); usableTitle(title) {
					s.Title = title
				}
			}
		case "assistant":
			s.Messages++
		case "ai-title":
			if s.Title == "" {
				if title := clean(obj.AITitle, 120); usableTitle(title) {
					s.Title = title
				}
			}
		case "last-prompt":
			if s.Title == "" {
				if title := clean(obj.LastPrompt, 120); usableTitle(title) {
					s.Title = title
				}
			}
		}
	}); err != nil {
		return s, err.Error()
	}
	if valid == 0 {
		return s, "no valid JSON records"
	}
	fillTimesFromStat(&s)
	if s.Cwd == "" {
		s.Cwd = cwdFromClaudePath(path)
	}
	if s.Title == "" {
		s.Title = "(untitled)"
	}
	setTokenStrings(&s, inTok, outTok)
	if s.Start == 0 && s.Last == 0 {
		return s, "no timestamped records"
	}
	return s, ""
}

func parseCodex(path string) (Session, string) {
	s := Session{Agent: "codex", Path: path, SessionID: codexIDFromPath(path)}
	valid := 0
	var inTok, outTok int64
	if err := scanJSONL(path, func(line []byte) {
		var obj struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Payload   struct {
				ID         string          `json:"id"`
				Timestamp  string          `json:"timestamp"`
				Cwd        string          `json:"cwd"`
				Type       string          `json:"type"`
				Message    string          `json:"message"`
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				Model      string          `json:"model"`
				CLIVersion string          `json:"cli_version"`
				Info       struct {
					TotalTokenUsage struct {
						InputTokens       int64 `json:"input_tokens"`
						CachedInputTokens int64 `json:"cached_input_tokens"`
						OutputTokens      int64 `json:"output_tokens"`
					} `json:"total_token_usage"`
					LastTokenUsage struct {
						InputTokens       int64 `json:"input_tokens"`
						CachedInputTokens int64 `json:"cached_input_tokens"`
					} `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &obj) != nil {
			return
		}
		valid++
		ts := iso(obj.Timestamp)
		if ts == 0 {
			ts = iso(obj.Payload.Timestamp)
		}
		if ts != 0 {
			if s.Start == 0 || ts < s.Start {
				s.Start = ts
			}
			if ts > s.Last {
				s.Last = ts
			}
		}
		switch obj.Type {
		case "session_meta":
			if obj.Payload.ID != "" {
				s.SessionID = obj.Payload.ID
			}
			if obj.Payload.Cwd != "" {
				s.Cwd = obj.Payload.Cwd
			}
		case "turn_context":
			if obj.Payload.Model != "" {
				s.Model = obj.Payload.Model
			}
		case "event_msg":
			switch obj.Payload.Type {
			case "user_message":
				s.Messages++
				if s.Title == "" {
					if title := clean(obj.Payload.Message, 120); usableTitle(title) {
						s.Title = title
					}
				}
			case "agent_message":
				s.Messages++
			case "token_count":
				tot := obj.Payload.Info.TotalTokenUsage
				inTok = tot.InputTokens + tot.CachedInputTokens
				outTok = tot.OutputTokens
				last := obj.Payload.Info.LastTokenUsage
				s.CtxTok = last.InputTokens + last.CachedInputTokens
			}
		case "response_item":
			if obj.Payload.Type == "message" {
				if obj.Payload.Role == "user" || obj.Payload.Role == "assistant" {
					s.Messages++
				}
				if obj.Payload.Role == "user" && s.Title == "" {
					if title := clean(codexContentText(obj.Payload.Content), 120); usableTitle(title) {
						s.Title = title
					}
				}
			}
		}
	}); err != nil {
		return s, err.Error()
	}
	if valid == 0 {
		return s, "no valid JSON records"
	}
	fillTimesFromStat(&s)
	if s.Title == "" {
		s.Title = "(untitled)"
	}
	setTokenStrings(&s, inTok, outTok)
	if s.Start == 0 && s.Last == 0 {
		return s, "no timestamped records"
	}
	return s, ""
}

type tokenUsage struct {
	present                  bool
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

func (u *tokenUsage) UnmarshalJSON(b []byte) error {
	u.present = bytes.ContainsRune(b, ':')
	type alias tokenUsage
	var a alias
	if json.Unmarshal(b, &a) == nil {
		*u = tokenUsage(a)
		u.present = bytes.ContainsRune(b, ':')
	}
	return nil
}

func setTokenStrings(s *Session, inTok, outTok int64) {
	if inTok != 0 || outTok != 0 {
		s.Tokens = parse.Hum(inTok) + "/" + parse.Hum(outTok)
	}
	if s.CtxTok != 0 {
		s.Ctx = parse.Hum(s.CtxTok)
	}
}

func scanJSONL(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
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
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
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
	}
	return nil
}

func fillTimesFromStat(s *Session) {
	st, err := os.Stat(s.Path)
	if err != nil {
		return
	}
	mt := st.ModTime().Unix()
	if s.Last == 0 {
		s.Last = mt
	}
	if s.Start == 0 {
		s.Start = mt
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

func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	for _, r := range parts {
		var p struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(r, &p) == nil && (p.Type == "text" || p.Type == "input_text") {
			return p.Text
		}
	}
	return ""
}

func codexContentText(raw json.RawMessage) string { return contentText(raw) }

func clean(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > width {
		return string(r[:width-1]) + "..."
	}
	return s
}

func usableTitle(s string) bool {
	if s == "" {
		return false
	}
	l := strings.ToLower(s)
	for _, p := range []string{
		"<environment_context>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<skills_instructions>",
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<command-name>",
		"<command-message>",
		"/clear",
		"/resume",
		"/run",
		"clear",
		"resume",
		"resume previous coding session",
		"claude --resume",
		"you were working on a task before",
	} {
		if strings.HasPrefix(l, p) {
			return false
		}
	}
	return true
}

func cwdFromClaudePath(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	if dir == "" {
		return ""
	}
	return "/" + strings.TrimLeft(strings.ReplaceAll(dir, "-", "/"), "/")
}

var codexIDRe = regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-(.+)$`)

func codexIDFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if m := codexIDRe.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	return base
}
