// Package jsonout serializes the board as --json schema_version 1, a
// frozen contract: field-for-field identical to v1. New fields require
// schema_version 2.
package jsonout

import (
	"encoding/json"
	"strings"

	"github.com/yrstm/agentdash/internal/board"
)

type agentV1 struct {
	Agent    string  `json:"agent"`
	PID      int     `json:"pid"`
	TTY      string  `json:"tty"`
	Tmux     *string `json:"tmux"`
	NeedsYou bool    `json:"needs_you"`
	UptimeS  int64   `json:"uptime_s"`
	LastW    *string `json:"last_write"`
	Model    *string `json:"model"`
	Tokens   *string `json:"tokens"`
	Ctx      *string `json:"ctx"`
	Status   *string `json:"status"`
	Cwd      string  `json:"cwd"`
	Task     string  `json:"task"`
}

type portV1 struct {
	Port    int      `json:"port"`
	Process string   `json:"process"`
	PID     int      `json:"pid"`
	Cwd     *string  `json:"cwd"`
	Flags   []string `json:"flags"`
}

type topV1 struct {
	SchemaVersion int       `json:"schema_version"`
	Agents        []agentV1 `json:"agents"`
	Ports         []portV1  `json:"ports"`
}

func nullDash(s string) *string {
	if s == "-" || s == "" {
		return nil
	}
	return &s
}

// agentRow projects a board row onto the frozen schema_version 1 agent
// shape, so every emitter (the --json document, event hooks) marshals the
// same fields the same way.
func agentRow(r board.Row) agentV1 {
	return agentV1{
		Agent: r.Kind, PID: r.PID, TTY: r.TTY,
		Tmux: tmuxState(r.Glyph), NeedsYou: r.Need, UptimeS: r.Uptime,
		LastW: nullDash(r.Last), Model: nullDash(r.Model),
		Tokens: nullDash(r.Tokens), Ctx: nullDash(r.Ctx),
		Status: nullDash(r.Status), Cwd: r.Cwd, Task: r.Task,
	}
}

// AgentJSON marshals a single board row as one schema_version 1 agent
// object (no indentation, the compact form event hooks pass on stdin). The
// field set is identical to an entry in the --json `agents` array.
func AgentJSON(r board.Row) ([]byte, error) {
	return json.Marshal(agentRow(r))
}

func tmuxState(glyph string) *string {
	var s string
	switch glyph {
	case "●":
		s = "attached"
	case "○":
		s = "detached"
	default:
		return nil
	}
	return &s
}

// Emit renders the schema_version 1 document, indented like v1.
func Emit(b *board.Board) ([]byte, error) {
	top := topV1{SchemaVersion: 1, Agents: []agentV1{}, Ports: []portV1{}}
	for _, r := range b.Rows {
		top.Agents = append(top.Agents, agentRow(r))
	}
	for _, p := range b.Ports {
		flags := []string{}
		for _, f := range p.Flags {
			flags = append(flags, strings.TrimPrefix(f, "SUSPECT:"))
		}
		var cwd *string
		if p.Cwd != "" {
			c := p.Cwd
			cwd = &c
		}
		top.Ports = append(top.Ports, portV1{
			Port: p.Port, Process: p.Proc, PID: p.PID, Cwd: cwd, Flags: flags,
		})
	}
	return json.MarshalIndent(top, "", "  ")
}
