#!/usr/bin/env bash
# stop hook: refuse to finish while go test fails. Exit 2 sends the failure
# back to the model as a new message (at most three times in a row).
cat > /dev/null
[ -f go.mod ] || exit 0
out=$(go test ./... 2>&1) && exit 0
printf 'You are not done: go test ./... fails.\n%s\n' "$(printf '%s' "$out" | tail -n 30)" >&2
exit 2
