# Lesson 01: Talking to the Model

## What you'll build

An HTTP client that streams a model response from the gateway, over server-sent events (SSE), and turns it into provider-neutral messages. It handles retries, reasoning blocks, token usage and cost. At the end you'll have a streaming chat REPL and a benchmark comparing five models.

This is the bottom layer of the harness. Everything above it (the loop, tools, compaction, routing) sees only `llm.Message` and `llm.StreamEvent`, never JSON from a particular provider.

## How the real harnesses do it

- **pi** wrote its own multi-provider layer (`packages/ai`). It speaks four wire APIs natively: OpenAI Completions, OpenAI Responses, Anthropic Messages, and Google. Two of its design rules are worth copying:
  - **A stream never throws.** Once a stream is returned, every failure arrives as a final assistant message with `stopReason: "error"` or `"aborted"`. Callers have exactly one code path for "the response ended".
  - **Partial results survive aborts.** Zechner calls missing abort support "entirely unacceptable." When you cancel, you keep what streamed so far.
- **opencode** uses the Vercel AI SDK's `streamText` and maps its stream into its own events. Its retry policy is the one used here: 2 s initial delay, ×2 backoff, 25 % jitter, 30 s cap, 5 retries, honoring `retry-after` and `retry-after-ms`.
- **Codex** uses OpenAI's Responses API with `store: false`, so requests are stateless (needed for zero-data-retention customers). Encrypted reasoning comes back in each response and is sent again on the next request. Newer versions also have a WebSocket transport that sends only new input plus `previous_response_id`.
- **Claude Code** talks to the Anthropic Messages API.

Every harness needs some layer like this. The choice is how many wire formats it speaks. You'll speak one, Chat Completions, through a gateway that translates it for every provider.

## Design for our harness

### The wire format

A request (abridged):

```json
{
  "model": "anthropic/claude-sonnet-5.5",
  "stream": true,
  "stream_options": {"include_usage": true},
  "messages": [
    {"role": "system", "content": "You are..."},
    {"role": "user", "content": "read a.go"},
    {"role": "assistant", "content": null,
     "tool_calls": [{"id": "call_1", "type": "function",
                     "function": {"name": "read", "arguments": "{\"path\":\"a.go\"}"}}],
     "reasoning_details": [ ...replayed verbatim... ]},
    {"role": "tool", "tool_call_id": "call_1", "content": "package a"}
  ],
  "tools": [{"type": "function", "function": {"name": "read", "description": "...", "parameters": {JSON Schema}}}],
  "reasoning": {"effort": "low"},
  "providerOptions": {"gateway": {"caching": "auto"}}
}
```

Note: `arguments` is a JSON **string**, not an object. Tool results are separate `role: "tool"` messages tied back by `tool_call_id`.

The response is an SSE stream: `data: {chunk}` lines separated by blank lines, ending with `data: [DONE]`. These are the shapes observed on this gateway (from `docs/probe/probe.sh`; raw details in `research-notes.md`):

```
data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}
data: {"choices":[{"index":0,"delta":{"content":"Hel"}}]}
data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"toolu_01…","type":"function","function":{"name":"read_file","arguments":""}}]}}]}
data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": \"a.txt"}}]}}]}
data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]}}]}
data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"toolu_01…", ...}]}}]}
data: {"choices":[{"index":0,"delta":{...provider_metadata...},"finish_reason":"tool_calls"}],
       "usage":{"prompt_tokens":387,"completion_tokens":97,"cost":0.0000872,"surcharge_cost":0.0002,
                "prompt_tokens_details":{"cached_tokens":0},"cache_creation_input_tokens":0,
                "completion_tokens_details":{"reasoning_tokens":0}}}
data: [DONE]
```

What the accumulator must handle:

- **Tool calls arrive in pieces, keyed by `index`.** The first fragment carries `id` and `name`; later ones carry only argument text. With parallel calls, fragments of call 0 and call 1 can interleave, so key by `index` and never assume order.
- **`finish_reason` and `usage` arrived in the same final chunk in this probe.** Accept them in separate chunks too; usage can follow the finish reason.
- **`finish_reason` mapping:** `stop` → `StopEnd`, `tool_calls` → `StopToolUse`, `length` → `StopLength`. Some providers say `stop` even when the message has tool calls (opencode works around this explicitly), so **if there are tool calls, the stop reason is `tool_use`.**

### Neutral messages

```
Message{Role: user|assistant|tool, Blocks: [text | thinking | tool_call ...], Result (tool only)}
```

The harness's history is a `[]Message`. `EncodeRequest` translates it to wire JSON at the last moment. That keeps provider quirks in one file, and lesson 19 gets a single place to transform history when switching models. pi's `transform-messages.ts` exists for exactly that reason.

### Reasoning is opaque state

Reasoning ("thinking") arrives in two places: `delta.reasoning` (text to display) and `delta.reasoning_details` (structured fragments keyed by `index`). Observed on this gateway:

- **Claude Haiku 5.5 returns no thinking text at all.** It sends only a `signature`: `{"type":"reasoning.text","text":"","signature":"CAQSwwcK…","format":"anthropic-claude-v1"}`. Text and signature come in separate fragments; merge them by `index`.
- **Gemini returns readable thinking text with no signature.**
- **The provider verifies the signature when you send it back.** A tampered signature was rejected with `Invalid 'signature' in 'thinking' block`.

So store the merged `reasoning_details` array as raw JSON (`Thinking.Raw`) and send it back **byte for byte** on the assistant message. Don't parse it or rebuild it. Omitting it was also accepted in the probe; the likely cost (an inference, not measured) is that the model no longer sees its earlier reasoning during a tool loop.

### Usage and cost

- **`usage.prompt_tokens` is the total input**, including cache reads (`prompt_tokens_details.cached_tokens`) and cache writes (`cache_creation_input_tokens`).
- **The gateway reports cost directly:** `usage.cost` for inference. The account used for this tutorial also pays `usage.surcharge_cost` of $0.0002 per request for zero data retention and the provider allowlist. That surcharge is more than the inference cost of a small Haiku request.
- **If `cost` is missing, compute it from catalog prices** (`CostFromUsage`). Subtract cached and cache-write tokens from input before applying the normal rate.

### Retries: only before the first byte

Retry 429 and 5xx (plus 529, Anthropic's "overloaded") and network errors, but **only before any of the body has been read.** Retrying after text has streamed would show the user duplicated output and bill twice. A 402 (out of credits), a 400 (bad request, including **context overflow**: `prompt is too long: 599026 tokens > 200000 maximum`) and a 403 (model not allowlisted) are never retried. They surface as `Stop: "error"` with the gateway's message.

### The stream contract

```
Stream(ctx, req) <-chan StreamEvent
  text_delta*, thinking_delta*, toolcall_start/toolcall_delta*, ... , done (exactly once, last)
```

The consumer must read until the channel closes. `done.Message.Stop` tells it how the response ended. Cancelling `ctx` ends the stream promptly with `Stop: "aborted"` and keeps the partial blocks.

## The contract

Package `harness/internal/llm`. The supplied `types.go` defines these provider-neutral types, which the tests and fake server also use:

```go
type Role string
const (RoleUser Role = "user"; RoleAssistant Role = "assistant"; RoleTool Role = "tool")

type StopReason string
const (
    StopEnd StopReason = "stop"; StopLength StopReason = "length"; StopToolUse StopReason = "tool_use"
    StopError StopReason = "error"; StopAborted StopReason = "aborted"
)

type ToolCall struct { ID string `json:"id"`; Name string `json:"name"`; Args json.RawMessage `json:"args"` }
type ToolResult struct { CallID, Name, Content string; IsError bool }        // add json tags as in solution
type Thinking struct { Text string; Raw json.RawMessage }                     // Raw = merged reasoning_details array
type Block struct { Text string; Thinking *Thinking; ToolCall *ToolCall }     // exactly one set
type Message struct {
    Role   Role
    Blocks []Block
    Result *ToolResult // set iff Role == RoleTool
    Model  string; Usage *Usage; Stop StopReason; Err string // assistant metadata
}
func UserText(s string) Message
func (m Message) Text() string          // concatenated text blocks
func (m Message) ToolCalls() []ToolCall

type Usage struct {
    Input, Output, CacheRead, CacheWrite, Reasoning int // Input includes CacheRead + CacheWrite
    CostUSD, SurchargeUSD float64
}
func (u *Usage) Add(o Usage)

type ToolSpec struct { Name, Description string; Schema json.RawMessage }
type Request struct {
    Model, System string; Messages []Message; Tools []ToolSpec
    MaxTokens int
    Reasoning string          // "", "enabled", or an effort: "low", "medium", "high", ...
    Extra map[string]any      // merged into the top level of the body
}

type StreamEventType string
const (
    EventTextDelta StreamEventType = "text_delta"; EventThinkingDelta = "thinking_delta"
    EventToolCallStart = "toolcall_start"; EventToolCallDelta = "toolcall_delta"; EventDone = "done"
)
type StreamEvent struct {
    Type StreamEventType
    Text string     // delta text (text, thinking, or argument fragment)
    Index int       // tool-call index
    Call *ToolCall  // toolcall_start: ID and Name
    Message *Message // done
}
type Client interface { Stream(ctx context.Context, req Request) <-chan StreamEvent }
```

The full file, including JSON tags, is supplied in `support/internal/llm/types.go`.

Functions to write:

```go
func EncodeRequest(req Request) ([]byte, error)                         // wire.go
func CostFromUsage(u Usage, p Pricing) float64                          // cost.go
func NewGateway(baseURL, apiKey string, opts ...Option) *Gateway        // client.go; *Gateway implements Client
func WithRetry(max int, initial, cap time.Duration) Option
func WithHTTPClient(c *http.Client) Option
func WithPricing(p map[string]Pricing) Option
```

Provided in `support/`: `internal/llm/types.go`, `internal/llm/sse.go` (SSE framing), `internal/llm/fake` (scripted gateway), `cmd/harness` (chat REPL), `cmd/bench`.

## Build it

Continue from lesson 00 and copy this lesson’s `tests/` and `support/`. The message types and SSE reader are supplied in `internal/llm/`; implement the remaining functions in the contract above.

1. **Read the fake first.** `support/internal/llm/fake/fake.go` shows the exact chunk shapes your client will parse. `Turn` fields script text, thinking, tool calls (`Interleave: true` for interleaved fragments), errors (`Status`, `Header`, `Body`), and hangs (`HangAfter`). Every later lesson tests against it.

2. **Inspect the supplied types and SSE reader.** `types.go` gives the agent provider-neutral messages; `sse.go` handles event framing. Your client will still need to interpret each event and assemble the model’s response.

3. **`EncodeRequest`.** Tests: `-run TestEncodeRequest`.
   - System prompt as the first message, only if non-empty.
   - Assistant: `content` is `null` when there's no text. `tool_calls[].function.arguments` is a string, and `"{}"` for empty args. `reasoning_details` comes from `Thinking.Raw`.
   - User messages with one text block send `content` as a string. With several blocks, send an array of `{"type":"text","text":...}` parts; lesson 12 needs parts for cache markers.
   - `Reasoning: "enabled"` → `{"enabled": true}`; any other non-empty value → `{"effort": value}`.

4. **Streaming and accumulation (`Gateway.Stream`).** Tests: `-run 'TestStream|TestStop|TestUsage'`.
   - Run the request in a goroutine that owns the channel and always sends exactly one `done`, then closes.
   - Keep per-index state for tool calls: `id`, `name`, and an argument `strings.Builder`. Emit `toolcall_start` the first time an index appears.
   - Merge `reasoning_details` fragments by `index`. String fields concatenate (`text` arrives in one fragment, `signature` in another); `type`, `format` and `id` don't.
   - Build the final message with blocks in this order: thinking, text, tool calls (sorted by index).
   - Keep each call's `Args` valid JSON. No arguments becomes `{}`, and truncated JSON is stored as a JSON string (test: `TestToolArgsAlwaysValidJSON`). An empty `json.RawMessage` makes `json.Marshal` fail for the whole message. This was found in lesson 18, where a no-argument MCP tool silently dropped events from traces and messages from session files.
   - When the context is cancelled, return what you have with `Stop: StopAborted`.

5. **Retries and errors.** Tests: `-run 'TestRetry|TestNoRetry|TestContextOverflow'`.
   - Loop around `http.Client.Do`. On a retryable status, read and close the body, wait, try again. Errors become `*APIError{Status, Message}`, with `Message` taken from `{"error":{"message":...}}`.
   - Header precedence: `Retry-After-Ms`, then `Retry-After` (seconds or an HTTP date), then exponential backoff with jitter.
   - Make the wait a `select` on `ctx.Done()`, so Ctrl-C during a backoff returns immediately.

6. **Cost.** Tests: `-run 'TestCost'`. Use `usage.cost` when present, otherwise `WithPricing` and `CostFromUsage`.

When stuck, compare with `solution/internal/llm/client.go`.

## Run it

```sh
go test ./internal/llm/
go run ./cmd/harness                       # chat; try --model anthropic/claude-haiku-5.5 --reasoning enabled
```

## Demo

`go run ./cmd/bench` sends the same coding prompt to five models, three times each. Measured on this tutorial's account (2026-10-20):

```
MODEL                         OK   FIRST TOKEN  FIRST TEXT  TOK/S  REASONING TOK    COST/RUN
anthropic/claude-sonnet-5.5   3/3  0.83s        0.83s       202    0 (hidden)       $0.01492
anthropic/claude-haiku-5.5    3/3  1.02s        1.02s       320    0 (hidden)       $0.00079
openai/gpt-6-luna             3/3  16.88s       16.88s      118    1064 (hidden)    $0.00082
google/gemini-3.8-flash       3/3  9.70s        9.70s       224    1310 (hidden)    $0.00762
deepseek/deepseek-v4.1-flash  3/3  0.75s        14.08s      200    4492 (streamed)  $0.00486

time to first answer text (lower is better)
  anthropic/claude-sonnet-5.5      █▉                                       0.83s
  anthropic/claude-haiku-5.5       ██▍                                      1.02s
  openai/gpt-6-luna                ████████████████████████████████████████ 16.88s
  google/gemini-3.8-flash          ███████████████████████                  9.70s
  deepseek/deepseek-v4.1-flash     █████████████████████████████████▎       14.08s
```

What it shows:

- **Reasoning dominates latency.** GPT-6 Luna and Gemini think silently for 10–17 s before the first visible token. DeepSeek streams its reasoning, so the first token is fast but the answer text comes 14 s later. In an agent, every step pays this before the next tool call can start.
- **Reasoning is billed as output** and counts against `max_tokens`. During development, Haiku 5.5 sometimes spent a whole 1,500-token budget on hidden reasoning and returned `finish_reason: "length"` with **no text at all**. In other runs it used no reasoning. That run-to-run variance is why later comparisons use repeats.
- **The cheapest per token isn't the cheapest per task.** DeepSeek's per-token price is lowest, but it produced 4,500 reasoning tokens. Haiku and Luna came out cheapest per run.
- **Networks fail mid-stream.** One development run got `read tcp …: connection reset` 30 s into a DeepSeek stream. Your client reports this as `Stop: "error"` with partial content. It can't retry: text was already shown. The agent loop (lesson 02) decides what to do next.

Cost: about $0.10 per run of the benchmark.

## Exercises

1. Compare the same prompt with and without `--reasoning high` in the benchmark. How do reasoning tokens, latency, and cost change?
2. Add a stream idle timeout and test it with `fake.Turn{HangAfter: 2}`. What should the harness return to the user if the model stalls after producing partial text?

## Further reading

- Gateway Chat Completions, streaming, reasoning and tool calling: https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions
- pi's `ai` package and its design rationale: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/ (the "pi-ai" section)
- Codex request construction: https://openai.com/index/unrolling-the-codex-agent-loop/
- SSE format: https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation
