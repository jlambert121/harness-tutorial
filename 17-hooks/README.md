# Lesson 17: Hooks and the Feedback Loop

## What you'll build

User-configured shell commands that run at five points in the agent's life:

- `session_start`
- `user_prompt_submit`
- `pre_tool_use`
- `post_tool_use`
- `stop`

They receive JSON on stdin. Exit code 2 blocks (with stderr as the reason), and JSON on stdout can rewrite arguments or add context. Three example hooks build a lint/test **feedback loop**: report `gofmt`/`go vet` problems after every edit, refuse to let the agent finish while `go test` fails, and block `git push`. The demo runs the eval suite on two models with and without them (192 runs).

## How the real harnesses do it

- **Claude Code** has the largest hook system: SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, PostToolUse, PostToolUseFailure, Stop, SubagentStart/Stop, PreCompact/PostCompact, Notification, SessionEnd, FileChanged, PreModelSwitch and more.
  - **Handlers:** shell commands (JSON on stdin), HTTP, MCP tools, a single LLM prompt, or an agent. Matching hooks run in parallel. Default timeout 600 s for commands.
  - **Exit 0:** stdout is parsed as JSON.
  - **Exit 2:** blocking. What that means depends on the event:
    - PreToolUse: the call is blocked.
    - UserPromptSubmit: the prompt is rejected.
    - Stop: Claude keeps working.
    - PostToolUse: stderr is shown to Claude (the tool already ran).
  - **Anything else:** a non-blocking error.
  - **JSON fields:** `permissionDecision`, `updatedInput`, `updatedToolOutput`, `additionalContext` (arrives as a system reminder), `decision: "block"`.
  - **Matchers:** plain names and `A|B` lists match exactly; anything else is a regex.
- **pi** has **extensions** instead of hooks: in-process TypeScript modules subscribing to `input`, `tool_call` (authorize or modify), `tool_result` (redact or replace), `turn_end`, `session_before_compact` and more.
- **opencode** has plugin hooks `tool.execute.before` and `tool.execute.after` around every tool.
- **Codex** runs Stop hooks at the end of a turn.

The **feedback loop** is older than hooks:

- **Aider** auto-lints every edited file and auto-runs tests (`--auto-test`). Errors are shown inside their enclosing function "because LLMs handle line numbers poorly". In its SWE-bench Lite run, a solution counted as "plausible" only with no edit, lint or test errors. Up to 6 attempts were made, and the first two produced about 90% of resolved solutions.
- **opencode** feeds LSP diagnostics back after every edit: "LSP errors detected in this file, please fix:".

## Design for our harness

```
hooks.json  (~/.config/harness, <root>/.harness, --hooks FILE; commands may use $HOOKS_DIR)
{"hooks": [{"event": "post_tool_use", "matcher": "edit|write", "command": "$HOOKS_DIR/lint.sh", "timeout_sec": 120}, ...]}

stdin:  {"event":"pre_tool_use","tool":"bash","cwd":"/repo","args":{"command":"git push"}}
exit 0  + stdout JSON {"decision":"block","reason":…} | {"updated_args":{…}} | {"additional_context":"…"}
exit 2  → block; stderr is the reason
other   → reported as a hook error, not blocking
timeout → killed (process group), reported, not blocking
```

| Event | Where | Block means | Context goes |
|---|---|---|---|
| `session_start` | `cmd/harness`, before the first prompt | — | first-message context (plain stdout counts) |
| `user_prompt_submit` | `Config.OnPrompt` | prompt refused, `Run` returns an error | reminder on that prompt (plain stdout counts) |
| `pre_tool_use` | `Registry.run`, before the tool | tool not run; reason returned to the model | — (`updated_args` replace arguments) |
| `post_tool_use` | `Registry.run`, after the tool | stderr appended (it already ran) | appended as `<hook_output>…</hook_output>` |
| `stop` | `Config.OnStop`, when the model answers with no tool calls | reason sent back as a user message; loop continues (max 3 in a row) | — |

- **Hooks run outside the sandbox and outside the permission checker.** They're the user's code, like Claude Code's. A hook is configuration you trust; a tool call is a request you check.
- **Matchers:** empty or `*` matches all; `name` or `a|b` match exactly (`edit` doesn't match `edit_notebook`); anything else is a regex (`mcp__.*`).
- **The stop-hook cap (3)** keeps a hook that can never be satisfied from looping forever. After three tries the agent stops anyway, and the failure is in the transcript.

### The example hooks (`hooks/`)

- **`lint.sh`** (`post_tool_use`, `edit|write`): `gofmt -l .` and `go vet ./...`. Problems go to stderr with exit 2, so they're appended to the edit's result.
- **`test-on-stop.sh`** (`stop`): `go test ./...`. On failure it prints "You are not done…" plus the last 30 lines, and exits 2.
- **`no-push.sh`** (`pre_tool_use`, `bash`): blocks any command matching `git…push`.

## The contract

`harness/internal/hooks`:

```go
type Event string // SessionStart="session_start", UserPromptSubmit, PreToolUse, PostToolUse, Stop
type Hook struct { Event Event; Matcher, Command string; TimeoutSec int }   // json: event, matcher, command, timeout_sec
func Load(paths ...string) ([]Hook, error)        // rejects unknown events
func Match(matcher, tool string) bool
type Outcome struct { Block bool; Reason string; UpdatedArgs json.RawMessage; AdditionalContext, Error string }
func New(root string, hooks []Hook) *Runner
func (r *Runner) Run(ctx context.Context, event Event, tool string, payload map[string]any) Outcome   // nil-safe
```

- **Multiple matching hooks:** any block blocks (first reason wins), the last `updated_args` wins, contexts join with `\n`.
- **The command runs** under `/bin/bash -c` with `cwd` = root, plus `HARNESS_EVENT` and `HARNESS_ROOT`.

`harness/internal/tools`: `Env.Hooks *hooks.Runner`. `Registry.run` calls `pre_tool_use` before the tool and `post_tool_use` after it.

`harness/internal/agent`: `Config.OnPrompt func(ctx, input) (block bool, reason, context string)` and `Config.OnStop func(ctx) string`. Stop feedback is appended to history and emitted as a `steer` event with `Data["source"] = "stop_hook"`.

Provided in `support/`: `cmd/harness` (loads hooks, `--hooks FILE`, wires `session_start`/`OnPrompt`/`OnStop`) and `cmd/hooklab`. The lesson directory has `hooks/`, with the three scripts and `hooks.json`.

## Build it

1. **Load and match.** Tests: `go test ./internal/hooks -run 'TestLoad|TestMatch'`.
2. **Run one hook.** Tests: `-run 'TestExit2|TestPayload|TestJSON|TestPlain|TestOther|TestTimeout'`.
   - Same subprocess care as lesson 05: `Setpgid`, `cmd.Cancel` killing the group, `WaitDelay`.
   - Only parse stdout as JSON when it starts with `{`.
3. **Combine.** Test: `-run TestMultiple`.
4. **Tool integration.** Tests: `go test ./internal/tools -run 'TestPreToolUse|TestPostToolUse'`.
5. **Agent integration.** Tests: `go test ./internal/agent -run 'TestPromptHook|TestStopHook'`. A blocked prompt must not reach the model *or* the history.

## Run it

```sh
go test ./...
export HOOKS_DIR=<lesson>/hooks
go run ./cmd/harness --root /tmp/textkit --hooks $HOOKS_DIR/hooks.json
> add a Palindrome function (and watch <hook_output> after each edit)
```

## Demo

`go run ./cmd/hooklab -hooks-dir <lesson>/hooks`: 16 tasks × 3 repeats × {Haiku 5.5, GLM 5.3 Flash} × {without, with hooks} = 192 runs, about $0.50.

```
CONFIG                    PASS   RATE ± SD  MEAN COST  MEAN TURNS  MEAN TIME  ERRORS
claude-haiku-5.5          48/48  100% ± 0   $0.0017    5.2         16s        0
claude-haiku-5.5 + hooks  47/48  98% ± 4    $0.0019    5.5         17s        0
glm-5.3-flash             46/48  96% ± 7    $0.0033    7.8         32s        0
glm-5.3-flash + hooks     47/48  98% ± 4    $0.0028    7.8         27s        0

stop-hook interventions (the hook sent the model back because go test failed): 0 and 0
  fail claude-haiku-5.5 + hooks/07-refactor-rename rep 1: vet: ./hidden_test.go:11:8: undefined: User
  fail glm-5.3-flash/10-follow-instructions rep 1: gofmt needed:
  fail glm-5.3-flash/15-repetitive-file-fix rep 1: pattern ./...: directory prefix . does not contain main module…
  fail glm-5.3-flash + hooks/11-semver-compare rep 0: --- FAIL: TestHiddenCompareInvalid
```

What it shows:

- **No measurable effect on this suite.** Every difference is within one standard deviation. The four failures are scattered: 0, 1, 2 and 1 across the four configurations, each on a different task. Lesson 07's warning applies: a saturated suite can't show an improvement.
- **The hooks fire, but these models rarely need them.** The stop hook never sent a model back, because every model that finished had already run its visible tests successfully. The lint hook does trigger: a manual run with an unformatted file shows `<hook_output>gofmt: these files need formatting…` after every edit.
- **The one failure the lint hook targets happened only without it.** GLM's task 10 failed on "gofmt needed". With hooks, GLM had no formatting failures. That's one event, not evidence, but it's the mechanism working as designed.
- **The failures hooks can't catch** are hidden-test failures (wrong behavior that passes visible tests: semver edge cases) and a model breaking the module itself. A test-on-stop hook only knows the tests the repository has.

When hooks pay off: weaker models, longer tasks, and repositories whose own tests cover what matters. That's what Aider's numbers measured: lint and test feedback on SWE-bench, where the first two attempts produced about 90% of the resolved solutions.

## Exercises

1. **Diagnostics with context.** Make `lint.sh` print each `go vet` error with its enclosing function (`go/ast`), as Aider does, and compare turns-to-fix on a task seeded with a vet error.
2. **A prompt hook.** `user_prompt_submit` that adds the current branch and `git status --short` as context. Does the model use it?
3. **An LLM hook.** A `pre_tool_use` handler that asks the small model "is this command consistent with the user's last request?" and blocks if not (Claude Code's `prompt` handler type). Run it against lesson 09's red-team cases.

## Further reading

- Claude Code hooks: https://code.claude.com/docs/en/hooks
- pi extensions: `packages/coding-agent/docs/extensions.md` in https://github.com/earendil-works/pi
- Aider lint and test: https://aider.chat/docs/usage/lint-test.html, https://aider.chat/2024/05/22/linting.html
