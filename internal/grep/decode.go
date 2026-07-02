package grep

import (
	"bytes"
	"encoding/json"
)

// decodeClaude extracts the searchable messages and session metadata from one
// Claude Code transcript line. With tools=true it also surfaces tool-call
// payloads (tool_use inputs and tool_result content) as assistant/user text.
func decodeClaude(line []byte, tools bool) ([]message, lineMeta) {
	var obj struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		IsMeta    bool   `json:"isMeta"`
		Message   struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &obj) != nil {
		return nil, lineMeta{}
	}
	meta := lineMeta{sessionID: obj.SessionID, cwd: obj.Cwd, ts: iso(obj.Timestamp)}
	if obj.IsMeta {
		return nil, meta
	}
	var role string
	switch obj.Type {
	case "user":
		role = "user"
	case "assistant":
		role = "assistant"
	default:
		return nil, meta
	}
	text, toolText := claudeContent(obj.Message.Content)
	var msgs []message
	if text != "" {
		msgs = append(msgs, message{role, text})
	}
	if tools && toolText != "" {
		msgs = append(msgs, message{role, toolText})
	}
	return msgs, meta
}

// claudeContent splits a message's content into human text and tool-payload
// text. Content is either a bare string or an array of typed parts.
func claudeContent(raw json.RawMessage) (text, toolText string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s, ""
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return "", ""
	}
	var textB, toolB []string
	for _, r := range parts {
		var p struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(r, &p) != nil {
			continue
		}
		switch p.Type {
		case "text", "input_text":
			if p.Text != "" {
				textB = append(textB, p.Text)
			}
		case "tool_use":
			toolB = append(toolB, p.Name+" "+string(p.Input))
		case "tool_result":
			toolB = append(toolB, string(p.Content))
		}
	}
	return join(textB), join(toolB)
}

// decodeCodex is the Codex-rollout counterpart of decodeClaude.
func decodeCodex(line []byte, tools bool) ([]message, lineMeta) {
	var obj struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			ID        string          `json:"id"`
			Timestamp string          `json:"timestamp"`
			Cwd       string          `json:"cwd"`
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Message   string          `json:"message"`
			Content   json.RawMessage `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &obj) != nil {
		return nil, lineMeta{}
	}
	ts := iso(obj.Timestamp)
	if ts == 0 {
		ts = iso(obj.Payload.Timestamp)
	}
	meta := lineMeta{cwd: obj.Payload.Cwd, ts: ts}
	if obj.Type == "session_meta" {
		meta.sessionID = obj.Payload.ID
		return nil, meta
	}

	var msgs []message
	switch obj.Type {
	case "event_msg":
		switch obj.Payload.Type {
		case "user_message":
			msgs = append(msgs, message{"user", obj.Payload.Message})
		case "agent_message":
			msgs = append(msgs, message{"assistant", obj.Payload.Message})
		}
	case "response_item":
		switch obj.Payload.Type {
		case "message":
			if r := obj.Payload.Role; r == "user" || r == "assistant" {
				if t, _ := claudeContent(obj.Payload.Content); t != "" {
					msgs = append(msgs, message{r, t})
				}
			}
		case "function_call", "local_shell_call", "custom_tool_call":
			if tools {
				msgs = append(msgs, message{"assistant", string(line)})
			}
		case "function_call_output":
			if tools {
				msgs = append(msgs, message{"user", string(line)})
			}
		}
	}
	return msgs, meta
}

func join(ss []string) string {
	out := ""
	for _, s := range ss {
		if s == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += s
	}
	return out
}

// JSON renders a Result as the schema_version 1 grep document.
func JSON(res Result, pattern string) ([]byte, error) {
	if res.Hits == nil {
		res.Hits = []Hit{}
	}
	doc := struct {
		SchemaVersion int    `json:"schema_version"`
		Pattern       string `json:"pattern"`
		Count         int    `json:"count"`
		Truncated     bool   `json:"truncated"`
		Hits          []Hit  `json:"hits"`
	}{SchemaVersion, pattern, len(res.Hits), res.Truncated, res.Hits}
	return json.MarshalIndent(doc, "", "  ")
}
