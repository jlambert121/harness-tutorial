# Lesson 02: The Agent Loop

## What you'll build

The loop that turns a chat model into an agent: send the conversation, run the tools the model asks for, append the results, and repeat until it answers in plain text. You'll also build the event stream the rest of the harness hangs off, cancellation that never leaves the conversation broken, and a first tool (`read_file`). The demo records a real run and draws it as a timeline.

## How the real harnesses do it

The loop has the same shape in every harness:

```
history += user message
loop:
    response = model(system, tools, history)        # streamed
    history += response
    if response has no tool calls: stop
    results = run(response.tool_calls)
    history += results                              # one result per call, in order
```

The differences are in the edges:

- **pi** (`packages/agent/src/agent-loop.ts`). If the response stopped with `length` (it hit `max_tokens`), pi fails every tool call without running it, because "arguments may be truncated". After each tool batch it checks for **steering** messages the user typed meanwhile. When the model stops calling tools, it checks for queued **follow-up** messages before ending. There's no step limit: "I never found a use case for that."
- **opencode** (`session/prompt.ts`). One model step per loop iteration. The exit condition explicitly handles providers that return `finish_reason: "stop"` while still sending tool calls. Three identical consecutive tool calls (same name, same arguments) count as a "doom loop" and trigger a permission prompt. When an agent's step limit is reached, opencode injects "CRITICAL - MAXIMUM STEPS REACHED … Tools are disabled until next user input. Respond with text only."
- **Codex** (`core/src/session/turn.rs`). It rebuilds the full prompt from history before every request. Tools start running as each tool-call item finishes streaming, before the whole response is done. If a turn is aborted, `ensure_call_outputs_present` adds a synthetic `"aborted"` output for every call that has none.

## Design for our harness

### The API is stateless

The model remembers nothing between requests. Every step re-sends the system prompt, the tool definitions and the **entire** history. In this lesson's demo, input grows on every step: 495 → 711 → 1036 → 1132 tokens. That means a long session's total cost grows roughly with the square of its length. Lessons 11–13 (budget, caching, compaction) all exist to manage this.

### Invariants that keep requests valid

Providers reject malformed histories, so the loop has to maintain two things:

1. **Every tool call gets exactly one result, in the same order.** That holds on every path: success, unknown tool, model cut off by `length`, step limit, cancellation.
2. **No half-written content goes into history.** If the user cancels mid-stream, the partial assistant message may hold a tool call with truncated JSON and no result, or a thinking block with an incomplete signature. Keep the visible text, drop everything else.

### Stop conditions

| Situation | What the loop does |
|---|---|
| No tool calls in the response | Done. Return nil. |
| `Stop: length` with tool calls | Don't run them. Each gets an error result ("may be truncated"). Continue so the model can retry with a smaller call. |
| Step `MaxSteps` reached | That request sends `tool_choice: "none"` plus a request-only user message `MaxStepsPrompt`, so the model has to write a summary. Tool calls made anyway get "not executed" results. |
| `Stop: error` | Emit an `error` event. The failed turn adds nothing to history. Return the error. |
| Context cancelled | Keep partial text. Any calls not yet run get "aborted" results. Return `ctx.Err()`. |

Why not do the step-limit turn opencode's way? opencode appends an *assistant* message. Claude 5.5 models reject a request that ends with an assistant message: "This model does not support assistant message prefill. The conversation must end with a user message." (verified through the gateway). Keeping the tools and sending `tool_choice: "none"` also leaves the tool definitions, the start of the request, unchanged, so a cached prefix still matches (lesson 12).

### Events, not printing

The loop never prints. It emits `Event` values to a `Sink func(Event)`:

```
agent_start
  turn_start(1)  text_delta*  toolcall_start*  message_end  tool_start  tool_end ...  turn_end
  turn_start(2)  text_delta*  message_end  turn_end
agent_end(usage)
```

The REPL renders these events. So do the trace recorder, the TUI in lesson 08, headless mode in lesson 07, and the tests. `Tee` sends one stream to several sinks. A callback is used rather than a channel because it keeps ordering trivial and means the loop doesn't need a consumer goroutine. The sink must return quickly; a UI that needs async rendering does its own buffering.

### Cancellation

`Run` takes a `context.Context`. Ctrl-C in the REPL cancels it. The model stream ends with `aborted`, and `Registry.Execute` stops starting new calls. The running tool receives the cancelled context and is expected to return promptly. The bash tool in lesson 05 shows how to make a subprocess honor that.

## The contract

`harness/internal/agent`:

```go
type EventKind string
const (
    AgentStart = "agent_start"; TurnStart = "turn_start"; TextDelta = "text_delta"; ThinkingDelta = "thinking_delta"
    ToolCallStart = "toolcall_start"; MessageEnd = "message_end"; ToolStart = "tool_start"; ToolEnd = "tool_end"
    TurnEnd = "turn_end"; AgentEnd = "agent_end"; Error = "error"
)   // all of type EventKind

type Event struct {
    Kind EventKind; Turn int; Text string
    Call *llm.ToolCall; Result *llm.ToolResult; Message *llm.Message; Usage *llm.Usage
    Data map[string]any
    Time time.Time            // set on every event
}                             // JSON tags: see solution/internal/agent/events.go (the trace recorder relies on them)
type Sink func(Event)
func Tee(sinks ...Sink) Sink  // skips nil sinks

type Config struct {
    Client llm.Client; Model, System string; Tools *tools.Registry
    MaxSteps int               // 0 = unlimited
    MaxTokens int; Reasoning string
}
func New(cfg Config) *Agent
func (a *Agent) Run(ctx context.Context, input string, sink Sink) error
func (a *Agent) History() []llm.Message   // a copy
func (a *Agent) Usage() llm.Usage         // cumulative across runs
const MaxStepsPrompt = "CRITICAL - MAXIMUM STEPS REACHED. ..."   // must contain that phrase
```

`harness/internal/tools`:

```go
type Env struct { Root string }
type Result struct { Content string; Details any; IsError bool }  // Content → model, Details → UI
type Tool interface {
    Spec() llm.ToolSpec
    ReadOnly() bool
    Run(ctx context.Context, env *Env, args json.RawMessage) Result
}
type Func struct { Def llm.ToolSpec; RO bool; Fn func(ctx context.Context, env *Env, args json.RawMessage) Result }  // implements Tool

func NewRegistry(env *Env) *Registry
func (r *Registry) Register(t Tool)
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) Env() *Env
func (r *Registry) Specs() []llm.ToolSpec   // sorted by name
func (r *Registry) Execute(ctx context.Context, calls []llm.ToolCall,
    onStart func(llm.ToolCall), onEnd func(llm.ToolCall, llm.ToolResult)) []llm.ToolResult
func ReadFile() Tool                          // name "read_file", args {"path": string}
```

Provided in `support/`: `internal/ui/repl` (renders events), `internal/trace` (JSONL recorder plus ASCII and HTML timelines), `cmd/harness`, `cmd/trace-view`. The lesson also includes a `sample/` workspace for the demo.

## Build it

Continue from lesson 01 and copy this lesson’s `tests/` and `support/`. The `internal/tools/` and `internal/agent/` implementation packages are new; the REPL and trace code are supplied.

1. **Registry.** Tests: `go test ./internal/tools`.
   - `Specs()` sorts by name. Go map iteration is random, and an unstable tool order changes the request prefix and defeats prompt caching (lesson 12). Codex once shipped exactly this bug, with MCP tools.
   - `Execute` returns results in call order. An unknown tool gets an error result that **lists the available tools**. An error the model can act on beats one it has to guess about.
   - Once `ctx` is cancelled, calls that haven't started get `"Tool execution aborted…"`.

2. **`read_file`.** Join the path to `Env.Root`, clean it, and reject anything that `filepath.Rel` puts outside the root. Return errors as `Result{IsError: true}`. **Tool failures are data for the model, not Go errors.** Lesson 04 makes the workspace check symlink-safe.

3. **Events.** `events.go` is mostly declarations; copy the contract.

4. **The loop.** Tests: `go test ./internal/agent -run 'TestLoop|TestEventOrder|TestHistory'`.
   - `sample()` consumes `Client.Stream` and forwards deltas as events. `Run` appends the user message, then loops: build the `llm.Request` from history, sample, append the assistant message, execute the calls, append one `RoleTool` message per result.
   - Emit `message_end` before running tools, so UIs can finish rendering the text first.

5. **Edge cases.** Tests: `-run 'TestMaxSteps|TestLength|TestUnknown|TestModelError'`.
   - The step-limit notice is passed in `req.Messages` but **not** stored in `a.history`. Put `{"tool_choice":"none"}` in `req.Extra`.

6. **Cancellation.** Tests: `-run TestCancel`.
   - When the stream is aborted, keep only text: `keepPartial`.
   - After `Execute` returns, append results first, then check `ctx.Err()`. That way the aborted calls' results are in history before you return.

## Run it

```sh
go test ./...
go run ./cmd/harness --root <lesson-dir>/sample --model anthropic/claude-haiku-5.5 --trace /tmp/trace.jsonl
> Start with main.go and follow the code to explain exactly how the port is chosen when APP_CONFIG is unset. Read every file you need.
```

Press Ctrl-C during a reply to cancel it, then ask a follow-up. The conversation continues, because history stayed valid.

## Demo

```sh
go run ./cmd/trace-view -html /tmp/trace.html /tmp/trace.jsonl
```

From a real run (Haiku 5.5):

```
turn 1 model                      |░░░░░░░░░░░█                                                |  1.026s   in 495 (cached 0) · out 75 · $0.00009
read_file {"path":"main.go"}      |           ▓                                                |      0s   224 bytes: package main import ( "fmt" "os" ) …
turn 2 model                      |           ░░░░░░░░░░░░██                                   |  1.048s   in 711 (cached 0) · out 94 · $0.00012
read_file {"path":"config.go"}    |                        ▓                                   |      0s   490 bytes: package main import ( "encoding/json" …
turn 3 model                      |                        ░░░░░░░░░██                         |   865ms   in 1036 (cached 0) · out 51 · $0.00013
read_file {"path":"defaults.go"}  |                                  ▓                         |      0s   87 bytes: package main func Defaults() Config { …
turn 4 model                      |                                  ░░░░░░░░░░░███████████████|  2.112s   in 1132 (cached 0) · out 324 · $0.00028
                                   total 5.054s   ░ waiting for first token  █ streaming  ▓ tool
```

What it shows:

- **Most wall-clock time is waiting for the first token (░).** Each step pays a full request round trip plus prompt processing. File reads take microseconds by comparison.
- **The model reads one file per step.** Three dependent reads cost three round trips. Lesson 03 adds parallel tool execution and tools that discover files. Here the model had to follow imports by hand.
- **Input grows every turn** (495 → 1132) because history is re-sent. 3,374 input tokens were billed for 87 + 224 + 490 bytes of source. `cached 0`: caching isn't enabled yet (lesson 12).

The HTML version shows the same timeline with hover details. Cost: under $0.001.

## Exercises

1. Use a trace to compare input tokens on successive model calls. How does re-sending the growing history affect the cost of a multi-step run?
2. Try detecting three identical consecutive tool calls and returning an error asking the model to change approach. Does the model recover? Lesson 09 implements a related check.

## Further reading

- Codex agent loop: https://openai.com/index/unrolling-the-codex-agent-loop/
- pi agent loop and events: https://github.com/earendil-works/pi (packages/agent/src/agent-loop.ts)
- Anthropic, "Building effective agents": https://www.anthropic.com/engineering/building-effective-agents
