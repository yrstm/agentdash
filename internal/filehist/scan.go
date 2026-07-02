package filehist

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// edit is one Edit/Write of the target file recorded in a transcript.
type edit struct {
	ts      int64
	agent   string
	session string
	task    string
}

var claudeEditOps = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// scanEdits walks both agents' transcripts and returns every Edit/Write of the
// target file, each tagged with the session's task (its first user message).
func scanEdits(home, target string) []edit {
	want := resolve(target)
	var out []edit
	for _, r := range []struct{ kind, dir string }{
		{"claude", filepath.Join(home, ".claude", "projects")},
		{"codex", filepath.Join(home, ".codex", "sessions")},
	} {
		_ = filepath.WalkDir(r.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			out = append(out, editsInFile(path, r.kind, want)...)
			return nil
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ts < out[j].ts })
	return out
}

// editsInFile scans one transcript: it captures the session id and first user
// message (the task) and collects the timestamps of edits touching want.
func editsInFile(path, agent, want string) []edit {
	var (
		task, session string
		stamps        []int64
	)
	scanLines(path, func(line []byte) {
		if agent == "claude" {
			claudeLine(line, want, &task, &session, &stamps)
		} else {
			codexLine(line, want, &task, &session, &stamps)
		}
	})
	if session == "" {
		session = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	out := make([]edit, 0, len(stamps))
	for _, ts := range stamps {
		out = append(out, edit{ts: ts, agent: agent, session: session, task: task})
	}
	return out
}

func claudeLine(line []byte, want string, task, session *string, stamps *[]int64) {
	var obj struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		SessionID string `json:"sessionId"`
		IsMeta    bool   `json:"isMeta"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &obj) != nil {
		return
	}
	if obj.SessionID != "" {
		*session = obj.SessionID
	}
	switch obj.Type {
	case "user":
		if *task == "" && !obj.IsMeta {
			if t := firstText(obj.Message.Content); t != "" {
				*task = clip(t, 60)
			}
		}
	case "assistant":
		if len(obj.Message.Content) == 0 || obj.Message.Content[0] != '[' {
			return
		}
		var parts []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(obj.Message.Content, &parts) != nil {
			return
		}
		for _, p := range parts {
			if p.Type == "tool_use" && claudeEditOps[p.Name] {
				if fp := strField(p.Input, "file_path"); fp != "" && resolve(fp) == want {
					*stamps = append(*stamps, iso(obj.Timestamp))
				}
			}
		}
	}
}

func codexLine(line []byte, want string, task, session *string, stamps *[]int64) {
	var obj struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Timestamp string `json:"timestamp"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Message   string `json:"message"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &obj) != nil {
		return
	}
	ts := iso(obj.Timestamp)
	if ts == 0 {
		ts = iso(obj.Payload.Timestamp)
	}
	switch obj.Type {
	case "session_meta":
		if obj.Payload.ID != "" {
			*session = obj.Payload.ID
		}
	case "event_msg":
		if obj.Payload.Type == "user_message" && *task == "" && obj.Payload.Message != "" {
			*task = clip(obj.Payload.Message, 60)
		}
	case "response_item":
		if obj.Payload.Type == "function_call" && strings.Contains(obj.Payload.Name, "apply_patch") {
			patch := strField(json.RawMessage(obj.Payload.Arguments), "input")
			if patch == "" {
				patch = strField(json.RawMessage(obj.Payload.Arguments), "patch")
			}
			for _, ln := range strings.Split(patch, "\n") {
				for _, pfx := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
					if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), pfx); ok && resolve(strings.TrimSpace(rest)) == want {
						*stamps = append(*stamps, ts)
					}
				}
			}
		}
	}
}

// JSON renders a Log as the schema_version 2 docs file-history document.
func JSON(lg Log) ([]byte, error) {
	if lg.Changes == nil {
		lg.Changes = []Change{}
	}
	doc := struct {
		SchemaVersion int `json:"schema_version"`
		Log
	}{SchemaVersion, lg}
	return json.MarshalIndent(doc, "", "  ")
}

// --- small shared helpers ---------------------------------------------------

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

func firstText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
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

func strField(raw json.RawMessage, field string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[field], &s) == nil {
		return s
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
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

func isoStr(epoch int64) string {
	if epoch == 0 {
		return "unknown time"
	}
	return time.Unix(epoch, 0).UTC().Format("2006-01-02 15:04")
}
