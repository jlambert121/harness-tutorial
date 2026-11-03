#!/bin/sh
# Stand-in for "harness exec --json": writes "done" into the workspace and
# prints a JSONL event stream. Fails (exit 3) if hidden check files are visible.
root=""
trace=/dev/null
while [ $# -gt 1 ]; do
  case "$1" in
    --root) root="$2"; shift 2 ;;
    --trace) trace="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[ -e "$root/check.sh" ] && { echo "check files visible to the agent" >&2; exit 3; }
echo done > "$root/file.txt"
echo '{"kind":"agent_start","time":"2026-10-20T10:00:00Z"}' | tee "$trace"
echo '{"kind":"turn_start","turn":1,"time":"2026-10-20T10:00:01Z"}'
echo '{"kind":"turn_start","turn":2,"time":"2026-10-20T10:00:02Z"}'
echo '{"kind":"agent_end","usage":{"input":1000,"output":100,"cache_read":400,"cost_usd":0.002},"time":"2026-10-20T10:00:03Z"}'
