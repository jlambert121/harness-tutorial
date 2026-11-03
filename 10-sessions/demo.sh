#!/usr/bin/env bash
# Crash, resume, undo, and branch, end to end. Run from your module root:
#   bash <lesson-dir>/demo.sh <path-to>/06-system-prompt/sample-repo
# Uses a throwaway data dir and workspace; costs about $0.02 with Haiku.
set -euo pipefail
SAMPLE="${1:?usage: demo.sh <sample-repo>}"
MODEL="${MODEL:-anthropic/claude-haiku-5.5}"
export HARNESS_DATA_DIR="$(mktemp -d)"
W="$(mktemp -d)/textkit"; cp -R "$SAMPLE" "$W"; (cd "$W" && git init -q)
go build -o /tmp/harness-demo ./cmd/harness && go build -o /tmp/sessions-demo ./cmd/sessions
H=/tmp/harness-demo

echo "== 1. start a task and kill -9 the harness while its first tool call runs"
"$H" exec --json --root "$W" --model "$MODEL" \
  "Implement Reverse so the tests pass, then add an IsPalindrome function with table-driven tests." > /tmp/demo-run1.jsonl &
PID=$!
deadline=$((SECONDS + 120))
until grep -q '"tool_start"' /tmp/demo-run1.jsonl 2>/dev/null; do
  if ! kill -0 "$PID" 2>/dev/null || (( SECONDS >= deadline )); then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
    echo "No tool call started; inspect /tmp/demo-run1.jsonl for the result or error." >&2
    exit 1
  fi
  sleep 0.05
done
kill -9 $PID; wait $PID 2>/dev/null || true
echo "killed: $(grep -c '"tool_start"' /tmp/demo-run1.jsonl) tool call(s) started, $(grep -c '"tool_end"' /tmp/demo-run1.jsonl) finished"

echo; echo "== 2. resume the same session"
"$H" exec --continue --root "$W" --model "$MODEL" "Continue where you left off." | tail -4
echo "synthetic results added by Repair: $(grep -c 'Tool execution was interrupted' "$HARNESS_DATA_DIR"/sessions/*/*.jsonl)"
(cd "$W" && go test ./... 2>&1 | tail -1)

echo; echo "== 3. undo the last prompt (files and conversation)"
printf '/undo\n' | "$H" --ui repl --continue --root "$W" --model "$MODEL" | grep -E "undid|session"
(cd "$W" && grep -c IsPalindrome textkit.go || true)

echo; echo "== 4. take a different branch from there"
"$H" exec --continue --root "$W" --model "$MODEL" "Instead of IsPalindrome, add CountVowels with tests." | tail -2

echo; echo "== 5. the session tree"
/tmp/sessions-demo --root "$W" tree
