# Coding Agent Harness Tutorial Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a 21-lesson tutorial (`00-…` through `20-…`) that guides the reader to build their own Go coding-agent harness. Each lesson directory holds the lesson README, the tests the reader makes pass, supporting code (fake model, demos, fixtures), and a reference solution.

**Architecture:** A Go harness that talks to Vercel AI Gateway's Chat Completions endpoint through a hand-rolled HTTP/SSE client, uses provider-neutral message types, and runs an agent loop that emits typed events consumed by a REPL (later also a Bubble Tea TUI). The author builds each lesson's `solution/` (previous lesson's `solution/` copied forward plus the lesson's changes), then extracts `tests/` and `support/` from it and writes the README as a guided build for the reader.

**Tech Stack:** Go 1.26 (stdlib-first), Bubble Tea + Lip Gloss (lesson 8+), `mvdan.cc/sh/v3` (lesson 9+), macOS `/usr/bin/sandbox-exec`.

**Spec:** `/Users/jlambert/tmp/harness/DESIGN.md` (facts: `/Users/jlambert/tmp/harness/research-notes.md`)

## Global Constraints

- Root: `/Users/jlambert/tmp/harness`. Lesson dirs: `NN-kebab-title/` with `README.md`, `tests/`, `support/`, `solution/`.
- Each `solution/` is its own Go module, module path `harness`, `go 1.26`.
- `solution/` for lesson N starts as `cp -R` of lesson N−1's `solution/`; earlier tests are carried forward and must still pass.
- Every solution: `go build ./... && go vet ./... && go test ./...` passes **offline** (fake model; no network in tests).
- **The reader writes the harness.** READMEs guide implementation; they do not paste the solution. Snippets only for wire formats, algorithms, and non-obvious code. Everything else lives in `solution/`.
- Gateway base URL `https://ai-gateway.vercel.sh/v1`; auth `Authorization: Bearer $AI_GATEWAY_API_KEY`.
- Default models: main `anthropic/claude-sonnet-5.5`, small `anthropic/claude-haiku-5.5`; overridable by `--model` / `HARNESS_MODEL` / `HARNESS_SMALL_MODEL`.
- Dependencies: stdlib only until lesson 8; then Bubble Tea/Lip Gloss; lesson 9 adds `mvdan.cc/sh/v3`. No tokenizer library; token estimate = bytes/4.
- Lessons 4–8 interim guard: file tools confined to the workspace root (resolve symlinks, reject escapes); interactive mode asks y/n before every bash command; eval runs only in temp dirs. Lesson 9 replaces it.
- Comparison demos (lessons 7, 12, 16, 17, 19): ≥3 repeats per task per configuration; show mean and spread; state approximate cost.
- README structure per lesson (from spec): What you'll build / How the real harnesses do it / Design for our harness / The contract / Build it / Run it / Demo / Exercises / Further reading.
- Claims about real harnesses come from `research-notes.md` or are marked as inference. Missing facts → research via web-search subagent first, append to `research-notes.md` with sources.
- Code comments: only where code is complex or opaque; no narration comments.
- Prose: follow the user's word-choice rules in ~/.claude/CLAUDE.md; no litotes, no irony.
- Not a git repository: no commit steps. The lesson snapshots are the checkpoints.

## Review Focus

1. **Streamed tool-call args split across chunks, with two parallel calls interleaved by `index`** — expected: each call's JSON reassembled correctly. Pinned by `TestStreamInterleavedToolCalls` (Task 2).
2. **Ctrl-C mid-stream or mid-tool** — expected: history ends with a result for every tool call (synthetic "aborted") so the next request is valid. Pinned by `TestCancelLeavesValidHistory` (Task 3) and `TestBashCancelKillsGroup` (Task 6).
3. **Paths escaping the workspace via `..` or a symlink** — expected: rejected with an actionable error. Pinned by `TestWorkspaceRejectsSymlinkEscape` (Task 5).
4. **Binary files, non-UTF-8 output, and multi-megabyte output** — expected: truncation never splits a rune, memory stays bounded, binaries refused. Pinned by `TestTruncateRuneSafe` (Task 4) and `TestBashHugeOutputBounded` (Task 6).
5. **Gateway 429 with `retry-after`, 5xx, 402, and context-overflow errors** — expected: 429/5xx retried with backoff, 402 surfaced immediately, overflow triggers compaction once. Pinned by `TestRetryHonorsRetryAfter` (Task 2) and `TestOverflowCompactsAndRetries` (Task 14).

## Per-lesson workflow (applies to every task below)

Each task's steps describe building `solution/`. In addition, every task ends with these steps:

- [ ] **Extract `tests/`:** copy the lesson's new and changed `_test.go` files from `solution/` into `NN/tests/` at the same relative paths. Carried-forward tests from earlier lessons are not repeated.
- [ ] **Extract `support/`:** copy into `NN/support/` the files that are not the lesson's concept: demo `cmd/`s, `llm/fake`, fixtures, sample repos, eval tasks, HTML templates, sandbox profiles only if the lesson says "provided". Rule: if a file teaches the lesson's concept, it is the reader's to write and is not in `support/`.
- [ ] **Reader-path check:** in a temp dir, `cp -R (N-1)/solution/ .`, copy in `NN/tests/` and `NN/support/`, run `go vet ./...`. Expected: the only errors are undefined symbols listed in the README's contract section (support code must not reference anything else new). Then copy `NN/solution/` over it and run `go test ./...` → ok.
- [ ] **Write the README as a guided build:** "The contract" lists every exported type/function the tests touch, with signatures. "Build it" is a numbered sequence; each step names the tests it turns green (`go test ./internal/llm -run TestStreamText`), explains the harness concept behind it, and gives a hint; snippets only where the plan step says the algorithm/format is fixed. Mention `solution/` as the fallback, with the specific file to look at.
- [ ] **For lesson 00 only:** the reader creates their module (`go mod init harness`) — the README covers this.

In task file lists below, `code/` means `solution/`.

---

## Shared Interfaces (defined in Tasks 2–4, used everywhere after)

These names are fixed for all lessons. Later lessons may add fields/methods; they do not rename.

```go
// package llm
type Role string                      // "user", "assistant", "tool"
type StopReason string                // "stop", "length", "tool_use", "error", "aborted"
type ToolCall struct { ID, Name string; Args json.RawMessage }
type ToolResult struct { CallID, Name, Content string; IsError bool }
type Thinking struct { Text string; Raw json.RawMessage } // Raw = gateway reasoning_details, replayed verbatim
type Block struct { Text string; Thinking *Thinking; ToolCall *ToolCall } // exactly one set
type Message struct {
    Role Role; Blocks []Block; Result *ToolResult  // Result set iff Role == "tool"
    Model string; Usage *Usage; Stop StopReason; Err string
}
type Usage struct { Input, Output, CacheRead, CacheWrite, Reasoning int; CostUSD float64 }
type ToolSpec struct { Name, Description string; Schema json.RawMessage }
type Request struct {
    Model, System string; Messages []Message; Tools []ToolSpec
    MaxTokens int; Reasoning string /* effort or "" */; Extra map[string]any
}
type StreamEventType string // "text_delta","thinking_delta","toolcall_start","toolcall_delta","done"
type StreamEvent struct { Type StreamEventType; Text string; Index int; Call *ToolCall; Message *Message }
type Client interface { Stream(ctx context.Context, req Request) <-chan StreamEvent }
// Contract: Stream never returns an error; failures arrive as the final "done" event whose Message.Stop == "error" (pi's contract).

// package llm/fake
type Turn struct { Text, Thinking string; Calls []llm.ToolCall; Usage llm.Usage; Status int; Header http.Header; ChunkSize int }
func NewServer(t testing.TB, turns ...Turn) (baseURL string, requests func() []json.RawMessage)

// package agent
type EventKind string
type Event struct {
    Kind EventKind; Turn int; Text string
    Call *llm.ToolCall; Result *llm.ToolResult; Message *llm.Message; Usage *llm.Usage
    Data map[string]any // lesson-specific payloads (permission, compaction, …)
    Time time.Time
}
// Kinds: agent_start, turn_start, text_delta, thinking_delta, toolcall_start, tool_start, tool_end, message_end, turn_end, agent_end, error
type Sink func(Event)
type Config struct { Client llm.Client; Model, System string; Tools *tools.Registry; MaxSteps int }
func New(cfg Config) *Agent
func (a *Agent) Run(ctx context.Context, input string, sink Sink) error
func (a *Agent) History() []llm.Message

// package tools
type Env struct { Root string } // grows: Approver, ReadTracker, …
type Result struct { Content string; Details any; IsError bool } // Content → model, Details → UI
type Tool interface { Spec() llm.ToolSpec; ReadOnly() bool; Run(ctx context.Context, env *Env, args json.RawMessage) Result }
func NewRegistry(env *Env) *Registry
func (r *Registry) Register(t Tool)
func (r *Registry) Specs() []llm.ToolSpec            // sorted by name (cache stability)
func (r *Registry) Execute(ctx context.Context, calls []llm.ToolCall, onStart, onEnd func(llm.ToolCall, llm.ToolResult)) []llm.ToolResult // results in call order
func SchemaFor[T any]() json.RawMessage                 // from struct tags `json:"x,omitempty" desc:"…"`
func Decode[T any](args json.RawMessage) (T, error)    // unknown fields + missing required → descriptive error
```

---

### Task 1: Wire probe, repo skeleton, lesson 00 (orientation + catalog explorer)

**Files:**
- Create: `README.md` (tutorial index, prerequisites, how to use, lesson table — fill titles now, links to all 21 dirs)
- Create: `00-orientation/README.md`, `00-orientation/code/go.mod`, `00-orientation/code/internal/llm/catalog.go`, `00-orientation/code/internal/llm/catalog_test.go`, `00-orientation/code/cmd/models/main.go`
- Create: `docs/probe/probe.sh` (kept as a resource; README references it)
- Modify: `research-notes.md` (append probe results)

**Interfaces:**
- Produces: `type Pricing struct{ Input, Output, CacheRead, CacheWrite float64 }` (USD per token, parsed from strings); `type ModelInfo struct{ ID string; ContextWindow, MaxTokens int; Tags []string; Pricing Pricing }`; `func (m ModelInfo) Has(tag string) bool`; `func FetchCatalog(ctx context.Context, baseURL string) ([]ModelInfo, error)`.

- [ ] **Step 1: Live probe.** Write `docs/probe/probe.sh` using curl against `anthropic/claude-haiku-5.5` and `openai/gpt-5.4-nano` to check: (a) streaming tool-call deltas shape, (b) `stream_options:{include_usage:true}` yields a usage chunk, (c) `provider_metadata.gateway.cost` presence in stream, (d) `usage.prompt_tokens_details.cached_tokens` after two identical ~3k-token requests with `providerOptions.gateway.caching:"auto"`, (e) `reasoning_details` returned with `reasoning:{effort:"low"}` and accepted when replayed in a follow-up tool turn, (f) confirm both default model IDs exist in `/v1/models`. Run it; append a "Gateway probe results (2026-10-20)" section to `research-notes.md` with raw excerpts. Expected: each item marked works / fails / shape differs. Any failure → apply the spec's "Wire-protocol risk" rule and note the workaround in the probe section.
- [ ] **Step 2: Failing test** `TestParseCatalog` in `catalog_test.go`: parses a fixture JSON (two entries copied from the real response shape in research notes) and asserts `ID=="anthropic/claude-sonnet-5"`, `ContextWindow==1000000`, `Pricing.Input==0.000002`, `Has("tool-use")==true`. Run `go test ./...` → FAIL (undefined).
- [ ] **Step 3: Implement** catalog parsing and `FetchCatalog` (unauthenticated GET `/models`). Run test → PASS.
- [ ] **Step 4: Demo** `cmd/models`: flags `--tag tool-use` (default), `--sort price|context`, `--limit 25`; prints table ID / context / $in per M / $out per M / caching (implicit/explicit/none) / reasoning. Run `go run ./cmd/models --limit 15`. Expected: table of real models. Paste a trimmed real output into the README demo section (as "what you should see").
- [ ] **Step 5: README** for lesson 00: harness anatomy diagram (model, loop, tools, context, policy, UI, persistence), survey table of pi/opencode/Codex/Claude Code/Aider philosophies (from research notes), setup, the three gateway APIs and why we use Chat Completions, how lessons are structured, `diff -r` tip. Root `README.md` index.
- [ ] **Step 6: Verify** `cd 00-orientation/code && go vet ./... && go test ./...` → ok.

### Task 2: Lesson 01 — talking to the model

**Files:** `01-talking-to-the-model/` — `code/internal/llm/{types.go,client.go,sse.go,wire.go,retry.go,cost.go}`, tests `client_test.go`, `code/internal/llm/fake/fake.go`, `code/cmd/harness/main.go` (chat REPL, no tools), `code/cmd/bench/main.go`, `README.md`.

**Interfaces:**
- Consumes: catalog from Task 1.
- Produces: all `llm` and `llm/fake` types in Shared Interfaces; `func NewGateway(baseURL, apiKey string, opts ...Option) Client`; `func WithRetry(max int, initial, cap time.Duration) Option`; `func CostFromUsage(u Usage, p Pricing) float64` (fallback when gateway cost absent).

- [ ] **Step 1: Failing tests** in `client_test.go`:
  - `TestStreamText`: fake turn `Text:"hello world", ChunkSize:3` → collected `text_delta` texts join to `"hello world"`, final `done` message `Stop=="stop"`, `Usage.Input>0`.
  - `TestStreamInterleavedToolCalls`: fake emits two tool calls whose arg fragments alternate by index across chunks → final message has 2 `ToolCall` blocks with args `{"path":"a.go"}` and `{"path":"b.go"}`, `Stop=="tool_use"`.
  - `TestRetryHonorsRetryAfter`: fake turn 1 `Status:429, Header: retry-after: 1`, turn 2 OK → success; elapsed ≥1s; 2 requests made.
  - `TestNoRetryOn402`: `Status:402` → single request; done `Stop=="error"`, `Err` contains `402`.
  - `TestWireRoundTrip`: a `Request` with system, user, assistant(thinking Raw + tool call), tool result → JSON body has `messages[0].role=="system"`, assistant `tool_calls[0].function.arguments` string, `reasoning_details` equals Raw verbatim, tool message `tool_call_id`.
- [ ] **Step 2:** Run `go test ./internal/llm/...` → FAIL.
- [ ] **Step 3: Implement** `fake.NewServer` (emits Chat Completions SSE chunks, final usage chunk, `data: [DONE]`; records request bodies), SSE parser (handles multi-line `data:`, comments, `[DONE]`), wire mapping, streaming accumulation keyed by tool-call `index`, retry (opencode values: initial 2s, ×2, jitter 0.25, cap 30s, max 5; honor `retry-after`/`retry-after-ms`; retry 429/5xx/network before first byte only), cost (prefer `provider_metadata.gateway.cost`, else `CostFromUsage`). Request body sets `stream:true`, `stream_options.include_usage:true` (subject to probe).
- [ ] **Step 4:** Run tests → PASS.
- [ ] **Step 5:** `cmd/harness`: REPL keeps neutral history, streams text, prints `[in/out tokens, $cost]` per reply; `--model`.
- [ ] **Step 6: Demo** `cmd/bench`: same prompt to 4 models (default: sonnet-5.5, haiku-5.5, gpt-5.4-nano, gemini-3.5-flash-lite), N=3 runs each; table TTFT p50, tokens/s, cost, plus Unicode bars. Run live; paste output into README.
- [ ] **Step 7: README** — request/response anatomy, SSE, neutral types and why (pi's cross-provider handoff), never-throw stream contract, retries, cost sources.
- [ ] **Step 8: Verify** vet + test offline.

### Task 3: Lesson 02 — the agent loop

**Files:** `02-the-agent-loop/code/internal/agent/{agent.go,events.go,history.go}`, `agent_test.go`; `code/internal/tools/{registry.go,schema.go,decode.go,read_file.go}`, tests; `code/internal/trace/{recorder.go,html.go}`; `code/internal/ui/repl/repl.go`; `code/cmd/trace-view/main.go`; README.

**Interfaces:**
- Consumes: `llm.Client`, `fake.NewServer`.
- Produces: `agent`, `tools` types in Shared Interfaces; `trace.NewRecorder(w io.Writer) agent.Sink` (JSONL, one Event per line); `trace.RenderHTML(events []agent.Event, w io.Writer) error`; `repl.Run(ctx, a *agent.Agent, in io.Reader, out io.Writer) error`.

- [ ] **Step 1: Failing tests** (`agent_test.go`):
  - `TestLoopRunsToolsUntilStop`: fake turns [call read_file(path=x.txt)] → [text "done"]; history = user, assistant(call), tool(result containing file contents), assistant("done"); events include `tool_start`,`tool_end`,`agent_end` in order.
  - `TestMaxStepsInjectsFinalPrompt`: `MaxSteps:2`, fake always returns a tool call → on the final step the request has no tools and the last message contains `MAXIMUM STEPS REACHED`; Run returns nil.
  - `TestLengthStopFailsToolCalls`: fake returns a tool call with `Stop` forced to `length` → tool not executed; tool result `IsError` with text containing `truncated`.
  - `TestCancelLeavesValidHistory`: cancel ctx while a slow tool runs → every assistant tool call in History has a matching tool result; aborted ones contain `aborted`.
  - `TestUnknownToolReturnsError`: model calls `nope` → result IsError listing available tool names.
- [ ] **Step 2:** FAIL. **Step 3:** implement loop: sample → if no calls, end → execute (sequentially in this lesson) → append results → repeat; max-steps final turn sends no tools and appends assistant-style instruction (opencode wording); synthetic results for orphans. `read_file` tool with `{path}` only, confined to `Env.Root`.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** `cmd/harness` now uses `repl.Run`, prints tool calls/results compactly; `--trace file.jsonl`.
- [ ] **Step 6: Demo** `cmd/trace-view trace.jsonl > trace.html`: self-contained HTML timeline (rows: model stream spans, tool spans; hover shows tokens/args). Run a live 3+ step task on a sample dir, render, describe output in README (include a small ASCII rendering too).
- [ ] **Step 7: README** — the loop as the core of every harness (compare pi `runLoop`, opencode `runLoop`, Codex `run_turn`), stop conditions, the "provider says stop with tool calls" quirk, events and why UI must be a subscriber, cancellation semantics.
- [ ] **Step 8:** verify.

### Task 4: Lesson 03 — tool design

**Files:** `03-tool-design/code/internal/tools/{read.go,glob.go,grep.go,ls.go,truncate.go,parallel.go}` + tests; `code/cmd/toolbench/main.go`; README. Remove `read_file` (replaced by `read`).

**Interfaces:** Produces `func Truncate(s string, maxLines, maxBytes int, mode TruncMode) (out string, truncated bool)` with `TruncHead|TruncTail|TruncHeadTail`; `func SaveOverflow(env *Env, s string) (path string, err error)`; constants `MaxLines=2000`, `MaxBytes=50*1024`, `MaxLineChars=2000`.

- [ ] **Step 1: Failing tests:** `TestReadLineNumbersAndPaging` (offset=3, limit=2 → lines `3: …`,`4: …` and footer `Use offset=5 to continue`); `TestReadRefusesBinary`; `TestTruncateRuneSafe` (multi-byte runes at cut point → valid UTF-8, marker `…N lines truncated…`); `TestDecodeErrorsAreActionable` (missing `path` → error contains `missing required field "path"`; unknown field → names it); `TestParallelReadOnlyOverlap` (two read-only tools with 200ms sleeps finish in <350ms; a non-read-only tool runs exclusively); `TestGrepRespectsGitignore`; `TestGlobCap100`.
- [ ] **Step 2:** FAIL. **Step 3:** implement; `Registry.Execute` uses an RWMutex per batch (read-only → RLock, else Lock), results in call order; overflow saved under `$TMPDIR/harness-overflow/` with hint text naming the path and suggesting grep/offset.
- [ ] **Step 4:** PASS. **Step 5: Demo** `cmd/toolbench`: scripted fake model issuing 12 reads across files, sequential vs parallel wall time (offline), plus a live run where the model hits a validation error and self-corrects (print the turn sequence). Paste outputs.
- [ ] **Step 6: README** — descriptions as prompts (Anthropic tool-writing guidance), LLM-facing vs UI-facing results, truncation limits across harnesses (pi/opencode 2000 lines/50KB, Codex head+tail, Claude Code 25k tokens), parallelism models (pi parallel + file queue, Codex RWLock). **Step 7:** verify.

### Task 5: Lesson 04 — editing files

**Files:** `04-editing-files/code/internal/tools/{edit.go,match.go,write.go,readtracker.go,workspace.go}` + tests; `code/cmd/editbench/main.go` + `code/cmd/editbench/corpus/*.json`; README.

**Interfaces:** Produces `type Strategy string` (`exact`,`trailing_ws`,`trimmed`,`unicode`); `func FindMatch(content, old string, strategies []Strategy) (start, end int, used Strategy, err error)`; `type ReadTracker` with `MarkRead(path string, mtime time.Time)`, `Check(path string) error`; `func (e *Env) Resolve(path string) (abs string, err error)` (workspace guard; `Env` gains `Reads *ReadTracker`).

- [ ] **Step 1: Failing tests:** `TestEditExactUnique`; `TestEditAmbiguousError` (2 matches → error contains `Found 2 matches` and `more surrounding context`); `TestEditReplaceAll`; `TestEditFallbackTrailingWS`; `TestEditFallbackUnicodeQuotes`; `TestDisproportionateMatchRejected`; `TestEditRequiresPriorRead` (error `read the file first`); `TestEditStaleRead` (file modified after read → error asks to re-read); `TestPreservesCRLFAndBOM`; `TestWorkspaceRejectsSymlinkEscape` (symlink inside root → `/etc` rejected; `../x` rejected); `TestEditReturnsUnifiedDiff`.
- [ ] **Step 2:** FAIL. **Step 3:** implement edit `{path, old_string, new_string, replace_all?}` with cascade order exact → trailing_ws → trimmed → unicode (Codex `seek_sequence` order), match must be unique unless replace_all; disproportionate guard (opencode thresholds: span ≥ max(old+3, old×2) lines); per-path mutex; write `{path, content}` (create parents, requires prior read when overwriting). Diff via a small LCS line diff in `internal/diff`.
- [ ] **Step 4:** PASS. **Step 5: Demo** `cmd/editbench`: ≥40 corpus cases (categories: exact, trailing whitespace, indentation drift, tabs/spaces, smart quotes, CRLF, ambiguous, near-miss that must NOT match). For each strategy prefix (exact only, +trailing_ws, +trimmed, +unicode) print success %, false-positive %, as table + bars. Offline. Paste output.
- [ ] **Step 6: README** — edit formats survey (Aider whole/diff/udiff findings incl. 9× error stat, Codex apply_patch grammar, Claude Code exact-only, opencode 9-replacer cascade and its v2 retreat), read-before-edit rationale, interim workspace guard. **Step 7:** verify.

### Task 6: Lesson 05 — running commands

**Files:** `05-running-commands/code/internal/tools/{bash.go,procgroup_unix.go,headtail.go}` + tests; `code/internal/ui/repl/confirm.go`; `code/cmd/shelltorture/main.go`; README.

**Interfaces:** Produces `type HeadTail struct` (`func NewHeadTail(limit int) *HeadTail`, `io.Writer`, `String() string`, `Dropped() int64`); bash tool `{command, timeout_ms?, workdir?}` default timeout 120000, max 600000; `Env` gains `Confirm func(ctx context.Context, command string) bool` (nil = allow; REPL sets y/n prompt; eval leaves nil).

- [ ] **Step 1: Failing tests:** `TestBashExitCodeAndStderr` (result contains `exit code: 3` and stderr text); `TestBashTimeoutKillsGroup` (`sleep 30 & sleep 30; wait`, timeout 500ms → returns <2s, no surviving child: check pgid via `ps`); `TestBashCancelKillsGroup`; `TestBashHugeOutputBounded` (`yes | head -c 50000000` → HeadTail holds ≤ limit, marker shows dropped bytes, RSS growth <50MB); `TestBashCwdPersists` (`cd sub` then `pwd` in next call → ends with `/sub`; cd outside root resets with notice); `TestConfirmDeniedReturnsError`.
- [ ] **Step 2:** FAIL. **Step 3:** implement: `/bin/bash -c` with `Setpgid`, kill `-pgid` SIGTERM then SIGKILL after 2s; HeadTail 50/50 split (Codex style); tracked cwd re-applied per call (Claude Code model: fresh process, cwd persists, env does not); output truncated with Task 4 `Truncate` tail mode + overflow file.
- [ ] **Step 4:** PASS. **Step 5: Demo** `cmd/shelltorture`: runs scenarios (50MB output, hang, SIGTERM-ignoring child via `trap '' TERM`, fork of 20 sleepers, interleaved stdout/stderr), prints per scenario: duration, bytes kept/dropped, orphans remaining (must be 0), peak RSS. Offline. Paste.
- [ ] **Step 6: README** — process groups, timeouts, head/tail vs tail-only, persistent shell vs fresh process tradeoffs (Codex PTY sessions with `write_stdin`, pi "use tmux"), background processes (described; research if needed). **Step 7:** verify.

### Task 7: Lesson 06 — system prompt and project instructions

**Files:** `06-system-prompt/code/internal/prompt/{system.go,instructions.go,env.go,inspect.go}`, `code/internal/prompt/templates/{base.md,anthropic.md,gpt.md}` (embedded), tests; `code/cmd/inspect/main.go`; `06-system-prompt/sample-repo/` (small Go module with a failing feature request: e.g. `stringsx` package missing `Reverse`, with tests); README.

**Interfaces:** Produces `func Discover(cwd, home string, budget int) []Instruction` (`type Instruction struct{ Path, Content string }`; global `~/.config/harness/AGENTS.md`; root = nearest ancestor with `.git` else cwd; per dir first of `AGENTS.override.md`, `AGENTS.md`, `CLAUDE.md`; root→cwd; total budget 32 KiB); `func Build(model string, env EnvInfo, instr []Instruction, tools []llm.ToolSpec) Prompt` with `type Prompt struct{ System string; Sections []Section }` (`Section{Name, Text string; Tokens int}`); `func EstimateTokens(s string) int` (bytes/4).

- [ ] **Step 1: Failing tests:** `TestDiscoverOrderAndFirstMatch` (root AGENTS.md + sub CLAUDE.md + sub AGENTS.md → root then sub/AGENTS.md); `TestDiscoverBudgetTruncates`; `TestPromptIsDeterministic` (two builds byte-identical; no timestamps in System); `TestModelVariantSelection` (`anthropic/…` → anthropic.md, `openai/…` → gpt.md, other → base.md).
- [ ] **Step 2:** FAIL. **Step 3:** implement; env block (cwd, platform, git yes/no, date as day only) placed in the *first user message* as `<environment>` rather than system (cache stability — Codex/Claude Code pattern); instructions also delivered in first user message, wrapped `<project_instructions path="…">`.
- [ ] **Step 4:** PASS. **Step 5: Demo** `cmd/inspect --model X` prints token breakdown bars per section (system, tool schemas each, instructions, env). Then live end-to-end: `go run ./cmd/harness --root ../sample-repo` "implement Reverse so tests pass" → agent edits and runs `go test` (bash confirm y). Paste trimmed transcript + inspect output.
- [ ] **Step 6: README** — prompt anatomy, pi's <1000-token stance vs larger prompts, per-model variants (opencode), instruction discovery comparison table (Codex 32KiB, pi, Claude Code hierarchy, opencode). **Step 7:** verify.

### Task 8: Lesson 07 — headless mode and evaluation

**Files:** `07-evaluation/code/cmd/harness/exec.go` (`harness exec --json "prompt"`); `code/internal/eval/{suite.go,runner.go,report.go,html.go}` + tests; `code/eval/tasks/NN-name/{task.md,repo/…,check.sh}` (10 tasks); `code/cmd/evalrun/main.go`; README.

**Interfaces:** Produces `type Task struct{ Name, Prompt, Dir string }`; `func LoadSuite(dir string) ([]Task, error)`; `type RunConfig struct{ Name, Model string; Repeats, Parallel int; HarnessArgs []string }`; `type Outcome struct{ Task, Config string; Rep int; Pass bool; Turns, InTok, OutTok, CacheRead int; CostUSD float64; Wall time.Duration; Err string }`; `func Run(ctx context.Context, tasks []Task, cfgs []RunConfig, harnessBin string) ([]Outcome, error)`; `func Summarize([]Outcome) []Summary` (per config: pass rate mean ± stddev across repeats, mean cost, mean turns); `report.WriteHTML`.

- [ ] **Step 1: Failing tests:** `TestLoadSuite`; `TestRunnerUsesTempCopy` (task repo untouched after run with fake harness binary); `TestSummarizeStats` (known outcomes → expected mean/stddev); `TestExecEmitsJSONL` (exec mode with fake model → each line parses as `agent.Event`, last is `agent_end` with usage).
- [ ] **Step 2:** FAIL. **Step 3:** implement; runner copies task repo to `os.MkdirTemp`, runs `harness exec --json --root tmp`, parses events for usage, runs hidden `check.sh` (copied in only after agent finishes) to decide pass. Exec mode: no confirm (approval "never" analogue) — runs only under eval temp dirs.
- [ ] **Step 4:** PASS. **Step 5:** author 10 tasks (2 bug fix, 3 feature, 2 refactor-preserving-tests, 1 multi-file, 1 needs exploration, 1 requires reading instructions in AGENTS.md); each `check.sh` runs `go test` on hidden tests; verify each task's reference solution passes its check (keep `solution.patch` per task, not copied into temp).
- [ ] **Step 6: Demo** `cmd/evalrun --configs sonnet,haiku --repeats 3`: terminal summary table + `report.html` (pass rate bars with error bars, cost vs pass scatter). Run live; paste summary, state cost.
- [ ] **Step 7: README** — why evals early, verifiable tasks, variance and repeats, exec modes (Codex `exec --json`, pi `--print`, Claude Code `-p`), Terminal-Bench/SWE-bench references. **Step 8:** verify.

### Task 9: Lesson 08 — terminal UI

**Files:** `08-terminal-ui/code/internal/ui/tui/{model.go,view.go,cells.go,status.go,input.go}` + tests; `code/internal/agent/steer.go`; `code/cmd/harness/main.go` (`--ui tui|repl`, default tui when stdout is a TTY); `code/cmd/replay/main.go`; README.

**Interfaces:** Produces `func (a *Agent) Steer(msg string)` (queued; injected after current tool batch — pi semantics); `tui.New(a *agent.Agent, opts Options) tea.Model`; `tui.FromEvents(events []agent.Event) tea.Model` (replay). Bubble Tea: use latest stable; confirm module path with `go list -m -versions` before writing code.

- [ ] **Step 1: Failing tests:** `TestSteerInjectedAfterToolBatch` (agent-level, fake model); `TestTUIRendersEvents` (feed events to model via `Update`, assert `View()` contains tool cell header `read path=x.go` and status `tokens`); `TestReplayDeterministic` (same events → identical final View).
- [ ] **Step 2:** FAIL. **Step 3:** implement: agent runs in goroutine, events forwarded as `tea.Msg`; cells for user/assistant text/tool calls (collapsed result preview); status bar model · context tokens · cost; Esc cancels run; Enter while running → Steer; bash confirm becomes a y/n modal. Keep markdown rendering out (plain text with minimal styling).
- [ ] **Step 4:** PASS. **Step 5: Demo:** live TUI session on sample-repo (describe + ASCII screenshot in README); `cmd/replay trace.jsonl` replays lesson-02-format trace into the TUI at recorded speed.
- [ ] **Step 6: README** — core/UI separation (Codex submission/event queues + app-server, opencode server+SSE, pi TUI diff rendering & scrollback vs fullscreen), steering vs follow-up, why we avoid markdown rendering. **Step 7:** verify.

### Task 10: Lesson 09 — permissions and safety

**Files:** `09-permissions/code/internal/permission/{rules.go,mode.go,shell.go,approver.go,doomloop.go,sandbox_darwin.go,sandbox_other.go,profile.sbpl}` + tests; remove interim `Confirm`; `code/cmd/redteam/main.go` + `redteam/cases.json`; TUI permission dialog; README.

**Interfaces:** Produces `type Action string` (`allow`,`ask`,`deny`); `type Rule struct{ Tool, Pattern string; Action Action }`; `type Mode string` (`default`,`accept-edits`,`plan`,`yolo`); `type Request struct{ Tool string; Subjects []string /* paths or subcommands */; Summary string }`; `type Decision struct{ Action Action; Remember bool; Feedback string }`; `type Approver interface{ Approve(ctx context.Context, r Request) Decision }`; `func Evaluate(rules []Rule, mode Mode, r Request) Action` (deny → ask → allow, first match per tier; mode defaults); `func SplitCommand(cmd string) ([]string, error)` (mvdan.cc/sh; splits `&&`,`||`,`;`,`|`, subshells, strips `timeout/nice/nohup` wrappers; returns error on unparseable → ask); `func SandboxCommand(cmd []string, writable []string, network bool) []string`; `Env` gains `Perm *permission.Checker`. Event kinds added: `permission_request`, `permission_reply`.

- [ ] **Step 1: Failing tests:** `TestDenyBeatsAllow`; `TestCompoundCommandAllSubcommandsMustMatch` (`npm test && rm -rf /` with allow `npm test*` → not allowed); `TestWrapperStripped`; `TestRedirectTargetChecked` (`echo x > ~/.ssh/authorized_keys` → ask/deny via path rule); `TestPlanModeDeniesWrites`; `TestRememberAddsSessionRule`; `TestRejectFeedbackReachesModel` (tool result contains user feedback text); `TestDoomLoopAsksAfter3Identical`; `TestSandboxBlocksWriteOutsideRoot` (darwin only: `touch $HOME/harness-sbx-test` under sandbox fails, inside root succeeds; `curl` fails with network off).
- [ ] **Step 2:** FAIL. **Step 3:** implement; Seatbelt profile modeled on Codex base policy (`(deny default)`, process exec/fork, reads allowed, writes only to `(subpath (param "WRITABLE_ROOT_n"))` + TMPDIR, `.git` read-only, network policy appended only when enabled); invoke only `/usr/bin/sandbox-exec -p <profile> -D… -- /bin/bash -c`. Approval round trip: agent emits `permission_request` with a reply channel in `Data`; REPL/TUI answer; exec mode uses mode + rules only (ask → deny).
- [ ] **Step 4:** PASS. **Step 5: Demo** `cmd/redteam`: ≥25 cases (rm variants, `sh -c`, `/bin/rm`, `$(…)`, `eval`, redirects to `~/.ssh`, `curl | sh`, `git push`, env exfil via curl, writing `.git/hooks`) → matrix: caught by rules / parser / sandbox / not caught. Offline. Paste.
- [ ] **Step 6: README** — threat model, modes across harnesses, rule evaluation orders (Claude Code deny→ask→allow vs opencode findLast), arity-based "always" patterns, "rules are not a security boundary", Seatbelt and bubblewrap, escalation flow (Codex), pi's "security theater"/container argument. **Step 7:** verify.

### Task 11: Lesson 10 — sessions

**Files:** `10-sessions/code/internal/session/{store.go,entry.go,rebuild.go,tree.go,snapshot.go}` + tests; `/resume`, `--continue`, `--resume ID` wiring; `code/cmd/sessions/main.go` (list + tree); README.

**Interfaces:** Produces `type Entry struct{ ID, ParentID string; Time time.Time; Type string; Message *llm.Message; Data json.RawMessage }` (types: `header`,`message`,`model_change`,`compaction`,`label`); `func Create(dir, cwd string) (*Store, error)`, `func Open(path string) (*Store, error)`, `(*Store).Append(e Entry) error` (fsync each append), `(*Store).Branch(fromID string)`, `(*Store).Messages(leafID string) []llm.Message`; path `~/.local/share/harness/sessions/<cwd-slug>/<timestamp>_<id>.jsonl`; `type Snapshots` (`Track(ctx) (treeHash string, err error)`, `Restore(ctx, treeHash string) error`) using a shadow bare git dir `--git-dir <data>/snapshots/<hash(root)> --work-tree <root>`. Event kind `snapshot`.

- [ ] **Step 1: Failing tests:** `TestAppendAndReopen`; `TestTornLastLineIgnored` (truncate file mid-line → reopen succeeds, last partial entry dropped); `TestRebuildAddsResultsForDanglingCalls`; `TestBranchCreatesTree` (branch from earlier entry → two leaves; Messages(leaf) differ); `TestSnapshotRestore` (agent edits file, Restore → original content; user's real `.git` untouched).
- [ ] **Step 2:** FAIL. **Step 3:** implement; snapshot at each turn start; `/undo` restores previous turn's tree.
- [ ] **Step 4:** PASS. **Step 5: Demo:** script `demo.sh`: start exec task, `kill -9` mid-turn, `harness --continue` finishes it; `cmd/sessions tree ID` prints branch tree (Unicode box drawing); `/undo` demo. Paste.
- [ ] **Step 6: README** — log formats (pi tree JSONL, Codex rollouts, opencode SQLite), append-only + never delete, resume reconstruction, branching, shadow-git snapshots. **Step 7:** verify.

### Task 12: Lesson 11 — context budget and pruning

**Files:** `11-context-budget/code/internal/ctxmgr/{budget.go,calibrate.go,prune.go}` + tests; status bar context meter; `/context` command (plain, before Task 15's command system: special-cased in REPL); `code/cmd/ctxsim/main.go`; README.

**Interfaces:** Produces `type Budget struct{ Window, Reserve int }` with `Usable() int` (`Reserve = min(20000, model max output)`); `type Calibrator` (`Observe(estimated, actual int)`, `Estimate(s string) int` — bytes/4 × running ratio); `func Prune(msgs []llm.Message, protectTokens int) (out []llm.Message, cleared int)` (walk backward, keep last 2 user turns and `protectTokens`=40000 of tool output, replace older tool results with `[Old tool result content cleared]`, never touch tool-call/result pairing); `Agent` applies Prune before each request when estimated context > 70% of Usable.

- [ ] **Step 1: Failing tests:** `TestUsable`; `TestCalibratorConverges`; `TestPruneKeepsRecentAndPairs`; `TestPruneIdempotent`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/ctxsim`: offline simulation with fake model of a 60-turn session reading large files; chart (terminal sparkline + HTML line chart) of context tokens per turn: no pruning (crosses limit, marked), pruning on. Paste.
- [ ] **Step 6: README** — context rot, attention budget (Anthropic), tool-result clearing as lightest compaction, thresholds across harnesses, why pruning breaks the cache (forward pointer to lesson 12). **Step 7:** verify.

### Task 13: Lesson 12 — prompt caching

**Files:** `12-prompt-caching/code/internal/llm/cache.go` + tests; `internal/prompt/reminder.go`; config flag `--cache auto|manual|off`; `code/cmd/cachelab/main.go`; README.

**Interfaces:** Produces `type CacheMode string`; `func ApplyCacheMarkers(body map[string]any, mode CacheMode)` (manual: `cache_control:{type:"ephemeral"}` on system message and last 2 messages, max 4 breakpoints; auto: `providerOptions.gateway.caching:"auto"`); `prompt.Reminder(text string) llm.Block` (`<system-reminder>…</system-reminder>` appended to the next user/tool message instead of editing System); session id sent as `providerOptions.gateway.sessionId`; Prune (Task 12) gains `MinClear` (only prune when ≥20000 tokens would be cleared) to limit cache busting. `Usage.CacheRead/CacheWrite` displayed.

- [ ] **Step 1: Failing tests:** `TestManualMarkersPlacement`; `TestAutoModeSetsProviderOption`; `TestToolOrderStable` (registry with tools registered in different orders → identical Specs JSON); `TestReminderDoesNotChangeSystem`; `TestPruneMinClear`.
- [ ] **Step 2:** FAIL. **Step 3:** implement (respect probe findings). **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/cachelab`: eval subset (5 tasks × 3 repeats) under configs `friendly` (default) vs `hostile` (timestamp in system, shuffled tool order, `--cache off`) on an Anthropic model and one implicit-cache model; table: cache-read %, cost, latency per config. Live; paste with cost.
- [ ] **Step 6: README** — prefix caching mechanics, pricing multipliers, Claude Code's caching lessons (no tool changes, reminders, no model switch, alerts on hit rate), Codex prefix-extension discipline and MCP order bug, gateway session affinity. **Step 7:** verify.

### Task 14: Lesson 13 — compaction

**Files:** `13-compaction/code/internal/ctxmgr/{compact.go,serialize.go,prompts/compact.md,prompts/update.md}` + tests; `/compact [focus]`; event kind `compaction`; `code/cmd/tinywindow/main.go`; README.

**Interfaces:** Produces `type Compactor struct{ Client llm.Client; Model string; KeepRecent int /* 20000 */ }`; `func (c *Compactor) Compact(ctx context.Context, sys string, tools []llm.ToolSpec, msgs []llm.Message, prior string, focus string) (summary string, kept []llm.Message, err error)`; `func Serialize(msgs []llm.Message) string` (`[User]: …`, `[Assistant]: …`, `[Assistant tool call]: read(path=…)`, `[Tool result]: …` cut at 2000 chars); `func SplitPoint(msgs []llm.Message, keepTokens int) int` (never splits call/result; cut lands before a user message when possible). Trigger: estimated context > `Usable()` − 16384 (pi reserve) after a turn, or provider overflow error (once). Summary sections: Goal / Constraints & Preferences / Progress (Done, In Progress, Blocked) / Key Decisions / Next Steps / Critical Context / Relevant Files. After compaction, re-inject instructions + up to 5 most recently edited files (≤5k tokens each) as a reminder; append "Continue if you have next steps…". Session store writes a `compaction` entry; originals never deleted; rebuild starts from latest compaction.

- [ ] **Step 1: Failing tests:** `TestSplitPointNeverOrphans`; `TestSerializeFormat`; `TestCompactionKeepsTailAndSummary` (fake summarizer); `TestIterativeSummaryUsesPrior` (second compaction request contains `<prior-summary>`); `TestOverflowCompactsAndRetries` (fake returns 400 context-length error once → compaction → retry succeeds; second overflow surfaces error); `TestResumeAfterCompaction`.
- [ ] **Step 2:** FAIL. **Step 3:** implement; summarize request reuses the session's System + Tools and appends the compaction prompt as a user message (Claude Code cache-friendly pattern), tool_choice none. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/tinywindow`: runs a multi-file eval task with `--context-window 30000` (artificial), 3 repeats with compaction on vs off: pass/fail, number of compactions; HTML timeline marking compaction points and summary text. Live; paste.
- [ ] **Step 6: README** — thresholds (Codex 90%, pi window−16k, opencode reserve, Claude Code), summary templates compared, what to re-inject, cache-aware summarization, server-side compaction (Codex remote, Anthropic `compact_20260112`) mentioned. **Step 7:** verify.

### Task 15: Lesson 14 — slash commands, templates, skills

**Files:** `14-commands-and-skills/code/internal/commands/{registry.go,builtin.go,template.go,skills.go}` + tests; migrate `/context`, `/compact`, `/undo`, `/resume` into it; add `/model`, `/clear`, `/help`; TUI command palette (filtered list on `/`); `code/cmd/skillcost/main.go`; sample `.harness/commands/review.md`, `.harness/skills/go-testing/SKILL.md`; README.

**Interfaces:** Produces `type Command struct{ Name, Description string; Run func(ctx context.Context, h *Host, args string) error }` (`Host` exposes agent, session, UI print); `func Parse(input string) (name, args string, ok bool)`; `func Expand(tmpl string, args string, shell func(cmd string) (string, error)) (string, error)` (`$ARGUMENTS`, `$1..$9`, `${1:-default}`, ``!`cmd` ``); skills: `type Skill struct{ Name, Description, Path string }`, `func LoadSkills(dirs ...string) []Skill`, listing injected into the first user message; `skill` tool `{name}` returns body (progressive disclosure). Dirs: `~/.config/harness/{commands,skills}`, `<root>/.harness/{commands,skills}`.

- [ ] **Step 1: Failing tests:** `TestParse`; `TestExpandArgs`; `TestExpandShellRunsThroughPermission` (``!`cmd` `` goes through permission checker); `TestSkillsListingOnlyMetadata`; `TestSkillToolReturnsBody`; `TestModelCommandSwitches` (records `model_change` entry).
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/skillcost`: with 12 sample skills, tokens in context for listing-only vs inlining all bodies; plus live run where the model loads `go-testing` skill when asked to write tests. Paste.
- [ ] **Step 6: README** — command processing pipeline (input → command? → template → prompt), opencode/Claude Code/pi template syntax, skills spec & progressive disclosure, Codex replacing custom prompts with skills. **Step 7:** verify.

### Task 16: Lesson 15 — memory

**Files:** `15-memory/code/internal/memory/{store.go,tool.go,recall.go}` + tests; `/memory` command; `code/cmd/memorydemo/main.go`; README. Research first: append memory findings (Claude Code auto memory, Codex `memories`, Anthropic memory tool) to `research-notes.md` if beyond current notes.

**Interfaces:** Produces store at `~/.local/share/harness/projects/<root-slug>/memory/` with `MEMORY.md` index + topic files (frontmatter `name`, `description`, `type: user|feedback|project|reference`); `memory` tool `{command: view|create|str_replace|delete, path, …}` confined to the memory dir (path traversal rejected); `func Recall(dir string, maxLines int /*200*/) string` injected in first user message; system prompt section describing when to save.

- [ ] **Step 1: Failing tests:** `TestMemoryToolCRUD`; `TestMemoryPathTraversalRejected`; `TestRecallTruncatesIndex`; `TestIndexUpdatedOnCreate`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/memorydemo`: session 1 on sample-repo where the user states a convention ("table-driven tests, use `got/want` names"); session 2 fresh context asked to add a test → check output follows convention; run 3× with memory on and off; show memory files. Live; paste.
- [ ] **Step 6: README** — types of memory (instructions, notes files PLAN.md/TODO.md per pi, auto memory, memory tool), write policies, staleness, privacy. **Step 7:** verify.

### Task 17: Lesson 16 — subagents

**Files:** `16-subagents/code/internal/subagent/{task.go,defs.go}` + tests; agent defs `.harness/agents/explore.md` (read-only tools, small model) and built-in `general`; TUI nested activity indicator; `code/cmd/subagentlab/main.go`; README.

**Interfaces:** Produces `task` tool `{description, prompt, agent}`; `type Def struct{ Name, Description, Model, System string; Tools []string; MaxSteps int }`; child `agent.Agent` with fresh history, filtered registry, inherited deny rules, depth limit 1 (child registry excludes `task`); returns final text wrapped `<task_result>…</task_result>`; child events forwarded with `Data["parent_call"]`. `task` is ReadOnly → parallel subagents via Task 4 executor.

- [ ] **Step 1: Failing tests:** `TestSubagentIsolatedHistory`; `TestSubagentToolFilter`; `TestNoNestedTask`; `TestParallelSubagents` (two tasks overlap in time); `TestChildUsesDefModel` (request body model).
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/subagentlab`: exploration question over a medium repo (use this tutorial's own lesson-15 `code/` as the corpus) inline vs via 3 parallel explore subagents, 3 repeats: parent peak context, total cost, wall time, answer quality check (expected file names present); HTML timeline of parallel children. Live; paste.
- [ ] **Step 6: README** — context isolation, summaries back (1–2k tokens), per-agent model, Claude Code/opencode definitions, depth limits, pi's critique. **Step 7:** verify.

### Task 18: Lesson 17 — hooks and the feedback loop

**Files:** `17-hooks/code/internal/hooks/{config.go,run.go,events.go}` + tests; config `.harness/hooks.json`; sample hooks `hooks/gofmt-vet.sh`, `hooks/block-secrets.sh`; README.

**Interfaces:** Produces events `session_start`, `user_prompt_submit`, `pre_tool_use`, `post_tool_use`, `stop`; `type Hook struct{ Event, Matcher, Command string; TimeoutSec int /*60*/ }` (matcher: exact tool name, `A|B`, or regex); handler gets JSON on stdin `{event, tool, args, result?, cwd, session_id}`; exit 0 → stdout JSON optional `{decision:"block"|"allow", reason, updated_args, additional_context}`; exit 2 → block with stderr fed to model; other → logged non-blocking. `stop` hook exit 2 → agent continues with stderr as user message (cap 3 consecutive).

- [ ] **Step 1: Failing tests:** `TestPreToolUseBlockExit2`; `TestPreToolUseRewritesArgs`; `TestPostToolUseAddsContext`; `TestStopHookContinuesLoopCapped`; `TestHookTimeout`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo:** eval suite (all 10 tasks × 3 repeats, haiku-class model to make failures visible) with vs without post-edit `gofmt -l` + `go vet` hook and a stop hook running `go test ./...`: pass rate, turns, cost. Live; paste.
- [ ] **Step 6: README** — hook systems (Claude Code event list & exit codes, pi extensions, opencode plugin hooks, Codex Stop hooks), lint/test loops (Aider, opencode LSP diagnostics), LSP as a pointer. **Step 7:** verify.

### Task 19: Lesson 18 — MCP

**Files:** `18-mcp/code/internal/mcp/{client.go,jsonrpc.go,adapter.go,search.go}` + tests; `18-mcp/code/cmd/mini-mcp/main.go` (tiny MCP server: tools `todo_add`, `todo_list`, `word_count`); config `.harness/mcp.json`; `code/cmd/mcpcost/main.go`; README. Research first: confirm current MCP protocol version string and `initialize` shape; append to research notes.

**Interfaces:** Produces `func Start(ctx context.Context, name string, cmd []string, env []string) (*Client, error)` (stdio, newline-delimited JSON-RPC 2.0; `initialize` → `notifications/initialized` → `tools/list`); `(*Client).Call(ctx, tool string, args json.RawMessage) (tools.Result, error)`; adapter registers each MCP tool as `mcp__<server>__<tool>` (sanitized, ≤64 chars); deferred mode: only `tool_search {query}` registered up front, which returns matching tool specs and activates them by appending a reminder + registering (tools list grows → note cache cost, compare Claude Code `defer_loading`).

- [ ] **Step 1: Failing tests:** `TestHandshakeAndList` (against `mini-mcp` built in test); `TestCallMapsIsError`; `TestToolNaming`; `TestDeferredSearchActivates`; `TestServerCrashSurfacesError`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/mcpcost`: generate a synthetic MCP server with 40 tools (mini-mcp `--synthetic 40`) → tokens of tool schemas loaded vs deferred; live end-to-end where agent uses `todo_add`/`todo_list`. Paste.
- [ ] **Step 6: README** — MCP architecture, transports, naming, context overhead (pi's Playwright numbers), deferred/tool-search (Claude Code, pi codemode), MCP vs CLI+README argument. **Step 7:** verify.

### Task 20: Lesson 19 — model selection and routing

**Files:** `19-model-routing/code/internal/route/{roles.go,router.go,transform.go}` + tests; titles via small model; `/model` cost warning; `code/cmd/routelab/main.go`; README.

**Interfaces:** Produces `type Roles struct{ Main, Small, Subagent string }` (resolution: flag > env > config > default); `func TransformForModel(msgs []llm.Message, target string) []llm.Message` (drop Thinking blocks whose producing model ≠ target; normalize tool-call IDs to `^[a-zA-Z0-9_-]{1,64}$` consistently across call+result; drop errored/aborted assistant turns); `type Router interface{ Pick(ctx context.Context, task string, state RunState) string }`; `HeuristicRouter` (small model unless prompt/repo signals complexity) and `EscalatingRouter` (start small; escalate to main after N=2 failed verifications or a failed tool streak ≥3; once escalated, stay — no mid-session downgrade); gateway fallbacks via `providerOptions.gateway.models`.

- [ ] **Step 1: Failing tests:** `TestRolesPrecedence`; `TestTransformDropsForeignThinking`; `TestTransformNormalizesIDsConsistently`; `TestEscalatingRouterNeverDowngrades`; `TestFallbackModelsInRequest`; `TestTitleUsesSmallModel`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Demo** `cmd/routelab`: full eval suite × 3 repeats under `fixed-strong` (sonnet-5.5), `fixed-cheap` (haiku-5.5), `heuristic`, `escalating`: pass rate ± spread, cost, cache-read %; HTML cost-vs-pass scatter. Live; paste with total cost.
- [ ] **Step 6: README** — answer "how is the right model chosen": roles across harnesses (Claude Code background model/opusplan, opencode small_model, Codex review/subagent models, Aider architect/editor/weak), routers (RouteLLM, OpenRouter auto, Cursor's cache-aware router, SWE-Router), costs of switching (cache, signatures), our measured results. **Step 7:** verify.

### Task 21: Lesson 20 — where to go next + final consistency pass

**Files:** `20-where-to-go-next/README.md`; Modify: root `README.md`, `research-notes.md`.

- [ ] **Step 1: Research** (web-search subagent): for each lesson concept, the file paths in pi (`earendil-works/pi`), opencode (`anomalyco/opencode`, v1 and v2 layout), Codex (`openai/codex/codex-rs`). Append to research notes with links.
- [ ] **Step 2: README** — omitted topics (LSP integration, web fetch/search, images, plan mode, client/server split, containers/devcontainers, ACP, background tasks), reading guide table lesson → files in each harness, suggested next projects.
- [ ] **Step 3: Consistency pass:** for every lesson N≥1 run `diff -rq (N-1)/solution N/solution` and confirm the README's "Build it" covers every changed package; run vet+test in all 21 solutions (`for d in */solution; do (cd $d && go vet ./... && go test ./...) || echo FAIL $d; done`) → no FAIL lines; confirm `tests/` and `support/` files are byte-identical to their `solution/` counterparts; grep all READMEs for the user's banned word → none; verify every README has the 9 sections.

---

## Self-review notes

- Spec coverage: lessons 00–20 map to Tasks 1–21; verification standard → Global Constraints + Task 21 Step 3; research policy → Tasks 1, 16, 19, 21; wire risk → Task 1 Step 1; interim guard → Tasks 5, 6, 8; budget/repeats → Tasks 8, 13, 14, 16–20.
- Type names checked across tasks: `llm.Message/Block/ToolCall/ToolResult/Usage`, `agent.Event/Sink/Agent`, `tools.Env/Result/Tool/Registry`, `permission.Approver/Decision`, `session.Store/Entry`, `ctxmgr.Budget/Prune/Compactor`.
