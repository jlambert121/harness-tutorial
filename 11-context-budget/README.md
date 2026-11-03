# Lesson 11: Context Budget and Pruning

## What you'll build

The harness learns:

- **its context budget:** window minus reserved output, read from the gateway catalog;
- **its token count:** bytes/4, calibrated against what the provider actually reports;
- **how to stay under the limit cheaply:** before each request, older tool output is cleared when the conversation nears the limit, while recent work stays intact.

There's also a live context meter in the TUI and a `/context` breakdown. The demo simulates 30 file-reading prompts: without pruning the session overflows at request 52, and with pruning it plateaus near 50k tokens. A live check shows bytes/4 underestimating real token counts by up to 1.7×.

## How the real harnesses do it

- **Context rot.** Anthropic's "Effective context engineering for AI agents" describes recall degrading as the window fills, and calls context an "attention budget". A full window costs more and works worse. It recommends **tool-result clearing** as the "lightest touch" form of compaction: once a tool result is far back in history, the model rarely needs its raw content again.
- **opencode** prunes after each loop. It walks backwards, skips the last 2 user turns, protects 40,000 tokens of recent tool output, and replaces older results with "[Old tool result content cleared]". It only prunes when at least 20,000 tokens would be cleared, and **pruning is off by default** in v1 (`compaction.prune`). Usable context = input limit − reserved, where reserved = min(20,000, max output tokens).
- **Claude Code** auto-compact "clears older tool outputs first, then summarizes". Anthropic's API offers the same thing server-side as `clear_tool_uses_20250919`: trigger at 100k input tokens, keep 3 tool uses, and `clear_at_least` "because clearing invalidates the cache".
- **pi** has a `transformContext` hook in its agent loop: a function that rewrites the messages sent, which is where pruning or injection goes. Its compaction triggers at `contextWindow − 16384`.
- **Codex** estimates 4 bytes per token and truncates tool output head and tail. Its history keeps the truncated version; the rollout file keeps the full payload.

## Design for our harness

### Budget and estimates

```
usable = window − reserve        reserve = min(20000, model's max output tokens)
estimate = ceil(bytes/4) × ratio  ratio  = EWMA of (provider prompt_tokens / our estimate)
```

The window comes from the catalog (lesson 00). `--context-window` overrides it, reserving at most a quarter of the overridden window for output; lesson 13 uses that to force overflow cheaply.

The calibration matters more than it looks. On Claude Haiku 5.5, measured below, Go source and JSON run about **2.3 bytes per token**, so bytes/4 reports 58% when the context is actually full. After the first response, the calibrator has the real ratio.

### Pruning is a request transform

```go
Config.Transform func(system string, tools []llm.ToolSpec, msgs []llm.Message) ([]llm.Message, map[string]any)
```

Before every request the agent passes the messages through `Transform` and emits a `context` event with what it returned. History is not modified: the session file and `History()` keep every byte.

`ctxmgr.Manager.Transform` prunes when the estimate exceeds 70% of usable context:

```
walk messages newest → oldest
  count user messages; inside the last 2 user turns: keep everything
  older tool results: keep until 40,000 tokens have been kept, then replace content with
                      "[Old tool result content cleared]"
never remove a message (a tool call without its result is an invalid request)
```

Two properties to keep:

- **Deterministic.** The same history always produces the same pruned request. That's what lets lesson 12's cache survive pruning, at least between prunes.
- **Idempotent.** Pruning an already-pruned history clears nothing more.

What gets cleared is raw tool output that the model has already read and acted on. If it needs the content again, it can re-read the file, and the marker tells it the content is gone.

### Seeing it

- **TUI status bar:** `context 9.0k/180k (5%)`, from the last request's real input tokens over usable. When pruning fires, an info cell says how much was cleared.
- **REPL `/context`:** an estimated breakdown by system prompt, tool definitions, user messages, assistant messages and tool results, like Claude Code's `/context`.

## The contract

`harness/internal/ctxmgr`:

```go
type Budget struct { Window, Reserve int }
func (b Budget) Usable() int
func NewBudget(info llm.ModelInfo, window int) Budget          // window > 0 overrides the catalog

type Calibrator struct{ ... }                                    // zero value ready; ratio starts at 1
func (c *Calibrator) Observe(estimated, actual int)              // ignores zeros
func (c *Calibrator) Ratio() float64
func (c *Calibrator) Estimate(s string) int
func (c *Calibrator) EstimateRequest(system string, tools []llm.ToolSpec, msgs []llm.Message) int

const ClearedMarker = "[Old tool result content cleared]"
func Prune(msgs []llm.Message, protect int) (out []llm.Message, clearedTokens int)  // copies; never mutates input

type Manager struct { Budget Budget; Threshold float64; Protect int; Cal Calibrator }
func NewManager(b Budget, threshold float64, protect int) *Manager
func (m *Manager) Transform(system string, tools []llm.ToolSpec, msgs []llm.Message) ([]llm.Message, map[string]any)
// info keys: "estimated", "usable", "window", "pruned_tokens", "ratio"
func (m *Manager) Observe(u llm.Usage)
```

`harness/internal/agent`: `Config.Transform`; `const ContextInfo EventKind = "context"`; `func (a *Agent) System() string`. The max-steps notice must be appended *after* the transform.

TUI: handle `ContextInfo`. Status shows `context <used>/<usable> (<pct>%)` once `usable` is known; token counts format as `9.0k` below 100k and `180k` above. `Data` values may arrive as `float64` after a JSON round trip (replay), so convert carefully. Show an info cell containing "pruned" and the amount.

Provided in `support/`:
- **`cmd/harness`**: fetches the model's window from the catalog, adds `--context-window`, and adds `/context`. An observer sink feeds usage back to the calibrator in every mode.
- **`cmd/ctxsim`**.

## Build it

Continue from lesson 10 and copy this lesson’s `tests/` and `support/`. The `internal/ctxmgr/` implementation is new; the harness wiring and simulator are supplied. You will also change the existing agent and TUI.

1. **Budget and calibrator.** Reserve space for the model’s reply and calibrate estimates against reported usage. Tests: `go test ./internal/ctxmgr -run 'TestUsable|TestCalibrator|TestEstimate'`.
2. **Prune.** Tests: `-run TestPrune`. Copy the slice and copy each `ToolResult` you change; the input belongs to the agent's history. Pruned content is a marker, so a second pass skips it.
3. **Manager.** Test: `-run TestManager`. Remember the last estimate so `Observe` can compare it with the real count.
4. **Agent hook.** Apply the context transform to each request without changing the stored history. Test: `go test ./internal/agent -run TestTransform`.
5. **TUI meter.** Test: `go test ./internal/ui/tui -run TestContextMeter`.

## Run it

```sh
go test ./...
go run ./cmd/harness --root /tmp/textkit --ui repl
> read every file and explain the package
> /context
```

## Demo

`go run ./cmd/ctxsim -live`. The simulation is offline: a scripted model reads one new 24 KB file per prompt through the real `read` tool. `-live` costs about $0.002.

```
30 prompts, each reading one 24 KB file, window 200000 (usable 180000)

no pruning         ▁▁▁▁▁▁▂▂▂▂▂▂▃▃▃▃▃▃▄▄▄▄▄▄▄▄▅▅▅▅▅▅▆▆▆▆▆▆▇▇▇▇▇▇████████✗  last request 208594 tokens  ✗ exceeds usable context at request 52
pruning            ▁▁▁▁▁▁▂▂▂▂▂▂▃▃▃▃▃▃▄▄▄▄▄▄▄▄▅▅▅▅▂▁▂▁▂▁▂▁▂▁▂▁▂▁▂▁▂▁▂▁▂▁▂  last request  50917 tokens
            (each column is one request; █ = usable context)

bytes/4 vs real tokens (anthropic/claude-haiku-5.5):
  Go source       40000 bytes  estimate  10000  actual  17244  → 2.32 bytes/token (ratio 1.72)
  English prose   27300 bytes  estimate   6825  actual   8717  → 3.13 bytes/token (ratio 1.28)
  JSON            16657 bytes  estimate   4165  actual   7218  → 2.31 bytes/token (ratio 1.73)
```

`ctxsim.html` draws both series against the usable line.

What it shows:

- **Without pruning, context grows linearly and the session dies.** At request 52 the request passes the usable context (window minus the 20k output reserve). The scripted model accepts anything, so the simulation continues; its last request, 208,594 tokens, is past the window itself. A real provider would reject it with `prompt is too long` and lesson 01's client would end the run. The total input billed grows quadratically (lesson 02).
- **With pruning it plateaus.** Once the 70% threshold is crossed, everything except the protected 40k tokens and the last two turns is cleared. The sawtooth alternates between "just read a new file" and "answered".
- **bytes/4 is a poor estimate on its own.** Code and JSON tokenize at about 2.3 bytes per token on this model. An uncalibrated meter would claim 58% when the context is full, and pruning would start too late. The calibrator corrects this after the first response (`/context` shows the current ratio).

What pruning can't do: if the *needed* content is old, clearing it loses it. Summarizing before discarding (lesson 13) fixes that. Pruning also breaks the prompt cache every time it changes what is sent (lesson 12 measures this).

## Exercises

1. Add a minimum amount to clear before pruning, then count how many distinct request prefixes a 30-prompt session produces. Lesson 12 implements this; what does delaying a prune trade for cache stability?
2. Preserve the most recent `read` result for each file instead of pruning by age alone. Compare what remains in context and how much room it uses.

## Further reading

- Anthropic, "Effective context engineering for AI agents": https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- Anthropic API context editing (`clear_tool_uses_20250919`): https://platform.claude.com/docs/en/build-with-claude/context-editing
- opencode overflow and pruning: `packages/opencode/src/session/overflow.ts`, `session/compaction.ts` (v1.x tags) in https://github.com/anomalyco/opencode
