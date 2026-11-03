# Lesson 12: Prompt Caching

## What you'll build

You'll make the harness cache-friendly and measure the effect:

- **Cache markers.** `cache_control` breakpoints placed by hand, or the gateway's automatic mode.
- **Session affinity**, so requests reach the provider that holds the cache.
- **Reminders** that deliver state changes without touching the system prompt.
- **A pruning boundary** that keeps the request prefix stable between prunes.
- **A cache hit rate in the status bar.**

The demo runs 90 eval runs: caching cuts cost about in half, and a "cache-hostile" harness pays *more* than one with caching off.

## How the real harnesses do it

Providers cache the processed prefix of a request. A later request that starts with exactly the same tokens pays about a tenth of the price for that part: Anthropic charges cache reads at 0.1× and cache writes at 1.25× (2× for a one-hour TTL). On Anthropic the cache lives 5 minutes by default, the prefix order is tools → system → messages, and a change anywhere invalidates everything after it. OpenAI, Google and DeepSeek cache automatically. Anthropic caches only where a request marks a breakpoint (at most 4).

An agent re-sends its whole history every step (lesson 02), so most of each request repeats the previous one. That makes caching the largest single cost lever, and it explains a set of design rules that otherwise look arbitrary. Thariq Shihipar's "Lessons from building Claude Code: prompt caching is everything" lists them:

- **Never add or remove tools mid-session.** Plan mode is a pair of tools (`EnterPlanMode`/`ExitPlanMode`) plus an instruction message, not a different tool set. MCP tools are loaded as deferred stubs in a stable order.
- **Put dynamic information in the next message, not in the system prompt.** Time, changed files and mode switches go in `<system-reminder>` blocks inside the next user message or tool result.
- **Don't switch models mid-session.** The cache is per model. To use another model, delegate to a subagent.
- **Compaction reuses the parent's exact prefix** (lesson 13).
- Bugs that broke their cache: timestamps in the static prompt, non-deterministic tool order, changing tool parameters. They alert on cache hit rate and treat drops as incidents.

**Codex** follows the same discipline: every request must be an exact prefix extension of the previous one. Config changes are *appended* as new messages (`<permissions instructions>`, `<environment_context>`). `prompt_cache_key` = session ID. An unstable MCP tool order once caused misses (PR #2611).

**opencode** marks the first 2 system messages and the last 2 messages.

**The gateway**:
- passes `cache_control: {type: "ephemeral"}` on content parts through to Anthropic, including on tool results (verified with a one-off probe and by this lesson's `cachelab` manual mode);
- offers `providerOptions.gateway.caching: "auto"`, which places markers itself (verified in `docs/probe/probe.sh`, case d);
- uses **session affinity** (`providerOptions.gateway.sessionId`) to route a conversation to the provider holding its cache. "AI Gateway can't move a cache from one model to another." (from the docs; not tested here)

## Design for our harness

### Markers

`Request.Cache` has three modes:

| Mode | Wire effect |
|---|---|
| `manual` (default) | `cache_control` on the system prompt and on the last two user/tool messages. Strings become `[{type:"text", text, cache_control}]` parts. |
| `auto` | `providerOptions.gateway.caching: "auto"`, merged with any other `providerOptions` |
| `off` | nothing (implicit-caching providers still cache) |

Why the last *two* messages? The newest marker caches this request. The one before it is where the *previous* request's marker was, so this request reads that prefix from cache and writes only the new suffix.

`Request.SessionID` (the session file's ID) sets `providerOptions.gateway.sessionId`.

### Reminders instead of system edits

```go
a.Remind("Plan mode is now active: do not edit files.")
// next user message: [<system-reminder>…</system-reminder>] [user text]
```

The system prompt is built once per session (lesson 06) and never changes. Lesson 14's `/mode` command uses `Remind`.

### Pruning vs. caching

Pruning (lesson 11) replaces old tool output with a marker. That changes an early message, which invalidates every cached token after it. If the prune point moved on every request, the cache would be lost on every request.

The fix is a **boundary** in `ctxmgr.Manager`:
- Tool results before the boundary are cleared; after it, they're kept.
- The boundary moves only when at least `MinClear` (20,000) tokens can be cleared beyond it, opencode's `PRUNE_MINIMUM`.
- Between moves, each request is a byte-identical extension of the previous one.

`TestManagerKeepsPrefixStableBetweenPrunes` checks that 20 growing requests produce a few large boundary jumps, with an unchanged prefix in between. The API-level equivalent is Anthropic's `clear_at_least` parameter, which exists "because clearing invalidates the cache".

### A hostile harness, for comparison

`--cache-hostile` (support code, `cmd/harness`) wraps the client to do the two things Claude Code's team lists as having broken their cache:
- prepend `Current time: <now>` to the system prompt on every request;
- shuffle the tool order.

## The contract

`harness/internal/llm`:

```go
type CacheMode string
const (CacheOff CacheMode = "off"; CacheAuto = "auto"; CacheManual = "manual")
type Request struct { ...; Cache CacheMode; SessionID string }
```

`EncodeRequest`:
- `CacheManual` marks the system message and the last two `user`/`tool` messages. The marker goes on the *last* content part.
- `CacheAuto` sets `providerOptions.gateway.caching`.
- A non-empty `SessionID` sets `providerOptions.gateway.sessionId`.
- These merge with `Extra["providerOptions"]`; they don't replace it.

`harness/internal/agent`: `Config.Cache`, `Config.SessionID` (passed into every request), and `func (a *Agent) Remind(text string)`. Reminders become blocks `"<system-reminder>\n" + text + "\n</system-reminder>"` placed before the user's text in the next user message.

`harness/internal/ctxmgr`:

```go
func Boundary(msgs []llm.Message, protect int) int          // index just past the last result Prune would clear
func ClearBefore(msgs []llm.Message, idx int) ([]llm.Message, int)
type Manager struct { ...; MinClear int }
func (m *Manager) Reset()                                    // forget the boundary (history replaced)
```

`Manager.Transform` keeps reporting `pruned_tokens`, but only on the request where the boundary moves.

TUI: the status bar adds `cache NN%` (last request's `CacheRead / Input`).

Provided in `support/`:
- **`cmd/harness`**: `--cache manual|auto|off` and `--cache-hostile`. It also sets `SessionID` and `MinClear = 20000`.
- **`cmd/cachelab`**.

## Build it

Continue from lesson 11 and copy this lesson’s `tests/` and `support/`. Work in the existing `internal/llm`, `internal/ctxmgr`, agent, and TUI code; the harness wiring and cache demo are supplied.

1. **Markers.** Tests: `go test ./internal/llm -run 'TestManualCache|TestCacheOff'`. Add a `CacheControl *cacheControl` field (`json:"cache_control,omitempty"`) to the text-part wire type. After building wire messages, walk backward and mark two user/tool messages.
2. **Agent.** Pass caching settings to each request and queue reminders in the next user message, keeping the system prefix stable. Tests: `go test ./internal/agent -run 'TestCacheOptions|TestRemind'`.
3. **Stable pruning.** Tests: `go test ./internal/ctxmgr -run 'TestClearBefore|TestManagerKeepsPrefix'`.
   - In `Transform`: when over the threshold, compute `Boundary`. Move `m.boundary` only if `ClearBefore(msgs[m.boundary:b])` would clear ≥ `MinClear`.
   - Always apply `ClearBefore(msgs, m.boundary)`, so earlier prunes persist. If the history shrinks below the boundary (after compaction), reset it.
4. **TUI.** Test: `go test ./internal/ui/tui -run TestStatusShowsCache`.

## Run it

```sh
go test ./...
go run ./cmd/harness --root /tmp/textkit --ui repl     # watch "(cached N)" in the usage line grow after the first turn
go run ./cmd/harness --root /tmp/textkit --ui repl --cache-hostile    # and stay at 0
```

## Demo

`go run ./cmd/cachelab`: 5 eval tasks × 3 repeats × 6 configurations = 90 runs, about $0.20.

```
CONFIG         PASS   INPUT TOK/RUN  CACHE READ  COST/RUN  TIME/RUN  CACHE READ %
haiku manual   15/15  20461          16843       $0.0016   12.7s     ████████████████████████······ 82%
haiku auto     15/15  20830          17960       $0.0015   12.1s     █████████████████████████····· 86%
haiku off      15/15  19373          0           $0.0029   12s       ······························ 0%
haiku hostile  15/15  20048          0           $0.0034   11.9s     ······························ 0%
luna implicit  15/15  15621          13302       $0.0011   20.9s     █████████████████████████····· 85%
luna hostile   15/15  15031          0           $0.0025   19s       ······························ 0%
```

What it shows:

- **Caching halves the cost of agent runs.** More than 80% of input tokens were cache reads, and cost per run fell from $0.0029 to $0.0016 (Haiku) and from $0.0025 to $0.0011 (Luna). Pass rates didn't move. The gain grows with session length, because the cached share of each request grows (lesson 02's re-sent history).
- **A cache-hostile harness costs more than no caching.** With manual markers but a changing prefix, every request pays the 1.25× cache-*write* price and never reads it back: $0.0034 vs. $0.0029 with caching off.
- **Implicit caching isn't automatic success.** Luna caches without any markers (85%), but the same hostile prefix drops it to 0%. Prefix stability is the harness's job on every provider.
- **Latency didn't change here.** These prompts are 3–8k tokens. Cache reads mainly cut time-to-first-token on long prompts. Expect a visible difference past roughly 50k tokens of context (an inference; not measured here).
- `auto` and `manual` performed the same. Manual control matters when you need markers somewhere the gateway's heuristic wouldn't put them, such as after a large stable tool result.

Costs above are inference cost (`usage.cost`). The account's flat $0.0002 per-request surcharge (lesson 01) comes on top and isn't affected by caching.

## Exercises

1. After lesson 14 adds `/model`, measure the cost and cache-read share before and after switching models mid-session. Why does a stable conversation prefix not guarantee a hit on the new model?
2. Track `cache_read / input` per request in traces. Introduce a changing system-prompt prefix and find where cache reuse drops.

## Further reading

- Claude Code team, "Lessons from building Claude Code: prompt caching is everything": https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/
- Anthropic prompt caching: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- Gateway automatic caching and session affinity: https://vercel.com/docs/ai-gateway/models-and-providers/automatic-caching, https://vercel.com/docs/ai-gateway/models-and-providers/session-affinity
- Codex and cache-stable prompts: https://openai.com/index/unrolling-the-codex-agent-loop/
