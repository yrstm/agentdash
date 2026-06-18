package parse

import (
	"encoding/json"
	"fmt"
)

type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

type codexTokenInfo struct {
	TotalTokenUsage    codexUsage `json:"total_token_usage"`
	LastTokenUsage     codexUsage `json:"last_token_usage"`
	ModelContextWindow int64      `json:"model_context_window"`
}

type codexPayload struct {
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Info      codexTokenInfo  `json:"info"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Model     string          `json:"model"`
	Cwd       string          `json:"cwd"`
}

type codexLine struct {
	Timestamp string        `json:"timestamp"`
	Type      string        `json:"type"`
	Payload   *codexPayload `json:"payload"`
}

func updateCodex(ent *Entry, line []byte) {
	var obj codexLine
	if json.Unmarshal(line, &obj) != nil || obj.Payload == nil {
		return
	}
	pay := obj.Payload
	switch {
	case obj.Type == "event_msg":
		switch pay.Type {
		case "user_message":
			ent.LastType = "user"
			if ts := isoEpoch(obj.Timestamp); ts != 0 {
				ent.LastUserTS = ts
			}
			if ent.TitleUser == "" && usableTitle(pay.Message) {
				ent.TitleUser = pay.Message
			}
		case "agent_message":
			ent.LastType = "assistant"
			if pay.Message != "" {
				ent.LastText = truncRunes(collapseWS(pay.Message), lastTextW)
				ent.Activity = ent.LastText
			}
		case "token_count":
			tot := pay.Info.TotalTokenUsage
			ent.In = tot.InputTokens + tot.CachedInputTokens
			ent.Out = tot.OutputTokens
			last := pay.Info.LastTokenUsage
			if li := last.InputTokens + last.CachedInputTokens; li != 0 {
				ent.Ctx = li
			}
			if pay.Info.ModelContextWindow != 0 {
				ent.Win = pay.Info.ModelContextWindow
			}
		}
	case obj.Type == "response_item" && pay.Type == "message":
		if pay.Role == "user" || pay.Role == "assistant" {
			ent.LastType = pay.Role
		}
		if pay.Role == "user" && ent.TitleUser == "" {
			var raws []json.RawMessage
			if json.Unmarshal(pay.Content, &raws) == nil {
				for _, r := range raws {
					var p struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}
					if json.Unmarshal(r, &p) == nil &&
						(p.Type == "input_text" || p.Type == "text") {
						if usableTitle(p.Text) {
							ent.TitleUser = p.Text
							break
						}
					}
				}
			}
		}
	case obj.Type == "response_item" && pay.Type == "function_call":
		ent.LastType = "tool"
		name := pay.Name
		if name == "" {
			name = "tool"
		}
		detail := codexToolDetail(pay.Arguments)
		if detail == "" {
			ent.LastTool = name
		} else {
			ent.LastTool = fmt.Sprintf("%s: %s", name, detail)
		}
		ent.LastTool = truncRunes(collapseWS(ent.LastTool), lastTextW)
		ent.Activity = ent.LastTool
	case obj.Type == "turn_context" && pay.Model != "":
		ent.Model = pay.Model
	}
}

func codexToolDetail(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = []byte(s)
	}
	return toolInputDetail(raw)
}
