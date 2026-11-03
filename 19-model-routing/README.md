# Lesson 19: Model Routing

## What you'll build

Model choice as a harness decision, not just a flag:

- **Roles.** A main model (`--model`) and a small model (`--small-model`). The small model already runs lesson 16's explore subagent; now it also names sessions in the background.
- **`internal/route`.** A history transform for switching models mid-session (`TransformForModel`), two routers (`Heuristic`, `Escalating`), and the title job (`Title`).
- **Agent hooks.** `Config.PickModel(step, history)` chooses the model before every request, `Config.PrepareForModel` adapts the history to that model, and `Config.Fallbacks` asks the gateway to try other models if the main one fails. A `model_change` event records every switch.
- **`--router none|heuristic|escalate`** and `--fallback` in `cmd/harness`. `/model` (from lesson 14) now pins the model: a manual choice outranks the router.

The demo runs the eval suite (lesson 07) with a fixed strong model, a fixed cheap model and both routers, and compares pass rate, cost and cache hits.

**How is the right model chosen for a task?** In the harnesses below, mostly by *role*, not per task: the user picks the main model, and the harness sends fixed side jobs (titles, summaries, exploration, review) to cheaper models. Per-request routers exist (Cursor Auto, OpenRouter's auto router), but an agent session pays for every switch with a cold cache and lost thinking. The routers that suit agents decide once per run or escalate on evidence.

## How the real harnesses do it

| | Main model | Cheaper/background roles | Subagents | Other |
|---|---|---|---|---|
| **Claude Code** | `--model` / `/model` > `ANTHROPIC_MODEL` > `model` setting | `ANTHROPIC_DEFAULT_HAIKU_MODEL` is "also used for background functionality" (summaries); `ANTHROPIC_SMALL_FAST_MODEL` is deprecated | `model:` per subagent: call parameter > frontmatter > `CLAUDE_CODE_SUBAGENT_MODEL` > main | `opusplan`: Opus in plan mode, Sonnet for execution. The `PreModelSwitch` hook can block a switch. |
| **Codex** | `model`, `model_reasoning_effort`, `plan_mode_reasoning_effort` | `review_model` (for `/review`), `memories.extract_model` | `agents.default_subagent_model` | No separate compaction model was found. |
| **opencode** | `model`, per agent | `small_model`, used for titles; compaction uses the session's model | inherit from the primary agent that called them | |
| **Aider** | architect (main) model proposes a solution | `--weak-model`: commit messages, summaries | — | `--editor-model` turns the architect's proposal into edits |
| **pi** | built for switching providers mid-session | — | — (no subagents) | `transform-messages.ts` adapts history to the new model (below) |

**Automatic routers:**
- **Cursor Auto** picks per request, and the choice "can vary between turns". The router is a classifier trained on more than 600k requests, reports about 60% savings, and is explicitly cache-aware: it counts the cache misses its own switches cause.
- **OpenRouter `openrouter/auto`** has used a task-type classifier plus a 7-day spend ranking since 2026-08-10. The chosen model comes back in the response's `model` field.
- **RouteLLM** chooses strong or weak per prompt, trained on Chatbot Arena preferences. At 95% of GPT-4's quality it cut cost by more than 85% on MT-Bench. Like RouterBench, it is single-turn.
- **SWE-Router** (agents, multi-turn) finds that routing on the prompt alone hits an error floor. It runs a cheap model for a few turns and then decides whether to escalate.
- **Vercel's routing guide** (seen via a search summary): heuristics first, an LLM classifier only below 0.6 confidence, escalation only on a verifiable failure.

**Why a switch costs something:**
- **The cache is per model.** Anthropic needs an exact prefix match, OpenAI lists the model as part of the cache key, and the gateway docs say "AI Gateway can't move a cache from one model to another." After a switch, the whole prefix is written again at cache-write prices (lesson 12: 1.25× input on Anthropic, against 0.1× for a read).
- **Thinking is signed for the model that produced it.** Lesson 01's probe sent back a tampered signature and got `Invalid 'signature' in 'thinking' block`. A real per-turn router (UncommonRoute, issue #20) hit `400 Invalid signature in thinking block` after a switch; its fix was to pin the session once signed thinking exists. pi instead **drops** thinking when provider, API and model don't all match: a signature only works for its model, and thinking replayed as text "makes the new model treat it as part of the visible reply."
- **Tool-call IDs differ by provider.** OpenAI Responses IDs run to 450+ characters and contain `|`; Anthropic requires `^[a-zA-Z0-9_-]+$` and at most 64 characters. pi normalizes them.

## Design for our harness

```
each step of a run:
  maybe compact (lesson 13)
  m = PickModel(step, history)       "" or the current model = no change
  if m changed: SetModel(m), emit model_change {from, to}
  msgs = PrepareForModel(history, model)    = route.TransformForModel
  request{model, msgs, Fallbacks}     → providerOptions.gateway.models
```

- **Titles.** After the first prompt of an interactive session, a goroutine asks the small model for a title of at most 6 words (30 output tokens) and appends a `session_info` entry. Headless `exec` skips it, or every eval run would pay for one more request.
- **`TransformForModel` runs on every request, not only after a switch.** It is deterministic, so with one model the prefix stays byte-identical and cached. It:
  - drops thinking blocks from assistant messages whose `Model` differs from the target;
  - rewrites tool-call IDs that don't match `^[a-zA-Z0-9_-]{1,64}$` to `call_<sha1 prefix>`, the same way in the call and in its result;
  - drops aborted or failed assistant turns that have no tool calls.
  `Message.Model` comes from the stream's `model` field. Checked on this gateway: it echoes the requested ID (`anthropic/claude-haiku-5.5`, `google/gemini-3.8-flash`, `openai/gpt-6-luna`). With fallbacks it would name the model that actually answered, which is what the transform needs.
- **`Heuristic`** decides at step 1 of each run, from that run's prompt: words such as `race`, `concurrency`, `performance`, `refactor`, `debug`, `design`, `spec`, `why`, or a prompt over 600 characters, go to the main model; everything else to the small one. A choice at step 1 happens before the first request of a headless run, so it costs no cache. In an interactive session each new prompt is a new run, and a different choice there does cost a cold cache.
- **`Escalating`** starts the session on the small model and switches to the main one, for good, after 3 consecutive failed tool results (counted back to the last success or user message). It never switches back: going back would pay a second cold cache and drop the main model's thinking. This is SWE-Router's idea with a crude signal.
- **`/model` pins.** Without the pin, `/model X` was overridden by the router on the very next request (`Escalating` returned the small model). A bug found while writing this lesson; `TestModelCommandOverridesRouter` pins it.
- **Fallbacks** go to the gateway as `providerOptions.gateway.models`, tried in order if the main model fails. `TestFallbackModels` checks the wire format; the fallback itself was not triggered live here.
- **Not built:** a per-request classifier. It adds a model call before every step and switches more often. I found no measurement of classifier latency in an agent loop; that it hurts is an inference.

## The contract

`harness/internal/route`:

```go
func TransformForModel(msgs []llm.Message, target string) []llm.Message
type Router interface{ Pick(step int, history []llm.Message) string }
type Heuristic struct{ Small, Main string /* + unexported state */ }
func (h *Heuristic) Pick(step int, history []llm.Message) string
type Escalating struct{ /* unexported */ }
func NewEscalating(small, main string, streak int) *Escalating
func (e *Escalating) Pick(step int, history []llm.Message) string
func (e *Escalating) Escalated() bool
func Title(ctx context.Context, c llm.Client, model, firstPrompt string) (string, error)
```

`harness/internal/agent`:

```go
// Config gains:
PickModel       func(step int, history []llm.Message) string
PrepareForModel func(msgs []llm.Message, model string) []llm.Message
Fallbacks       []string
const ModelChange EventKind = "model_change" // Data: from, to
```

`harness/internal/llm`: `Request.Fallbacks []string`, encoded as `providerOptions.gateway.models`.

`support/` adds `cmd/harness` (flags, title job, the `/model` pin, `TestModelCommandOverridesRouter`) and `cmd/routelab`.

## Build it

Continue from lesson 18 and copy this lesson’s `tests/` and `support/`. The `internal/route/` implementation is new; the harness wiring and routing demo are supplied. You will also change the existing LLM and agent code.

1. **Fallbacks on the wire.** Test: `go test ./internal/llm -run TestFallbackModels`. Merge `models` into the existing `providerOptions.gateway` object (caching and session options live there too).
2. **`TransformForModel`.** Tests: `-run TestTransform`. Copy each message's block slice before filtering it, or you'll edit the agent's history in place. Keep one ID map for the whole call so a call and its result get the same new ID.
3. **Routers.** Choose a model for each run or escalate after failed tool calls. Tests: `-run 'TestEscalatingRouterNeverDowngrades|TestHeuristicRouter'`. The solution guards their state with a mutex.
4. **`Title`.** Use the small model for a background session title, illustrating model choice by role. Test: `-run TestTitleUsesSmallModel`.
5. **The agent hook.** Test: `go test ./internal/agent -run TestPickModelPerStep`. Pick before building the request, after the compaction check, and emit `ModelChange` only when the model actually changes.
6. **Wire it up.** Copy `support/cmd/harness`. Test: `go test ./cmd/harness -run TestModelCommandOverridesRouter`.

## Run it

```sh
go test ./...
cp -R <path-to>/02-the-agent-loop/sample /tmp/sample
go run ./cmd/harness --root /tmp/sample --router escalate --trace /tmp/route.jsonl
> Add a Timeout field (time.Duration, json "timeout") to Config, default it to 30 seconds in Defaults, and include it in the line main prints.
grep model_change /tmp/route.jsonl
go run ./cmd/harness --root /tmp/sample --fallback openai/gpt-6-luna
> /model anthropic/claude-haiku-5.5
```

## Demo

`go run ./cmd/routelab`: 16 tasks × 3 repeats × {fixed strong (Sonnet 5.5), fixed cheap (Haiku 5.5), heuristic router, escalating router} = 192 runs, about $1.30. It writes `routelab.html` with the per-run detail.

```
CONFIG             PASS   RATE ± SD  MEAN COST  MEAN TURNS  MEAN TIME  ERRORS
fixed strong       48/48  100% ± 0   $0.0182    4.4         15s        0
fixed cheap        47/48  98% ± 4    $0.0018    5.4         18s        0
heuristic router   46/48  96% ± 4    $0.0047    4.9         13s        0
escalating router  47/48  98% ± 4    $0.0028    5.1         14s        0

CONFIG             RUNS ON STRONG MODEL  STEP-1 PICKS  MID-RUN SWITCHES  CACHE READ %  TOTAL COST
fixed strong       48/48                 0             0                 87%           $0.87
fixed cheap        0/48                  0             0                 86%           $0.09
heuristic router   6/48                  42            0                 86%           $0.22
escalating router  3/48                  0             3                 85%           $0.14
  fail fixed cheap/14-spec-in-docs rep 2
  fail heuristic router/07-refactor-rename rep 0
  fail heuristic router/07-refactor-rename rep 1
  fail escalating router/07-refactor-rename rep 1
```

`STEP-1 PICKS` are changes made before the first request of a run (free); `MID-RUN SWITCHES` happen after the cache is warm. `RUNS ON STRONG MODEL` counts runs that used the main model at all.

What it shows:

- **On this suite, the cheap model alone is the best deal.** Haiku passed 47/48 at a tenth of Sonnet's cost ($0.09 vs $0.87 for all 48 runs). The suite is near-saturated (lesson 07), so neither router can show a quality gain; what's left to compare is cost.
- **The heuristic paid for the wrong tasks.** Its keywords sent 11-semver-compare (`precedence`, `spec`) and 13-perf-dedupe (`slow`) to Sonnet, tasks Haiku passes anyway, and kept 12-race-worker-pool on Haiku because its prompt says "wrong total under load" and never "race". In total it cost $0.13 more than fixed cheap and passed one task fewer. All 42 other choices happened at step 1, before any cache existed.
- **Escalation was rare and cheap.** 3 of 48 runs hit 3 consecutive failed tool results and switched to Sonnet mid-run; the total was $0.14, $0.05 above fixed cheap. The cache-read share hardly moved (85% vs 86%); 45 of 48 runs never switched.
- **The failures cluster on one task.** 07-refactor-rename failed for both routers (on Haiku, for the heuristic). The traces weren't kept, so the cause is unconfirmed; in lesson 07 the same task failed because BSD `sed` doesn't support `\b`. In the first run of this demo the counts were fixed cheap 47/48, heuristic 47/48, escalating 48/48, every miss on 07. One or two failures out of 48 is within run-to-run noise here.
- **What would change the result** (an inference): a harder suite where the cheap model fails often. Then escalation's evidence-based switch should beat both a fixed cheap model (quality) and a keyword heuristic (which can't see the failure coming).

## Exercises

1. Estimate the cost of rewriting the prefix before escalating to the main model. Compare the estimate with the observed switching cost in routelab.
2. Replace the keyword router with a classifier and compare pass rate, cost, and latency. Does an extra classification call pay for itself?
3. After a model switch with signed thinking, compare dropping that thinking with refusing to switch. What context does each policy preserve?

## Further reading

- Claude Code model configuration: https://code.claude.com/docs/en/model-config, environment variables: https://code.claude.com/docs/en/env-vars, subagents: https://code.claude.com/docs/en/sub-agents
- Codex config reference: https://learn.chatgpt.com/docs/config-file/config-reference
- opencode config and agents: https://opencode.ai/docs/config/, https://opencode.ai/docs/agents/
- Aider chat modes and options: https://aider.chat/docs/usage/modes.html, https://aider.chat/docs/config/options.html
- Cursor's router: https://cursor.com/blog/router
- OpenRouter auto router: https://openrouter.ai/docs/guides/routing/routers/auto-router
- RouteLLM: https://arxiv.org/abs/2406.18665. SWE-Router: https://arxiv.org/abs/2607.00053. RouterBench: https://arxiv.org/abs/2403.12031
- Thinking and signatures: https://platform.claude.com/docs/en/build-with-claude/thinking. UncommonRoute issue #20: https://github.com/CommonstackAI/UncommonRoute/issues/20
- pi's cross-provider transform: `packages/ai/src/api/transform-messages.ts` in https://github.com/earendil-works/pi
