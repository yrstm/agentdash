package usage

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/yrstm/agentdash/internal/parse"
)

// addFn receives one transcript's flattened events and identity.
type addFn func(path, agent, model, project string, subagent bool, evs []event, title string)

// scanClaude walks ~/.claude/projects and emits per-message usage events.
// Usage blocks are deduped by message id (a resumed/streamed message repeats
// the same id with the same cumulative usage), matching the board's rule.
func scanClaude(home string, add addFn) {
	root := filepath.Join(home, ".claude", "projects")
	repos := map[string]string{}
	walkJSONL(root, func(path string) {
		var (
			evs               []event
			model, cwd, title string
			seen              = map[string]bool{}
		)
		scanLines(path, func(line []byte) {
			var obj struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				Cwd       string `json:"cwd"`
				IsMeta    bool   `json:"isMeta"`
				Message   struct {
					ID      string          `json:"id"`
					Model   string          `json:"model"`
					Content json.RawMessage `json:"content"`
					Usage   struct {
						InputTokens              int64 `json:"input_tokens"`
						CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
						CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
						OutputTokens             int64 `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &obj) != nil {
				return
			}
			if obj.Cwd != "" && cwd == "" {
				cwd = obj.Cwd
			}
			if obj.Message.Model != "" {
				model = obj.Message.Model
			}
			if title == "" && obj.Type == "user" && !obj.IsMeta {
				if t := parse.Clean(claudeText(obj.Message.Content), 80); t != "" {
					title = t
				}
			}
			// a usage block counts once per message id
			u := obj.Message.Usage
			hasUsage := u.InputTokens != 0 || u.OutputTokens != 0 ||
				u.CacheCreationInputTokens != 0 || u.CacheReadInputTokens != 0
			if !hasUsage {
				return
			}
			if obj.Message.ID != "" {
				if seen[obj.Message.ID] {
					return
				}
				seen[obj.Message.ID] = true
			}
			evs = append(evs, event{
				ts:            iso(obj.Timestamp),
				model:         obj.Message.Model,
				in:            u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
				out:           u.OutputTokens,
				cacheRead:     u.CacheReadInputTokens,
				cacheCreation: u.CacheCreationInputTokens,
			})
		})
		if cwd == "" {
			cwd = cwdFromClaudePath(path)
		}
		add(path, "claude", model, repoOf(cwd, repos), isClaudeSubagent(path), evs, title)
	})
}

// scanCodex walks ~/.codex/sessions. Codex reports cumulative token_count
// events; the per-turn delta is last_token_usage, which is what we bucket by
// the event timestamp. Codex does not split cache read vs creation, so it
// contributes to the windows but not the cache-hit stats.
func scanCodex(home string, add addFn) {
	root := filepath.Join(home, ".codex", "sessions")
	repos := map[string]string{}
	walkJSONL(root, func(path string) {
		var (
			evs               []event
			model, cwd, title string
		)
		scanLines(path, func(line []byte) {
			var obj struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				Payload   struct {
					Type      string `json:"type"`
					Timestamp string `json:"timestamp"`
					Cwd       string `json:"cwd"`
					Model     string `json:"model"`
					Message   string `json:"message"`
					Info      struct {
						LastTokenUsage struct {
							InputTokens       int64 `json:"input_tokens"`
							CachedInputTokens int64 `json:"cached_input_tokens"`
							OutputTokens      int64 `json:"output_tokens"`
						} `json:"last_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &obj) != nil {
				return
			}
			switch obj.Type {
			case "session_meta":
				if obj.Payload.Cwd != "" {
					cwd = obj.Payload.Cwd
				}
			case "turn_context":
				if obj.Payload.Model != "" {
					model = obj.Payload.Model
				}
			case "event_msg":
				switch obj.Payload.Type {
				case "user_message":
					if title == "" {
						if t := parse.Clean(obj.Payload.Message, 80); t != "" {
							title = t
						}
					}
				case "token_count":
					last := obj.Payload.Info.LastTokenUsage
					in := last.InputTokens + last.CachedInputTokens
					if in == 0 && last.OutputTokens == 0 {
						return
					}
					ts := iso(obj.Timestamp)
					if ts == 0 {
						ts = iso(obj.Payload.Timestamp)
					}
					evs = append(evs, event{ts: ts, model: model, in: in, out: last.OutputTokens})
				}
			}
		})
		add(path, "codex", model, repoOf(cwd, repos), false, evs, title)
	})
}

func isClaudeSubagent(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "subagents" ||
		strings.HasPrefix(filepath.Base(path), "agent-")
}

func cwdFromClaudePath(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	if dir == "" {
		return ""
	}
	return "/" + strings.TrimLeft(strings.ReplaceAll(dir, "-", "/"), "/")
}

// claudeText flattens a Claude message content (string or typed-part array)
// down to its human text, for the session title.
func claudeText(raw json.RawMessage) string {
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
