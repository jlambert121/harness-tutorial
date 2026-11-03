# Lesson 00: Orientation

## What you'll build

You'll set up the Go module that becomes your harness. You'll also write its first piece: a client for the Vercel AI Gateway model catalog. The catalog gives you, for every model, the context window, per-token prices, and capability tags such as tool use, caching and reasoning. Later lessons read this data for cost accounting (lesson 01), context budgets (lesson 11) and model routing (lesson 19), so you won't hard-code any of it.

Before writing code, this lesson covers what a harness is and how the well-known ones differ.

## What a harness is

A coding agent combines a model with the program around it, the **harness**. The model produces responses; the harness manages the work around them. It does six jobs:

```
            ┌─────────────────────────────────────────────────────────────┐
            │                        HARNESS                              │
 user ──▶   │  input & commands ──▶ ┌───────────┐ ──▶ request ──▶ ┌───────┐ │
            │  (REPL / TUI / exec)  │   LOOP    │                 │ MODEL │ │
            │                       │ sample →  │ ◀── stream  ◀── │ (API) │ │
 events ◀── │  UI renders events ◀─ │ run tools │                 └───────┘ │
            │                       │ → repeat  │                           │
            │                       └─────┬─────┘                           │
            │        ┌─────────────┬──────┴──────┬──────────────┐          │
            │      TOOLS        CONTEXT        POLICY        PERSISTENCE    │
            │  read/edit/bash  prompt, budget, permissions,  sessions,      │
            │  MCP, subagents  caching,        sandbox,      memory,        │
            │                  compaction      hooks         snapshots      │
            └─────────────────────────────────────────────────────────────┘
```

1. **The loop.** Send the conversation to the model. If the model asks for tools, run them, append the results, and send again. Stop when it answers without calling a tool. Every harness has this loop at its core, and it is short. pi's `runLoop`, opencode's `runLoop` and Codex's `run_turn` all have the same shape.
2. **Tools.** These are how the model affects the world. Their names, schemas, descriptions and output formats are prompts too: the model only knows what they tell it.
3. **Context.** This is everything the model sees: system prompt, project instructions, history and tool output. The window is finite, and models get worse as it fills, so the harness budgets it, caches it and compacts it.
4. **Policy.** Rules for what the agent may do without asking: permission rules, a sandbox, and hooks.
5. **Persistence.** Sessions you can resume, memory that survives across sessions, and file snapshots you can undo.
6. **Interface.** A REPL, a full-screen TUI, or a headless mode for scripts and evals. Good harnesses keep the interface separate from the loop by having the loop emit events.

## How the real harnesses differ

They all share the loop. Where they differ is in how much harness to put around it.

| Harness | Language | Philosophy | Distinctive choices |
|---|---|---|---|
| **pi** (Mario Zechner) | TypeScript | Minimal. "If I don't need it, it won't be built." | 4 tools (read, write, edit, bash). System prompt plus tools under 1,000 tokens. The original design rejected MCP, subagents, plan mode, todo lists and permission prompts ("security theater"; run it in a container instead). Session files are trees. Later versions added MCP and compaction. |
| **opencode** | TypeScript (an earlier Go project of the same name became Charm's `crush`) | Batteries included, client/server | An HTTP server with an SSE event bus; the TUI is one client. A 9-strategy fuzzy edit matcher (v1). Snapshots in a shadow git directory. LSP diagnostics fed back to the model after edits. Agents defined in markdown. |
| **Codex CLI** (OpenAI) | Rust | Safety and protocol discipline | Shell commands run under OS sandboxes (macOS Seatbelt, Linux bubblewrap + seccomp). `apply_patch` edits in a custom diff format. Every request is an exact prefix of the next, for cache hits. Internally, a submission queue in and an event queue out. |
| **Claude Code** (Anthropic) | TypeScript (closed source) | Broad feature set, cache-driven design | Exact-match string edits. Layered CLAUDE.md memory. Hooks, skills, subagents, plugins, MCP with deferred tool loading. Its design rules come from prompt caching: never change tools mid-session, and add updates as reminders rather than editing the system prompt. |
| **Aider** | Python | Pair programmer, pre-dates tool calling | Edits arrive as text blocks (SEARCH/REPLACE, udiff, whole file), not tool calls. Its benchmarks on edit formats are still the best public data. A tree-sitter "repo map" ranked with PageRank. Auto lint and test after every edit. |

Two things to watch for as you go:

- **Most features are context management in disguise.** Subagents, skills, compaction, MCP tool search and memory files all exist to put the right tokens in the window and keep the wrong ones out.
- **Prompt caching shapes the architecture.** Providers charge about a tenth of the normal price for input tokens that repeat an earlier request's prefix. Harnesses are built to keep that prefix stable. Lesson 12 measures the effect.

## Design for our harness

You'll build a Go harness one capability at a time. See the [lesson index](../README.md#lessons) for the full sequence.

Decisions that apply throughout:

- **One API: the gateway's OpenAI-compatible Chat Completions endpoint.** The gateway also offers the OpenAI Responses API and the Anthropic Messages API. Chat Completions works across every provider family tested for this tutorial (Anthropic, OpenAI, Google, DeepSeek), so switching models is a one-flag change. You'll write the HTTP client yourself, without an SDK, so you see the wire format and can use the gateway's extra fields directly.
- **Provider-neutral messages internally**, translated to the wire format at the edge. This is pi's design, and lesson 19 shows why it matters.
- **The loop emits events; UIs subscribe to them.** A line REPL comes first and a TUI in lesson 08. Headless mode and evals use the same events.
- **Tests never touch the network.** Lesson 01 provides a fake model server that replays scripted responses.

## The contract

Package `harness/internal/llm`, file `catalog.go`:

```go
type Pricing struct {
    Input, Output, CacheRead, CacheWrite float64 // USD per token
}

type ModelInfo struct {
    ID               string   // "anthropic/claude-sonnet-5.5"
    Name             string
    Type             string   // "language", "embedding", "image", ...
    ContextWindow    int
    MaxTokens        int      // max output tokens
    Tags             []string // "tool-use", "reasoning", "implicit-caching", "explicit-caching", ...
    Pricing          Pricing
    Eligibility      string   // "eligible", "ineligible", or "" if fetched without a key
    IneligibleReason string   // e.g. "model_not_allowlisted"
}

func (m ModelInfo) Has(tag string) bool
func ParseCatalog(body []byte) ([]ModelInfo, error)
func FetchCatalog(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, error)
```

## Build it

### Setup

1. Install Go 1.26 or newer.
2. Create your module anywhere outside this tutorial directory:
   ```sh
   mkdir -p ~/src/my-harness && cd ~/src/my-harness
   go mod init harness
   ```
   The module path must be `harness`, because the provided tests and support code import `harness/internal/...`.
3. Get a gateway API key (Vercel dashboard → AI Gateway → API Keys) and `export AI_GATEWAY_API_KEY=...`.

**Every lesson works the same way:**
1. Read the lesson.
2. Copy its `tests/` and `support/` into your module: `cp -R <lesson>/tests/. <lesson>/support/. ~/src/my-harness/`. Replace `<lesson>` with the absolute path to that lesson directory.
3. Implement the contract until `go test ./...` passes.
4. Run the demo.

If you get stuck, `solution/` holds the reference module as of the end of the lesson. `diff -r 03-tool-design/solution 04-editing-files/solution` shows exactly what one lesson adds.

### Steps

1. **Look at the real data first.**
   ```sh
   curl -s https://ai-gateway.vercel.sh/v1/models | python3 -m json.tool | less
   ```
   The listing needs no authentication. Note that prices are **strings** in USD per token (`"input": "0.000002"`), and that some models also carry `input_tiers` (prices that change past a token threshold) and `regional` prices. Use only the base price.

2. **`ParseCatalog`** turns the response body (`{"data": [ ... ]}`) into `[]ModelInfo`. Tests: `TestParseCatalog`, `TestParseCatalogMissingPricing`, `TestParseCatalogBadPrice`.
   - A missing price is `0`. A price that isn't a valid number is an error. A silently wrong price would corrupt every cost figure later.
   - `model_eligibility` only appears in authenticated responses: `{"status":"ineligible","reason":"model_not_allowlisted"}`.

3. **`FetchCatalog`** does `GET {baseURL}/models`. Test: `TestFetchCatalogSendsAuthOnlyWithKey`, `TestFetchCatalogHTTPError`.
   - With a key, send `Authorization: Bearer <key>` and add `?include_availability=true`. The gateway then tells you which models your team may use. Teams can restrict models: on the account used to write this tutorial, only 40 of 477 models are allowed, and a request to any other model fails with HTTP 403 `RestrictedModelsError`.
   - On a non-200 status, return an error that includes the body. Gateway errors are JSON with a useful `message`.

## Run it

```sh
go test ./internal/llm/
go run ./cmd/models --limit 15
```

## Demo

`cmd/models` (from `support/`) lists tool-capable models your team can use. It sorts them by a blended price that weights input 10:1 over output, roughly the ratio of an agent workload, where the whole conversation is re-sent on every step. What you should see (account-specific; your list will differ):

```
MODEL                         CONTEXT  $IN/M  $OUT/M  $CACHE-READ/M  CACHING            REASONING  USABLE
deepseek/deepseek-v4.1-flash  1M       0.075  0.300   0.002          implicit           yes        yes
anthropic/claude-haiku-5.5    1M       0.100  0.500   0.010          explicit           yes        yes
openai/gpt-6-luna             1.1M     0.100  0.500   0.010          implicit+explicit  yes        yes
...
google/gemini-3.8-flash       1M       0.750  3.750   0.075          implicit           yes        yes
anthropic/claude-haiku-4.5    200k     1.000  5.000   0.100          explicit           yes        yes

15 of 39 matching models shown (477 in catalog).
```

Things to notice; each comes back in a later lesson:

- **Cache reads cost about a tenth of normal input.** An agent re-sends its whole history on every step, so caching is the biggest single lever on cost (lesson 12).
- **"implicit" vs "explicit" caching.** OpenAI, Google and DeepSeek cache automatically. Anthropic models cache only when the request asks for it (lesson 12).
- **Context windows differ by 5x** (200k vs 1M). That number drives the budget and compaction logic (lessons 11 and 13).
- **Try `--all`** to see ineligible models and the reasons.

Cost to run: free; the catalog endpoint isn't billed.

## Exercises

1. Add a `--provider anthropic` filter. The provider is the prefix before `/`.
2. Fetch `GET /v1/models/{creator}/{model}/endpoints` for one model. It lists every upstream provider serving that model, with its latency and throughput. The gateway uses this list for fallbacks; lesson 19 uses it.
3. Use `input_tiers` to compute what a 150k-token request costs on `anthropic/claude-haiku-5.5`. Above 100k tokens the price per token rises 5x.

## Further reading

- Vercel AI Gateway, OpenAI-compatible API: https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions
- Mario Zechner, "What I learned building an opinionated and minimal coding agent": https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
- Michael Bolin, "Unrolling the Codex agent loop": https://openai.com/index/unrolling-the-codex-agent-loop/
- Anthropic, "Effective context engineering for AI agents": https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- Aider, edit formats: https://aider.chat/docs/more/edit-formats.html
- `../research-notes.md`: the sourced facts behind every claim about real harnesses in this tutorial.
