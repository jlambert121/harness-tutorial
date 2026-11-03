# Lesson 13: Compaction

## What you'll build

When the context nears its limit, the harness replaces older history with a model-written summary and keeps the recent messages verbatim. It includes:

- **Split rules** that never orphan a tool result.
- **A structured handoff template.**
- **Iterative summaries**, where a second compaction updates the first instead of starting over.
- **Re-injection** of project context and recently used files, sized to the window.
- **Recovery from a provider's "prompt is too long" error.**
- **Durable compaction entries** in the session file.
- **`/compact [focus]`.**

The demo is one long session of 13 prompts under a 24k window. Without compaction, the last 5 prompts fail. With it, all 13 succeed, and the final answer depends on a fact from prompt 1 that survived two compactions.

## How the real harnesses do it

| | Trigger | What's summarized | Summary format | After |
|---|---|---|---|---|
| **Codex** | `min(config, 90% of window)`, checked after each sampling request; also pre-turn and async | everything except initial context and recent user messages (≤20k tokens kept) | "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM…" | the summary is prefixed "Another language model started to solve this problem…"; for OpenAI providers, a server-side opaque compaction item |
| **pi** | `contextTokens > window − 16384`, or `/compact`, or a context-overflow error (one retry) | everything except the last ~20k tokens; **never cuts at a tool result** | Goal / Constraints & Preferences / Progress (Done, In Progress, Blocked) / Key Decisions / Next Steps / Critical Context; the transcript is serialized as `[User]: … [Assistant tool calls]: read(path=…)` so the summarizer doesn't continue the conversation | a `CompactionEntry`; **originals never deleted**; later compactions update the previous summary |
| **opencode** | usable = input limit − reserved; checked after every step; overflow error | serialized transcript (tool outputs cut at 2,000 chars); keeps a tail of clamp(25% usable, 2k–15k) | Objective / Important Details / Work State / Next Move / Relevant Files; `<prior-summary>` for incremental updates ("anything you do not carry into the new summary is lost") | adds "Continue if you have next steps…" |
| **Claude Code** | about the window boundary (`/autocompact` to change it); clears old tool outputs first, then summarizes | the summarize call **reuses the parent's exact system prompt, tools and messages** and appends the compaction prompt as a user message, so it hits the cache | about a 20k-token summary | re-injects CLAUDE.md, auto memory, the plan, git status, up to 5 recent files (≤5k tokens each), and invoked skills; stops after repeated thrashing |

Common ground:

- **Summarize, but keep the tail verbatim.** The model needs exact recent context (the error it just saw), not a description of it.
- **Use a structured template** so the summary is a handoff: goal, state, decisions, next steps.
- **Re-attach what the summary can't carry:** instructions and the files being worked on.
- **Append, never delete.** The session log keeps everything.

## Design for our harness

```
before each request:  estimate > 0.9 × usable  (and not thrashing)  ──▶ Compact
provider error "prompt is too long" (once per run)                   ──▶ Compact, retry the step
/compact [focus]                                                     ──▶ Compact

Compact(history):
  cut = SplitPoint(history, KeepRecent)        walk back until KeepRecent tokens, then forward past tool results
  head, tail = history[:cut], history[cut:]
  request = same System + same Tools + head + [user: compaction prompt]   tool_choice: none
  new history = [user: Context + <conversation-summary>…</conversation-summary> + recent files] + tail
  session: append {"type":"compaction","data":{"history":[…]}}         resume starts from here
```

- **The split may fall inside one long turn.** It skips forward past tool results, so the tail starts at a user or assistant message. The first version required a *user* message at the cut, so a headless run with a single prompt could never be compacted ("nothing to compact"). Found while building the demo; `TestSplitPointInsideOneLongTurn` pins it.
- **The summarize request is the session's own prefix.** Same system prompt, same tools, the head of the same history, same cache mode, and then the instructions. The provider reads the head from cache (Claude Code's pattern). `tool_choice: "none"` stops the summarizer from calling tools. Max output is min(4096, max(KeepRecent, 1024)). One catch: `ShouldCompact` and `Compact` see the unpruned history, so after lesson 11's pruning has cleared old tool results, the summarize request's head differs from what was cached, and the cache hit stops at the first pruned message.
- **The template** is `prompts/compact.md`: Goal / Constraints and preferences / Progress (Done, In progress, Blocked) / Key decisions / Relevant files / Next steps. When the head already starts with a `<conversation-summary>`, `prompts/update.md` asks for an update: carry forward what's still true, because "anything you do not carry … is lost".
- **Re-injection** sends the first-message `Context` (environment, AGENTS.md) again, plus up to 5 recently read or edited files (`ReadTracker.Recent`). They're sized to the window: per file at most min(5000, KeepRecent/4) tokens, in total at most KeepRecent/2; larger files are listed by path only. With larger per-file limits, a 24k window refilled itself right after compacting (measured while building this). The file the model read last is usually the file it just finished with.
- **The thrash guard** lives in support code. If the last compaction left the context nearly as large as before (the new estimate isn't 20% above the post-compaction size), don't compact again; let the request go and, if needed, overflow.
- **Overflow recovery.** `IsOverflow` matches the gateway's `prompt is too long` (lesson 00's probe) and similar messages. After one compaction and retry, a second overflow is a real error.
- **Simulated windows.** `--context-window N` (lesson 11) plus a client wrapper that fails requests estimated above N with the provider's error text. That makes overflow and recovery testable at small sizes and low cost.

## The contract

`harness/internal/ctxmgr`:

```go
var ErrNothingToCompact error
type Compactor struct { Client llm.Client; Model string; KeepRecent int; Cache llm.CacheMode }
func NewCompactor(client llm.Client, model string, keepRecent int) *Compactor
type CompactOptions struct { Focus, Context string; RecentFiles []string }
type CompactResult struct { Summary string; History []llm.Message; Cut int; Usage *llm.Usage }
func (c *Compactor) Compact(ctx context.Context, system string, tools []llm.ToolSpec, msgs []llm.Message, opts CompactOptions) (*CompactResult, error)
func SplitPoint(msgs []llm.Message, keepTokens int) int
func IsOverflow(err string) bool
func (m *Manager) Need(est int) bool             // est > 0.9 × usable
```

`harness/internal/agent`:
- **Config hooks:** `Config.ShouldCompact(system, tools, msgs) bool`, `Config.Compact(ctx, system, tools, msgs, focus) ([]llm.Message, error)`, `Config.IsOverflow(err string) bool`, `Config.OnCompact(history)`.
- **On-demand compaction:** `func (a *Agent) Compact(ctx, focus string, sink Sink) error`.
- **Event:** `const Compaction EventKind = "compaction"`, with Data `reason` (threshold | overflow | manual), `before` and `after`.

`harness/internal/session`: `func (s *Store) AppendCompaction(history []llm.Message) string`. `Messages` restarts at the latest compaction on the path.

`harness/internal/tools`: `func (t *ReadTracker) Recent(n int) []string`, newest first, no duplicates.

TUI: a `compacted N messages into M (reason)` info cell.

Provided in `support/`:
- **`internal/ctxmgr/prompts/{compact,update}.md`**: embed with `//go:embed prompts/*.md`.
- **`cmd/harness`**: compaction wiring, thrash guard, `--no-compact`, the window guard and `/compact`.
- **`cmd/tinywindow`**.

## Build it

Continue from lesson 12 and copy this lesson’s `tests/` and `support/`. Work in the existing context manager, agent, session, tools, and TUI packages; the compaction prompts, harness wiring, and demo are supplied.

1. **SplitPoint.** Test: `go test ./internal/ctxmgr -run TestSplitPoint`. Keep a recent tail without separating a tool result from its call; otherwise the next model request can contain invalid history.
2. **Compact.** Tests: `-run 'TestCompact|TestIterative'`.
   - Detect a prior summary with a regexp over the head's first message.
   - Build the replacement history as one user message (Context, then summary, then files) followed by the tail.
   - `TestCompactSummarizesPrefixWithParentRequest` checks that the request is the session's prefix plus the prompt.
3. **IsOverflow.** Recognize a context-overflow error so the agent can compact and retry once. Test: `-run TestIsOverflow`.
4. **Agent hooks.** Tests: `go test ./internal/agent -run 'TestCompaction|TestOverflow'`. Track `overflowRetried` per run, and on a successful overflow compaction, `continue` the loop without consuming the step's outcome.
5. **Session and tracker.** Persist the compacted history for resume and track recent files for re-injection. Tests: `go test ./internal/session -run TestMessagesRestart` and `go test ./internal/tools -run TestReadTrackerRecent`.
6. **TUI.** Test: `go test ./internal/ui/tui -run TestCompactionShown`.

## Run it

```sh
go test ./...
go run ./cmd/harness --root /tmp/textkit --ui repl --context-window 24000
> (work for a while)
> /compact focus on the failing test
```

## Demo

`go run ./cmd/tinywindow`. Twelve ~7 KB design notes, one session with 13 prompts: prompt 1 reads part 1 and asks for the release codename, prompts 2–12 read part N and summarize it, prompt 13 asks "What was the release codename from part 1?". The codename appears once, in part 1. Haiku 5.5, 24k simulated window, 3 runs per configuration, about $0.05.

```
CONFIG               RUN  PROMPTS ANSWERED  COMPACTIONS  ERRORS  FINAL ANSWER  COST
24000 + compaction   1    13/13             2            0       BLUEFIN ✓     $0.0093
24000 + compaction   2    13/13             2            0       BLUEFIN ✓     $0.0093
24000 + compaction   3    13/13             2            0       BLUEFIN ✓     $0.0092
24000 no compaction  1    8/13              0            5       -             $0.0060
24000 no compaction  2    8/13              0            5       -             $0.0061
24000 no compaction  3    8/13              0            5       -             $0.0064

one compacted session, request by request (window 24000):
  prompt 1        1524 ███             5907 ████████████
  prompt 2        5189 ██████████      7435 ███████████████
  …
  prompt 6       14437 ██████████████████████████████
    ▲ compaction (threshold): 23 messages → 9
                  8068 ████████████████
  …
  prompt 10      15708 ████████████████████████████████
    ▲ compaction (threshold): 25 messages → 9
                  7992 ████████████████
  prompt 13      13169 ███████████████████████████
```

What it shows:

- **Without compaction, every prompt from 9 on fails** with `prompt is too long`. With compaction, the session runs all 13 prompts: two compactions each brought the context from about 15k back to 8k.
- **The fact from prompt 1 survived two summaries.** The second compaction updated the first summary (`<prior-summary>`) and didn't start over, so "BLUEFIN" was carried forward both times. That's what the iterative update prompt is for.
- **It costs about 55% more** ($0.0093 vs $0.0060), but the comparison is 13 answered prompts against 8: two summarize calls and the re-injected context are the price of a session that keeps working.
- **Two earlier versions of this demo failed instructively:**
  - On eval tasks, a single-prompt run couldn't compact at all (the split rule above).
  - With a re-injection budget closer to Claude Code's (up to 3k tokens per file at this window), a small window refilled immediately after compacting, so the context *grew* (14.8k → 15.2k).
  - Summaries and re-injection have a fixed cost. Compaction only pays when the summarized head is much larger than what replaces it.

## Exercises

1. Use a smaller model for summaries. Compare cost and whether the early BLUEFIN fact survives repeated compactions.
2. Start compaction in the background. What must happen to messages added after the summary request begins but before it finishes?

## Further reading

- Codex compaction: `codex-rs/core/src/compact.rs` and `prompts/templates/compact/` in https://github.com/openai/codex
- pi compaction: `packages/coding-agent/docs/compaction.md` in https://github.com/earendil-works/pi
- Claude Code: what survives compaction: https://code.claude.com/docs/en/context-window
- Claude Code team on cache-aware compaction: https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/
