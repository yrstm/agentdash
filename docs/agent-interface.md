# Driving agentdash from another agent

agentdash is already an agent-native interface — no MCP server, no plugin,
no daemon required. Any agent that can run a shell command (Claude Code,
Codex, a CI step, a cron job) can read the fleet and triage it through the
ordinary CLI. The `--json` output is a frozen, versioned contract
(`schema_version`), which makes it a stable API surface; the rest of the
commands print human- and machine-readable text. The tool stays read-only:
it can *report* and *suggest* actions, never silently kill or relaunch a
session — which is exactly what makes it safe to hand to an agent.

## The commands an agent uses

| Goal | Command | Notes |
|---|---|---|
| Read the whole fleet | `agentdash --json` | `schema_version`, `agents[]`, `ports[]`. Parse this. Claude and Codex rows are built in; Hermes rows appear too in `-tags hermes` builds, same v1 shape. |
| Is anything blocked? | `agentdash --any-waiting` | Exit 0 if an agent needs you, 1 otherwise. Cheap gate. |
| Drill into one agent | `agentdash show <pid>` | Recent turns, session path, resume command. |
| Why a value is what it is | `agentdash why <pid>` | Pairing evidence and per-cell provenance. |
| Get the resume command | `agentdash resume <pid>` | Paste-ready `--resume`/`resume` line, with cwd. |
| What changed recently | `agentdash recap [4h]` | Transitions since you last looked. |

Address agents by **pid** (stable across refreshes) rather than row
number, which is positional. Pids come straight from `--json`.

## The JSON shape

`agentdash --json` emits one document:

```json
{
  "schema_version": 1,
  "agents": [
    {
      "agent": "claude",
      "pid": 4123,
      "tty": "pts/3",
      "tmux": "detached",
      "needs_you": true,
      "uptime_s": 5402,
      "last_write": "4m",
      "model": "opus-4-8",
      "tokens": "34m/359k",
      "ctx": "45%",
      "status": "waiting",
      "cwd": "/home/dev/code/app-be",
      "task": "rebase the feature branch"
    }
  ],
  "ports": []
}
```

`needs_you` is the field to branch on: it is true for `waiting`, `stuck?`,
and `respawn` statuses. Event hooks (`--on-needs-you` / `--on-stuck`, see
the README) emit the same per-agent object, so a streaming consumer and a
polling one see identical fields.

## A ready-to-paste skill

Drop this in a supervising project as `.claude/skills/fleet/SKILL.md` (or
fold it into `CLAUDE.md`) to teach an agent how to mind the others. No code,
no MCP — it leans entirely on the existing CLI:

```markdown
---
name: fleet
description: Inspect and triage the local agent fleet with agentdash. Use when
  asked to check on running agents, see who is blocked, or get a resume command.
---

To see every running agent and its state, run `agentdash --json` and parse it.
Branch on each agent's `needs_you` and `status` fields.

- To check quickly whether anything is blocked: `agentdash --any-waiting`
  (exit 0 means an agent needs attention).
- To understand one agent, run `agentdash show <pid>` for its recent turns and
  `agentdash why <pid>` for where its values came from.
- To hand the user a way back in, run `agentdash resume <pid>` and report the
  printed command verbatim.

Address agents by pid, never by row number. agentdash is read-only: report
findings and suggest resume/kill commands, but never run anything that kills or
relaunches a session yourself — leave that decision to the user.
```

## When you would reach for MCP instead

Only two cases justify wrapping this in an MCP server: the consuming agent
has **no shell access** (a hosted/sandboxed runtime), or you want agentdash
to appear in a **tool-picker menu across many MCP clients**. If neither
holds, the CLI is the lighter and more direct interface — keep agentdash a
one-shot reader and let the agent call it.
