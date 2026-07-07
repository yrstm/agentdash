package trail

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

// ---- commands --------------------------------------------------------------

// Commands returns every shell command an agent ran, newest last.
func Commands(opt Options) []Command {
	var out []Command
	eachTranscript(opt.Home, func(agent, path string) {
		st := state{agent: agent, path: path}
		if agent == "claude" {
			st.cwd = cwdFromClaudePath(path)
		}
		scanLines(path, func(line []byte) {
			if agent == "claude" {
				out = append(out, claudeCommands(line, &st, opt)...)
			} else {
				out = append(out, codexCommands(line, &st, opt)...)
			}
		})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

func claudeCommands(line []byte, st *state, opt Options) []Command {
	obj, parts := decodeClaude(line, st)
	var out []Command
	if obj.Type != "assistant" {
		return nil
	}
	for _, p := range parts {
		if p.Type == "tool_use" && p.Name == "Bash" {
			cmd := jsonField(p.Input, "command")
			if cmd == "" || !keep(opt, obj.ts, st.cwd) {
				continue
			}
			out = append(out, Command{TS: obj.ts, Agent: "claude", Session: sessionName(st.path), Cwd: st.cwd, Command: cmd})
		}
	}
	return out
}

func codexCommands(line []byte, st *state, opt Options) []Command {
	p, ts := decodeCodex(line, st)
	var cmd string
	switch p.Type {
	case "local_shell_call":
		cmd = strings.Join(p.Action.Command, " ")
	case "function_call":
		if strings.Contains(p.Name, "shell") || strings.Contains(p.Name, "exec") {
			if arr := jsonStringArray(p.Arguments, "command"); len(arr) > 0 {
				cmd = strings.Join(arr, " ")
			} else {
				cmd = jsonField(json.RawMessage(p.Arguments), "command")
			}
		}
	}
	if cmd == "" || !keep(opt, ts, st.cwd) {
		return nil
	}
	return []Command{{
		TS: ts, Agent: "codex", Session: sessionName(st.path), Cwd: st.cwd, Command: cmd,
		ApprovalsOff: st.approval == "never",
		SandboxOff:   st.sandbox == "danger-full-access",
	}}
}

// ---- files -----------------------------------------------------------------

// Files returns every Edit/Write an agent performed, newest last.
func Files(opt Options) []FileEdit {
	var out []FileEdit
	eachTranscript(opt.Home, func(agent, path string) {
		st := state{agent: agent, path: path}
		if agent == "claude" {
			st.cwd = cwdFromClaudePath(path)
		}
		scanLines(path, func(line []byte) {
			if agent == "claude" {
				out = append(out, claudeFiles(line, &st, opt)...)
			} else {
				out = append(out, codexFiles(line, &st, opt)...)
			}
		})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

var editOps = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

func claudeFiles(line []byte, st *state, opt Options) []FileEdit {
	obj, parts := decodeClaude(line, st)
	if obj.Type != "assistant" {
		return nil
	}
	var out []FileEdit
	for _, p := range parts {
		if p.Type == "tool_use" && editOps[p.Name] {
			path := jsonField(p.Input, "file_path")
			if path == "" || !keep(opt, obj.ts, st.cwd) {
				continue
			}
			out = append(out, FileEdit{TS: obj.ts, Agent: "claude", Session: sessionName(st.path), Cwd: st.cwd, Op: p.Name, Path: path})
		}
	}
	return out
}

// applyPatchFileRe pulls file paths out of a codex apply_patch body.
func codexFiles(line []byte, st *state, opt Options) []FileEdit {
	p, ts := decodeCodex(line, st)
	if p.Type != "function_call" || !strings.Contains(p.Name, "apply_patch") {
		return nil
	}
	patch := jsonField(json.RawMessage(p.Arguments), "input")
	if patch == "" {
		patch = jsonField(json.RawMessage(p.Arguments), "patch")
	}
	var out []FileEdit
	for _, ln := range strings.Split(patch, "\n") {
		for _, pfx := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), pfx); ok && keep(opt, ts, st.cwd) {
				out = append(out, FileEdit{TS: ts, Agent: "codex", Session: sessionName(st.path), Cwd: st.cwd, Op: "apply_patch", Path: strings.TrimSpace(rest)})
			}
		}
	}
	return out
}

// ---- decode helpers --------------------------------------------------------

type claudeLine struct {
	Type string
	ts   int64
}

type claudePart struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// decodeClaude parses a claude line, updating session/cwd state and returning
// the entry type/timestamp plus any tool-use content parts.
func decodeClaude(line []byte, st *state) (claudeLine, []claudePart) {
	var obj struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &obj) != nil {
		return claudeLine{}, nil
	}
	if obj.SessionID != "" {
		st.session = obj.SessionID
	}
	if obj.Cwd != "" {
		st.cwd = obj.Cwd
	}
	cl := claudeLine{Type: obj.Type, ts: iso(obj.Timestamp)}
	if obj.Type != "assistant" || len(obj.Message.Content) == 0 || obj.Message.Content[0] != '[' {
		return cl, nil
	}
	var parts []claudePart
	_ = json.Unmarshal(obj.Message.Content, &parts)
	return cl, parts
}

type codexPayload struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Action    struct {
		Command []string `json:"command"`
	} `json:"action"`
}

// decodeCodex parses a codex line, updating cwd/approval/sandbox state and
// returning the payload plus its timestamp.
func decodeCodex(line []byte, st *state) (codexPayload, int64) {
	var obj struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			codexPayload
			Timestamp      string `json:"timestamp"`
			Cwd            string `json:"cwd"`
			ID             string `json:"id"`
			ApprovalPolicy string `json:"approval_policy"`
			SandboxPolicy  string `json:"sandbox_policy"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &obj) != nil {
		return codexPayload{}, 0
	}
	switch obj.Type {
	case "session_meta":
		if obj.Payload.Cwd != "" {
			st.cwd = obj.Payload.Cwd
		}
		if obj.Payload.ID != "" {
			st.session = obj.Payload.ID
		}
	case "turn_context":
		if obj.Payload.ApprovalPolicy != "" {
			st.approval = obj.Payload.ApprovalPolicy
		}
		if obj.Payload.SandboxPolicy != "" {
			st.sandbox = obj.Payload.SandboxPolicy
		}
	}
	ts := iso(obj.Timestamp)
	if ts == 0 {
		ts = iso(obj.Payload.Timestamp)
	}
	return obj.Payload.codexPayload, ts
}

func cwdFromClaudePath(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	if dir == "" {
		return ""
	}
	return "/" + strings.TrimLeft(strings.ReplaceAll(dir, "-", "/"), "/")
}

// jsonField extracts a string field from a raw JSON object (fail-soft "").
func jsonField(raw json.RawMessage, field string) string {
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

// jsonStringArray extracts a []string field from a raw JSON string that itself
// holds an object (codex function-call arguments are a JSON-encoded string).
func jsonStringArray(argStr, field string) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(argStr), &m) != nil {
		return nil
	}
	var arr []string
	if json.Unmarshal(m[field], &arr) == nil {
		return arr
	}
	return nil
}
