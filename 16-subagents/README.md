# Lesson 16: Subagents

## What you'll build

A `task` tool that runs a child agent. The child has its own context window, a restricted tool list, and its own model if wanted, and it returns only its final answer. That includes:

- a built-in read-only `explore` agent on the small model;
- agent definitions loaded from markdown files;
- parallel subagents;
- child usage counted in the parent's totals;
- live progress in the TUI.

The demo answers one codebase question both ways. With subagents, the parent's context is 60% smaller, and the whole run costs 3× more.

## How the real harnesses do it

- **Claude Code.** Each subagent has its "own context window with a custom system prompt, specific tool access, and independent permissions"; only the final result returns. Subagents get their own short system prompt plus environment details, not the main one.
  - **Definitions** live in `.claude/agents/*.md`. Fields include `tools` (an allowlist), `disallowedTools`, `model` (with `inherit`), `permissionMode`, `maxTurns` and `isolation: worktree`.
  - **Built-ins:** Explore and Plan (read-only), general-purpose.
  - **Limits:** nesting up to 3 levels, up to 20 concurrent.
  - **Return size.** Anthropic's context-engineering post says subagents return 1–2k-token summaries.
- **opencode.** The `task` tool takes `description`, `prompt`, `subagent_type` and an optional `task_id` to resume a child session.
  - A child is a session whose `parentID` is the parent's session.
  - **Depth limit 1 by default:** subagents can't spawn subagents.
  - The child inherits only the parent's deny rules, plus a default deny on `task` and `todowrite`.
  - Built-ins: `general` and `explore` (read-only tool set).
- **Codex.** `agents.default_subagent_model` configures the child model.
- **pi** has **none**, on purpose. Zechner calls subagents "a black box within a black box": the parent can't see what the child read or why it concluded what it did. "Using a sub-agent mid-session for context gathering is a sign you didn't plan ahead." The one use he accepts is a slash command that spawns `pi --print`, for example for a code review.

The trade-off: a subagent spends tokens in *its* context so the parent doesn't have to. That's a win when the parent would otherwise fill up with search results it reads once. It's a loss when the work is small, or when the parent needs the details the child threw away.

## Design for our harness

```
parent agent ── task{agent:"explore", description, prompt} ──▶ Runner.run
                                                               ├─ child registry: parent tools ∩ def.Tools, minus "task" (depth 1)
                                                               ├─ child Env: same Root/Perm/Sandbox; own Cwd and ReadTracker
                                                               ├─ child agent: def.System, def.Model (or parent's), MaxSteps
                                                               ├─ events: subagent{phase: start|tool|end} via RunContext.Emit
                                                               ├─ usage: RunContext.AddUsage → parent totals
                                                               └─ result: <task_result agent="explore">final answer</task_result>
```

- **The prompt must be self-contained.** The child sees *nothing* from the parent's conversation. The tool description says so, because the parent model otherwise writes "fix the bug we discussed".
- **`task` is read-only in the registry's eyes**, so several `task` calls in one response run in parallel (lesson 03). The child's own tool list decides what it can actually do; `explore` has only read, grep, glob and ls.
- **Children share the permission checker**, so deny rules and the sandbox apply. Each child gets **its own `Cwd` and `ReadTracker`**: parallel children must not move each other's working directory.
- **Reporting through the context.** The agent package imports `tools`, so `tools` can't import `agent`. Instead, `Agent.Run` puts a `RunContext{Emit, AddUsage}` in the context passed to tools, and the task tool pulls it out with `agent.RunContextFrom(ctx)`. Emission is now serialized with a mutex, because children emit from tool goroutines.
- **Usage roll-up.** Without `AddUsage`, the eval's cost figures (lesson 07) would silently leave out every child's spending.
- **Definitions** come from `~/.config/harness/agents/*.md` and `<root>/.harness/agents/*.md`:

  ```markdown
  ---
  name: reviewer
  description: Reviews a diff for bugs
  model: anthropic/claude-haiku-5.5
  tools: read, grep, bash
  max_steps: 12
  ---
  You review code. Report problems with file:line references.
  ```

  The built-in `explore` runs on `--small-model` (default Haiku 5.5): Claude Code's background-model idea applied to research.

## The contract

`harness/internal/agent`:

```go
const Subagent EventKind = "subagent"
type RunContext struct { Emit func(Event); AddUsage func(llm.Usage) }
func WithRunContext(ctx context.Context, rc RunContext) context.Context
func RunContextFrom(ctx context.Context) (RunContext, bool)
```

`Run` installs a `RunContext` whose `Emit` is serialized and whose `AddUsage` adds to both the run's usage (reported in `agent_end`) and the agent's lifetime usage. Protect them with `a.mu`.

`harness/internal/subagent`:

```go
type Def struct { Name, Description, Model, System string; Tools []string; MaxSteps int }
var Explore Def
func LoadDefs(dirs ...string) []Def            // built-ins + *.md files
type Runner struct { Client llm.Client; Parent *tools.Registry; DefaultModel string; Defs []Def; Cache llm.CacheMode }
func TaskTool(r Runner) tools.Tool             // "task": {agent, description, prompt}
```

Subagent event `Data`:
- `start`: `id`, `agent`, `description`, `phase`, `model`
- `tool`: `id`, `agent`, `description`, `phase`, `tool`, `args`
- `end`: `id`, `agent`, `description`, `phase`, `input`, `output`, `cost_usd`

TUI: one cell per subagent `id`, updated in place. It shows `⇢ agent: description`, then `N tool calls, last: X`, then `done · N tool calls · $cost`.

Provided in `support/`: `cmd/harness` (registers `task` unless `--no-subagents`; `--small-model`) and `cmd/subagentlab`.

## Build it

Continue from lesson 15 and copy this lesson’s `tests/` and `support/`. The `internal/subagent/` implementation is new; the harness wiring and demo are supplied. You will also change the existing agent and TUI.

1. **RunContext.** In `Run`, wrap `emit` with a mutex, then `ctx = WithRunContext(ctx, …)` before anything uses `ctx`. Run `go test -race ./internal/agent` after.
2. **Definitions.** Test: `go test ./internal/subagent -run TestLoadDefs`.
3. **The task tool.** Tests: `-run 'TestSubagentIsolated|TestUnknownAgent'`. Filter the parent registry with `Registry.Get`. Take the child's answer from the last assistant message of its history.
4. **Parallelism and usage.** Run independent children concurrently and include their usage in the parent’s accounting. Tests: `-run 'TestParallel|TestChildUsage'`.
5. **TUI cell.** Show each child’s progress separately as its events arrive. Test: `go test ./internal/ui/tui -run TestSubagentCell`.

## Run it

```sh
go test -race ./...
go run ./cmd/harness --root <your-module> --mode plan
> Use explore subagents in parallel to find where permissions are checked and where sessions are written, then explain both.
```

## Demo

`go run ./cmd/subagentlab -corpus <tutorial>/15-memory/solution`. The question is about this harness's own code (lesson 15's solution, 102 Go files). Haiku 5.5 is both parent and child; plan mode; 6 runs, about $0.07.

```
CONFIG             RUN  PARENT PEAK CONTEXT  SUBAGENTS  PARENT TOOL CALLS  TOTAL COST  TIME  ANSWER NAMES
inline             1    20339                0          11                 $0.0055     20s   4/4
inline             2    18709                0          12                 $0.0060     26s   4/4
inline             3    21334                0          14                 $0.0069     31s   4/4
explore subagents  1    8282                 3          3                  $0.0167     34s   4/4
explore subagents  2    8199                 3          4                  $0.0187     39s   4/4
explore subagents  3    8784                 3          3                  $0.0185     38s   4/4
```

What it shows:

- **Subagents kept the parent's context 60% smaller** (≈8k vs ≈20k tokens). The parent made 3–4 calls (the tasks) instead of 11–14 reads and greps. In a long session that headroom is what delays compaction (lesson 13).
- **They cost 3× more and took longer.** Three children each re-read overlapping files from cold caches, and each paid its own system prompt and tool definitions. Parallelism didn't make up for the extra round trips.
- **Answer quality was the same** on this check: all runs named `Execute`, `agent.go`, `Stream` and `registry.go`. For a question that fits comfortably in one context, delegation bought nothing but context headroom. This is pi's critique in numbers.
- **When delegation pays:** the parent's context is the scarce resource (long sessions, large repos), the subtasks are independent, or the child can use a much cheaper model than the parent. Exercise 1 tests the last one.

## Exercises

1. Make the parent Sonnet 5.5 and keep `explore` on Haiku. Compare cost and answer quality against inline Sonnet. When is the extra context and delegation cost worthwhile?
2. Give children a token budget and return partial findings when it is reached. How does the parent handle incomplete research?

## Further reading

- Claude Code subagents: https://code.claude.com/docs/en/sub-agents
- Anthropic, "Effective context engineering for AI agents" (sub-agent architectures): https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- opencode task tool: `packages/opencode/src/tool/task.ts` (v1.x tags) in https://github.com/anomalyco/opencode
- pi on subagents: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
