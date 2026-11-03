# Design: Build-Your-Own Coding Agent Harness Tutorial

## Intent

The reader is a senior developer who wants to understand how coding-agent harnesses (opencode, pi, Codex CLI, Claude Code) work by building one. The goal is understanding the machinery and the design tradeoffs, not shipping a competitor. The finished harness must be able to do real coding work.

The emphasis is on *how harnesses work and why they are designed that way*. Go syntax and general programming are not taught.

**The reader builds the harness themselves.** The tutorial does not hand over a finished harness to read; each lesson guides the reader to implement the lesson's capability in their own module. The lessons supply the design explanation, the contract (exact types/signatures), the tests that prove it works, the supporting code that isn't about harness concepts, and a reference solution to compare against when stuck. The reader does not use agents to do the lessons.

## Goals

- A guided, sequential tutorial where each lesson adds one harness capability.
- Every lesson ends with **fully working code written by the reader** (proven by the lesson's provided tests) and a **demo** (stress test, benchmark, TUI, or visualization) that shows what the lesson built and, where it makes sense, how it ties into earlier lessons.
- Ground every design discussion in how the real harnesses do it, with concrete details (thresholds, formats, prompts) from `research-notes.md`. Where harnesses disagree, show the alternatives and their tradeoffs.
- Cover model selection, including how a model gets chosen per task and an experiment with automatic routing.

## Non-goals

- Teaching Go.
- Feature parity with any real harness.
- Multi-provider wire adapters beyond what's needed (everything goes through Vercel AI Gateway).
- Production hardening (auth flows, telemetry, auto-update, Windows support).

## Anti-goals (failure even if it technically works)

- Lessons that are code dumps without explaining *why* the harness is built that way.
- A TUI that consumes the tutorial: UI complexity must stay small.
- Claims about real harnesses that are not backed by the research notes (or clearly marked as inference).
- Code that only works in the lesson where it was introduced and breaks later.

## Constraints (decided by the user)

- **Language:** Go (Go 1.26.5 installed locally). Ruby was the fallback; not used.
- **Model access:** Vercel AI Gateway (`AI_GATEWAY_API_KEY` is set). Model selection must be switchable, and dynamic selection is a topic.
- **Layout:** one directory per lesson containing the lesson markdown file(s) and any supplemental code/resources.
- **UI:** full-screen TUI is wanted but must not add extreme complexity.
- **Topic list:** approved as drafted (see lesson plan).
- **Lesson contents (user choice):** lesson text + provided tests the reader makes pass + supporting files + a reference solution in `solution/`. Done = tests pass and demo runs.
- **Safety before lesson 9:** approved interim guard. Lessons 4–8 confine file tools to a sample project directory (paths resolved and checked against the workspace root), the interactive REPL/TUI asks y/n before every bash command, and the eval suite only runs in throwaway temp directories. Lesson 9 replaces this guard with the real permission system and sandbox.
- **API budget:** no hard limit. Comparison demos (lessons 7, 12, 16, 17, 19) should present *measured* effects: multiple repeats per configuration (target ≥3 per task), with variance shown, so conclusions are not single-run noise. Each lesson states the approximate cost of running its demo.

## Decisions made by me (the author), open to override

1. **Hand-rolled HTTP + SSE client against the gateway's OpenAI-compatible Chat Completions endpoint**, not an SDK. Reason: the reader learns the wire protocol, and gateway extensions (`provider_metadata`, `reasoning_details`, `cache_control`, `providerOptions`) are easier with raw JSON. Chat Completions is chosen over Responses/Anthropic Messages because it works for every model in the gateway catalog. `openai-go/v3` is mentioned as an alternative.
2. **Neutral internal message model** (pi-style: user / assistant with text, thinking, tool-call blocks / tool result), translated to Chat Completions at the edge. Explains why harnesses normalize.
3. **Core/UI separation via an event stream** from lesson 2 onward. The core emits typed events; a line REPL renders them first; a Bubble Tea TUI is added in lesson 8 as a second subscriber. Same pattern as Codex (submission/event queues), opencode (server + SSE), pi (agent events).
4. **Code layout:** the reader keeps one growing Go module of their own (module path `harness`, any location). Each lesson directory provides `tests/` and `support/` trees that mirror module paths and are copied into the reader's module, plus `solution/`: the complete reference module as of the end of that lesson (includes the same tests and support files). `diff -r NN/solution NN+1/solution` shows exactly what a lesson adds. Demos are support code (`support/cmd/<demo>/`) that import the reader's packages, so the reader's own implementation drives the demo.
5. **Offline-testable:** the support code includes a scripted fake model (an `httptest` server replaying canned SSE responses) so `go test ./...` passes with no network and no cost. Demos that need a real model say so and use cheap models by default.
6. **Evaluation comes early** (lesson 7) so later lessons can measure their effect (caching savings, compaction survival, routing cost/quality) with the same task suite instead of anecdotes.
7. **Default models:** main `anthropic/claude-sonnet-5.5`, small/background `anthropic/claude-haiku-5.5`. Configurable via flag/env/config; any `tool-use`-tagged gateway model should work.
8. **Visualizations:** terminal output (tables, Unicode bar charts, sparklines) by default; self-contained HTML report files (no external assets) where a timeline or chart is clearer.
9. **Dependencies kept minimal:** standard library plus Bubble Tea/Lip Gloss (TUI), `mvdan.cc/sh/v3` (shell parsing for permissions). Token counts are estimated (bytes/4) and calibrated against provider-reported usage, matching what Codex/opencode do; no tokenizer dependency.
10. **Sandboxing:** permission rules + macOS Seatbelt (`/usr/bin/sandbox-exec`) for the bash tool, since the reader is on macOS. Linux bubblewrap is described, not implemented.

## Directory layout

```
harness/
  README.md                 # index, prerequisites, how to use the tutorial
  DESIGN.md                 # this file
  research-notes.md         # source-backed facts about real harnesses
  00-orientation/
    README.md
    tests/ support/ solution/
  01-talking-to-the-model/
    README.md               # the lesson
    tests/                  # test files to copy into your module (mirror module paths)
    support/                # non-concept code/files to copy in: fake model, demo cmds, fixtures
    solution/               # reference module as of the end of this lesson
  ...
  20-where-to-go-next/
    README.md
```

Each lesson `README.md` follows the same structure:

1. **What you'll build** (one paragraph) and **why it matters** in a harness.
2. **How the real harnesses do it**: comparison of pi / opencode / Codex / Claude Code (and Aider where relevant), with tradeoffs.
3. **Design for our harness**: the decisions taken and why; diagrams (ASCII or Mermaid) where useful.
4. **The contract**: exact package paths, types, and signatures the provided tests compile against.
5. **Build it**: ordered steps for the reader to implement, each tied to the tests it turns green; hints and short snippets only for the parts that are tricky or non-obvious (wire formats, algorithms). Not a full code listing; the full code is in `solution/`.
6. **Run it**: copy `tests/` and `support/`, run the tests, run the harness.
7. **Demo**: what to run, what you should see, what it proves, how it connects to earlier lessons.
8. **Exercises / experiments** (optional extensions, no solutions required).
9. **Further reading**: links from research notes.

## Package layout of the final harness

```
cmd/harness/            main binary (REPL, TUI, exec modes)
cmd/<demo-name>/        demo programs per lesson
internal/llm/           neutral types, gateway client, SSE parser, model catalog, cost
internal/agent/         the loop, events, steering, cancellation
internal/tools/         registry, schema, validation, truncation, built-in tools
internal/prompt/        system prompt assembly, instruction-file discovery, reminders
internal/permission/    rules, approval flow, shell command parsing, sandbox
internal/session/       JSONL session log, resume, fork, snapshots/undo
internal/ctxmgr/        token accounting, pruning, compaction
internal/commands/      slash commands, prompt templates, skills
internal/memory/        cross-session memory
internal/subagent/      task tool, child agents
internal/hooks/         hook events and external command handlers
internal/mcp/           MCP client (stdio JSON-RPC)
internal/route/         model roles and routing
internal/eval/          task suite runner, metrics, reports
internal/ui/repl/       line-based front-end
internal/ui/tui/        Bubble Tea front-end
internal/llm/fake/      scripted fake model for tests
```

Packages appear only in the lesson that introduces them.

## Lesson plan

Each entry: concepts → code added → demo.

**00 Orientation.** What a harness is (model + loop + tools + context + policy). Anatomy diagram. Survey table of pi / opencode / Codex / Claude Code / Aider philosophies (pi minimalism vs Claude Code breadth). Setup: Go, API key, gateway endpoints. → Code: small catalog explorer that fetches `/v1/models`. → Demo: table of tool-capable models sorted by price with context window, cache support, reasoning support; this data later drives cost accounting and routing.

**01 Talking to the model.** Chat Completions request/response, SSE parsing, streaming deltas, neutral message types, usage and cost (`provider_metadata.gateway.cost`), errors and retry with backoff/`retry-after` (opencode's numbers). → Code: `internal/llm` client, minimal chat REPL. → Demo: streaming benchmark of 3–4 models on the same prompt: TTFT, tokens/s, cost, as a table + bars.

**02 The agent loop.** Tool-call protocol, the loop (sample → run tools → append results → repeat), stop reasons (`length` → fail tool calls), orphaned tool calls, max steps (opencode's "MAXIMUM STEPS REACHED" pattern), event stream design, cancellation via `context.Context`. First tool: `read_file`. Fake scripted model introduced. → Demo: trace recorder that writes every event to JSONL plus an HTML timeline (model spans, tool spans, tokens) of a real multi-step run.

**03 Tool design.** Registry, JSON Schema from Go structs, argument validation and actionable errors returned as tool results (self-correction), output truncation (head/tail, full output saved to disk), descriptions as prompts, LLM-facing vs UI-facing results. Tools: `read` (line numbers, offset/limit), `glob`, `grep`, `ls`. Parallel execution with a read/write lock (Codex). → Demo: benchmark parallel vs sequential tool execution on a multi-file exploration; a scripted test showing the model recovering from a validation error.

**04 Editing files.** Edit formats (whole, search/replace, apply_patch, udiff) with Aider's findings; exact-match uniqueness; read-before-edit tracking; fallback matching cascade (exact → trailing ws → trimmed → unicode normalize, with disproportionate-match guard); per-file locks; CRLF/BOM preservation; returning diffs. Tools: `edit`, `write`. → Demo: edit stress test over a corpus of drifted edit requests (whitespace, indentation, smart quotes, ambiguous matches): success and false-positive rate per matching strategy, as a table/chart.

**05 Running commands.** `bash` tool: process groups, timeouts, kill on cancel, head/tail output buffer, cwd persistence, environment, exit codes in results, background processes (brief). → Demo: shell torture test: huge output, hung command, child processes that ignore SIGTERM, interleaved stderr; shows harness memory and cleanup (no orphans).

**06 System prompt and project instructions.** Prompt anatomy (identity, tool guidance, environment block), per-model prompt variants (opencode), AGENTS.md/CLAUDE.md discovery (root→cwd, first match per dir, byte budget), ordering for cache stability. At the end of this lesson the harness is a usable coding agent. → Demo: `/context`-style inspector: token breakdown by prompt section, tools, history (bar chart); first real end-to-end task ("add a function with tests to this sample repo").

**07 Headless mode and evaluation.** `exec` mode (non-interactive, JSONL events), task suite of ~10 small verifiable Go tasks (bug fix, feature, refactor) each with hidden tests, runner in temp dirs, metrics (pass rate, turns, tokens, cost, wall time). → Demo: eval report (terminal summary + HTML) comparing two models. This runner is reused by later demos.

**08 Terminal UI.** Bubble Tea front-end subscribing to the event stream: streaming text, tool-call cells, status bar (model, tokens, cost), cancel, input queueing/steering. Why core/UI separation matters (Codex app-server, opencode server). → Demo: the TUI itself, plus replaying a recorded lesson-02 trace into the TUI (proves the UI is purely a function of events).

**09 Permissions and safety.** Threat model; approval modes (ask / accept-edits / plan(read-only) / yolo); rules (allow/ask/deny, deny-first, patterns); parsing compound shell commands with `mvdan.cc/sh`; approval round-trip through the event bus (loop pauses); "always allow" learning; doom-loop detection; macOS Seatbelt sandbox with writable roots and network off. pi's "security theater" argument presented fairly. → Demo: red-team matrix: ~25 risky commands (`rm -rf` variants, `sh -c`, `/bin/rm`, redirects to `~/.ssh`, `curl | sh`, network exfil) showing which layer (rules, parser, sandbox) catches each; TUI permission dialog.

**10 Sessions.** Append-only JSONL log, crash safety, resume (rebuild context from log), fork/branch (pi's tree), session list; undo via shadow git snapshots (opencode). → Demo: `kill -9` mid-turn and resume; session tree visualization; undo an agent's edits.

**11 Context budget and pruning.** Token accounting (estimate + calibration with reported usage), context window from catalog, usable = window − reserve, tool-output pruning (clear old results, keep recent), live context meter. → Demo: long synthetic session chart: context size over turns with and without pruning, hitting/avoiding the limit.

**12 Prompt caching.** Prefix caching mechanics, why harness design bends around it (stable tools, appended reminders instead of system edits, deterministic ordering, no mid-session model switch), gateway `cache_control`/`caching:"auto"`, session affinity, measuring hit rate. → Demo: A/B experiment using the eval suite: cache-friendly vs cache-hostile configuration (timestamp in system prompt, shuffled tools), cost and latency comparison.

**13 Compaction.** When to trigger (thresholds from Codex/pi/opencode), serializing history for the summarizer, structured summary template, preserving the recent tail without splitting tool call/result pairs, re-injecting instructions and recent files, iterative summaries, overflow-error retry, cache-friendly summarize call (Claude Code). `/compact`. → Demo: a task forced through a deliberately tiny context window (e.g. 30k) that only completes because of compaction; timeline marking compaction events and what survived.

**14 Slash commands, templates, and skills.** Command parsing and dispatch (built-ins: `/model`, `/compact`, `/clear`, `/context`, `/undo`, `/resume`), user prompt templates (`$ARGUMENTS`, `$1`, `!\`cmd\``), skills with progressive disclosure. → Demo: token cost of skill listing vs inlining all skill bodies; command palette in the TUI.

**15 Memory.** Kinds of memory: instruction files (static), notes files (PLAN.md/TODO.md, pi), auto memory (index + topic files, Claude Code), memory tool. What to store, when to write, recall at session start. → Demo: two-session test: session 1 learns a project convention, session 2 (fresh context) follows it; memory browser.

**16 Subagents.** Task tool, isolated context, restricted tools, per-agent model, summary return, depth limit, parallel subagents; pi's critique. → Demo: same exploration task inline vs via subagents: parent context size, cost, wall time; parallel subagent timeline.

**17 Hooks and the feedback loop.** Hook events (pre/post tool, prompt submit, stop, session start), external command handlers with JSON stdin, exit-code-2 block, input rewriting; lint/test feedback loop (Aider, opencode LSP diagnostics) via `gofmt`/`go vet`/`go test` hooks. → Demo: eval suite with and without the post-edit lint/test hook: pass rate and turns.

**18 MCP.** Protocol basics (JSON-RPC over stdio: initialize, tools/list, tools/call), hand-rolled client, tool naming `mcp__server__tool`, context overhead, deferred loading/tool search. Supplemental: a tiny Go MCP server written for the lesson. → Demo: context overhead measurement of loaded vs deferred MCP tools; agent using the MCP tool end to end.

**19 Model selection and routing.** Roles (main, small/background for titles and summaries, subagent model), `/model` switching and its costs (cache loss, dropping thinking blocks), cross-model history transform, gateway fallbacks, a router: heuristic + escalate-on-failure (SWE-Router style). → Demo: eval suite under fixed-strong, fixed-cheap, and router: cost vs pass-rate chart.

**20 Where to go next.** What was left out (LSP, web fetch, images, plan mode, client/server split, containers, ACP), and a reading guide mapping each lesson's concepts to files in pi, opencode, and Codex.

## Verification standard (for the builder)

- Every lesson's `solution/` builds with `go build ./...` and passes `go vet ./...` and `go test ./...` offline.
- `tests/` and `support/` are byte-identical to the corresponding files in `solution/`.
- Taking lesson N−1's `solution/`, copying in lesson N's `tests/` and `support/`, must compile except for the symbols the lesson asks the reader to write (the tests fail, not the support code).
- Every demo is run at least once against `solution/`; demos needing the network are run against the gateway with cheap models where possible.
- Lesson N's solution is lesson N−1's solution plus the lesson's changes; regressions in earlier behavior are caught by carried-forward tests.

## Research policy

Claims about real harnesses must be backed by `research-notes.md` or marked as inference. Where a lesson needs more than the notes contain (further-reading links, the lesson-20 reading guide, thin areas such as background processes, memory, MCP deferred loading), the builder does additional source research (via web-search subagents) and appends the findings, with sources, to `research-notes.md` before writing the lesson.

## Wire-protocol risk

Streaming tool-call deltas, `stream_options.include_usage`, passing `reasoning_details` back, and `cache_control` on messages are not fully documented for the gateway's Chat Completions endpoint. The builder verifies these with a tiny live probe against a cheap model before writing lesson 1. If something fails, working across models wins over protocol purity (the user asked for model selection): the lesson documents the gap and works around it (e.g. non-streaming for the affected feature, or `providerOptions.gateway.caching: "auto"` instead of manual markers), rather than switching the whole harness to the Anthropic Messages endpoint. *(Author's call.)*

## Left to the builder

- Exact Go APIs, type names, file splits within packages.
- Exact task-suite contents for lesson 7 (must be small, verifiable, and Go-based).
- Exact visual style of terminal charts and HTML reports.
- Which cheap models to use for each demo.
- Length of each lesson (target: thorough on concepts, terse on boilerplate).
