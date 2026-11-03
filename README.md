# Build Your Own Coding Agent Harness (in Go)

A guided tutorial for building a coding agent harness, the program around a language model that turns it into something like pi, opencode, Codex CLI, or Claude Code. You build its agent mechanisms one capability per lesson, using supplied code for supporting plumbing. Each lesson explains the design, provides tests, and includes a demo.

## Prerequisites

- Go 1.26+
- A Vercel AI Gateway API key in `AI_GATEWAY_API_KEY`
- macOS or Linux. Lesson 09's sandbox is macOS-only; on Linux, commands run without that sandbox. Use an isolated environment for headless runs.
- Comfort with Go. The lessons explain harness concepts, not the language.

## How each lesson works

```
NN-lesson-name/
  README.md    the lesson: concepts, how real harnesses do it, the contract, build steps, demo
  tests/       test files to copy into your module (paths mirror the module)
  support/     code that isn't the lesson's concept: fake model, demo programs, fixtures
  solution/    reference module as of the end of the lesson
```

1. Read the lesson's `README.md`.
2. From the tutorial directory, copy that lesson's files into your module, for example:
   `cp -R 00-orientation/tests/. 00-orientation/support/. ~/src/my-harness/`
3. Follow the build steps and run `go test ./...`.
4. Run the demo.

The **Build it** steps cover the mechanisms that later lessons build on; orientation sets up supplied catalog code. The **Exercises** are optional experiments; no later lesson requires them. They can change the harness beyond the reference implementation, so use a separate copy if you want to keep the lesson sequence easy to follow.

Run the Go commands from your module. In lesson commands, replace `<path-to>` or `<lesson>` with the absolute path to the tutorial or lesson directory. Some demos reuse `/tmp/textkit` or `/tmp/sample`; use a fresh destination when copying a sample, since `cp -R` behaves differently when the destination already exists.

Use `solution/` when stuck. `diff -r` between two consecutive `solution/` directories shows exactly what a lesson adds.

## Lessons

| # | Lesson | Concepts |
|---|---|---|
| 00 | [Orientation](00-orientation/) | what a harness is; survey of pi, opencode, Codex, Claude Code, Aider; model catalog |
| 01 | [Talking to the model](01-talking-to-the-model/) | Chat Completions, SSE streaming, neutral message types, retries, cost |
| 02 | [The agent loop](02-the-agent-loop/) | tool-call protocol, loop and stop conditions, events, cancellation |
| 03 | [Tool design](03-tool-design/) | schemas, validation, truncation, parallel execution |
| 04 | [Editing files](04-editing-files/) | edit formats, match cascades, read-before-edit |
| 05 | [Running commands](05-running-commands/) | process groups, timeouts, bounded output |
| 06 | [System prompt](06-system-prompt/) | prompt anatomy, AGENTS.md discovery, first real task |
| 07 | [Evaluation](07-evaluation/) | headless mode, verifiable tasks, measuring with repeats |
| 08 | [Terminal UI](08-terminal-ui/) | event-driven UI, steering |
| 09 | [Permissions](09-permissions/) | approval modes, rules, shell parsing, sandboxing |
| 10 | [Sessions](10-sessions/) | append-only logs, resume, branching, undo snapshots |
| 11 | [Context budget](11-context-budget/) | token accounting, pruning old tool output |
| 12 | [Prompt caching](12-prompt-caching/) | prefix caching and the design rules it imposes |
| 13 | [Compaction](13-compaction/) | summarizing history without losing the thread |
| 14 | [Commands and skills](14-commands-and-skills/) | slash commands, templates, progressive disclosure |
| 15 | [Memory](15-memory/) | what persists across sessions and how |
| 16 | [Subagents](16-subagents/) | isolated contexts, delegation |
| 17 | [Hooks](17-hooks/) | extension points, lint/test feedback loops |
| 18 | [MCP](18-mcp/) | Model Context Protocol client, tool overhead |
| 19 | [Model routing](19-model-routing/) | model roles, switching costs, routers |
| 20 | [Where to go next](20-where-to-go-next/) | reading guide to real harness source |

## Other files

- `research-notes.md`: sourced facts about the real harnesses and the gateway, including the wire behavior observed in `docs/probe/probe.sh`.
- `DESIGN.md`: the tutorial's design document.
