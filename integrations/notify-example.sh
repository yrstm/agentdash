#!/usr/bin/env bash
# Example agentdash event hook. Wire it up with:
#   agentdash -w --on-needs-you 'integrations/notify-example.sh'
#   agentdash -w --on-stuck     'integrations/notify-example.sh'
#
# agentdash runs this once per state transition. The agent row arrives as
# JSON on stdin (same shape as an entry in `agentdash --json`), wrapped in
# an {event, ts, attached, agent} envelope; the headline fields are also in
# the environment as AGENTDASH_EVENT / AGENTDASH_PID / AGENTDASH_TASK.
#
# This sample just composes a one-line message and hands it to whatever
# notifier you have. Replace the `deliver` body with your transport (ntfy,
# Pushover, Slack webhook, terminal-notifier, …) — that is the line that
# touches the network; agentdash never does.
set -euo pipefail

payload=$(cat)

# Prefer jq for robust fields; fall back to the env vars if it is absent.
if command -v jq >/dev/null 2>&1; then
  event=$(jq -r '.event' <<<"$payload")
  pid=$(jq -r '.agent.pid' <<<"$payload")
  task=$(jq -r '.agent.task // "(no task)"' <<<"$payload")
  cwd=$(jq -r '.agent.cwd // "-"' <<<"$payload")
  attached=$(jq -r '.attached' <<<"$payload")
else
  event=${AGENTDASH_EVENT:-?}
  pid=${AGENTDASH_PID:-?}
  task=${AGENTDASH_TASK:-"(no task)"}
  cwd="-"
  attached="unknown"
fi

# Stay quiet for agents you are already attached to, if you like:
#   [ "$attached" = "true" ] && exit 0

msg="agentdash: ${event} · pid ${pid} · ${task} (${cwd})"

deliver() {
  # --- replace this with your transport ---
  # ntfy:      curl -fsS -d "$1" https://ntfy.sh/your-topic
  # Pushover:  curl -fsS -F "token=$PO_TOKEN" -F "user=$PO_USER" -F "message=$1" \
  #              https://api.pushover.net/1/messages.json
  # Slack:     curl -fsS -X POST -H 'Content-type: application/json' \
  #              -d "{\"text\":\"$1\"}" "$SLACK_WEBHOOK_URL"
  # macOS:     terminal-notifier -message "$1" -title agentdash
  echo "$1"
}

deliver "$msg"
