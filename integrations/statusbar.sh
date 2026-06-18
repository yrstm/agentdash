#!/usr/bin/env bash
# SwiftBar/xbar feed rendered from `agentdash --json`.
# For a remote devbox, run it over ssh:
#   AGENTDASH='ssh devbox agentdash' ./statusbar.sh
AGENTDASH=${AGENTDASH:-agentdash}

J=$($AGENTDASH --json 2>/dev/null) || { echo "🖥 ?"; exit 0; }

n=$(jq '.agents | length' <<< "$J")
need=$(jq '[.agents[] | select(.needs_you)] | length' <<< "$J")
flagged=$(jq '[.ports[] | select((.flags | length) > 0)] | length' <<< "$J")

top="🖥 ${n}a"
[ "$need" -gt 0 ] && top+=" ${need}w"
[ "$flagged" -gt 0 ] && top+=" ${flagged}!"
if [ "$flagged" -gt 0 ]; then echo "$top | color=red"
elif [ "$need" -gt 0 ]; then echo "$top | color=yellow"
else echo "$top"; fi

echo "---"
jq -r '.agents[] |
  "\(.agent) \(.status // "-") · \(.task) | font=Menlo size=11"' <<< "$J"
echo "---"
jq -r '.ports[] | select((.flags | length) > 0) |
  ":\(.port) \(.process) \(.flags | join(",")) | color=red font=Menlo size=11"' <<< "$J"
echo "full board: tmux a -t dash | color=gray size=11"
