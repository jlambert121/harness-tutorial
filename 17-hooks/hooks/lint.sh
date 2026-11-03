#!/usr/bin/env bash
# post_tool_use hook for edit|write: report gofmt and go vet problems to the model.
# Exit 2 sends stderr back to the model (the edit already happened).
cat > /dev/null
[ -f go.mod ] || exit 0
problems=""
unformatted=$(gofmt -l . 2>&1)
[ -n "$unformatted" ] && problems+="gofmt: these files need formatting (run gofmt -w): $unformatted"$'\n'
vet=$(go vet ./... 2>&1) || problems+="go vet:"$'\n'"$vet"$'\n'
[ -z "$problems" ] && exit 0
printf '%s' "$problems" | head -c 4000 >&2
exit 2
