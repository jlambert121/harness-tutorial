#!/usr/bin/env bash
# pre_tool_use hook for bash: block git push from the agent.
if grep -q 'git[^"]*push' ; then
  echo "git push is reserved for humans in this repository" >&2
  exit 2
fi
