# Lesson 08: Terminal UI

## What you'll build

A full-screen terminal UI built with Bubble Tea. It shows:

- a scrolling transcript of user messages, streamed answers and tool cells
- a status bar with the model, current turn, context size and session cost
- y/n dialogs for command confirmation
- an input line that stays usable while the agent works: Enter during a run **steers** the agent, Esc cancels

The UI is built only from the events the loop already emits. The demo proves it: a recorded trace replays in the TUI with no model and no tools.

## How the real harnesses do it

Every serious harness puts a protocol between the agent core and the screen:

- **Codex.** The core takes `Submission{id, op}` on a bounded queue (512 entries) and emits `Event{id, msg}` on an unbounded one. Ops are user turns, interrupts and approval answers. The TUI (ratatui) is now a client of the **app-server**, a JSON-RPC protocol over stdio with `thread/start`, `turn/start`, `turn/interrupt` and `item/started → item/*/delta → item/completed` notifications. Approvals are requests from the server to the client; the turn pauses until the client answers.
- **opencode.** The core is an HTTP server with an SSE event bus (`GET /event`). The TUI (OpenTUI + Solid) is one client; `serve`, `attach` and `web` are others. Even the built-in TUI runs the server in a worker and talks to it over RPC.
- **pi.** The agent emits `agent_start … message_update … tool_execution_end … agent_end` events, and its own `tui` package renders them. Zechner's blog explains choosing **scrollback** (append to normal terminal output, keeping native scroll and search) over **full screen**. The current version defaults to full screen anyway, with its own scrolling and search. Rendering is differential: redraw from the first changed line, wrapped in synchronized-output escape codes (`\x1b[?2026h … l`) to avoid flicker.

What this buys them:

- **Multiple front ends** with no changes to the core: TUI, IDE extension, web, headless exec.
- **Testability:** feed events, assert on the screen.
- **Remote and async operation:** opencode's `attach`, Codex's app-server.

### Steering vs. follow-up

A user who types while the agent works wants one of two things:

- **Steering** ("also check the README"): pi injects it **after the current tool batch** finishes, before the next model call. Codex drains pending input at the start of each sampling request. In both, already-requested tools still run.
- **Follow-up** ("then write a changelog"): pi queues it until the agent would otherwise stop.

Our `Agent.Steer` handles both cases with one queue. Queued messages go in after the tool batch. If the model has just answered with no tool calls, they start a new turn instead of ending the run.

## Design for our harness

```
      ┌───────────── tea.Program (one goroutine owns all UI state) ─────────────┐
keys ─▶ Update(KeyPressMsg) ── Enter idle ──▶ go agent.Run(ctx, text, sink) ─────┐
      │                       Enter running ─▶ agent.Steer(text)                 │
      │                       Esc ──────────▶ cancel()                           │
      │ Update(EventMsg) ◀──────────── p.Send(EventMsg{e}) ◀── sink ◀────────────┤
      │ Update(confirmMsg) ◀── p.Send ◀── Env.Confirm (tool goroutine blocks) ◀──┘
      │        y/n ──▶ reply chan ──▶ tool continues
      │ View() = viewport(transcript) + status bar + input/dialog
      └────────────────────────────────────────────────────────────────────────┘
```

- **The agent runs in its own goroutine. Every event is sent with `p.Send`.** Bubble Tea delivers messages one at a time to `Update`, so UI state needs no locks.
- **Confirmation crosses the boundary twice.** The bash tool calls `Env.Confirm`, which sends a `confirmMsg` with a reply channel and blocks. The UI shows the dialog, and a key press writes the answer to the channel. Cancelling the run unblocks it. This is the shape Codex uses for approvals and lesson 09 reuses for permissions.
- **Cells, not raw text.** Events fold into cells: user, assistant (text deltas append to the current cell), thinking, tool (header filled at `tool_start`, result attached at `tool_end` by call ID), info, error. Rendering cells at the current width means a resize simply re-wraps.
- **Context in the status bar is the last request's input tokens.** That's what the model actually saw, not a running total. Lesson 11 turns it into a budget meter.
- **No markdown rendering.** It adds a dependency and per-delta re-parsing of a growing document. Plain wrapped text is enough to learn from.
- **The REPL stays.** `--ui repl` (or a non-TTY stdout) uses it, and `exec` mode (lesson 07) still uses the REPL renderer.

## The contract

`harness/internal/agent`:

```go
const Steer EventKind = "steer"   // Text = the injected message
func (a *Agent) Steer(msg string) // safe to call from any goroutine while Run is active
```

`harness/internal/ui/tui`:

```go
type EventMsg struct{ agent.Event }
type Model struct{ ... }           // implements tea.Model with pointer receivers
func New(a *agent.Agent, model string) *Model
func (m *Model) SetSend(func(tea.Msg))
func FromEvents(events []agent.Event, w, h int) *Model     // replay without an agent
func (m *Model) Transcript() string                         // all cells rendered at current width
func (m *Model) Status() string                             // must include context tokens and session cost ($%.4f)
func (m *Model) Confirm(ctx context.Context, command string) bool
func Run(a *agent.Agent, model string, extra agent.Sink) error
```

Tool cells mark results with `✓` or `✗`. The tests check for `✗` and for the order of cells.

Dependencies (Bubble Tea v2 moved to the `charm.land` module path):

```sh
go get charm.land/bubbletea/v2@v2.1.0 charm.land/bubbles/v2@v2.2.1 charm.land/lipgloss/v2@v2.0.6
```

Provided in `support/`: `cmd/harness` (`--ui tui|repl`; TUI is the default on a terminal) and `cmd/replay`.

## Build it

Continue from lesson 07 and copy this lesson’s `tests/` and `support/`. The `internal/ui/tui/` implementation is new; the updated harness command and replay command are supplied. You will also change the existing agent.

1. **Steering.** Tests: `go test ./internal/agent -run TestSteer`.
   - Add a mutex-protected queue to `Agent`. Write `injectSteers` to drain the queue into history and emit `Steer` events.
   - Call it after appending tool results (unless cancelled). Also call it when the model answers without tools: if anything was queued, `continue` the loop instead of returning.
2. **Cells from events.** Tests: `go test ./internal/ui/tui -run 'TestTranscript|TestToolError'`.
   - `apply(e)` is a switch on `e.Kind`. Keep `map[callID]cellIndex` so `tool_end` fills the right cell, even when parallel tools finish out of order.
3. **Layout.**
   - A `viewport` for the transcript with height `h-3`, then one status line, then a `textinput`.
   - On every update, re-render the cells and keep the view pinned to the bottom if it was already there. Scrolling up to read must not snap back.
   - Wrap each cell with `lipgloss.NewStyle().Width(w)`. Test: `TestWindowResizeRewraps`.
4. **Status bar.** Show the latest request’s context use and the session cost from agent events. Test: `TestStatusBar`.
5. **Live wiring.** In `start`, launch `agent.Run` in a goroutine with a sink that calls `send(EventMsg{e})`, and send `runDoneMsg` when it returns. Write `Confirm` with a reply channel and a `select` on `ctx.Done()`.
6. **Keys.** In confirm mode, `y` approves; anything else denies. Ctrl-C cancels a running agent, or quits when idle. Esc cancels. PgUp and PgDn scroll.

Bubble Tea v2 notes: `View()` returns `tea.View` (use `tea.NewView(s)` and set `v.AltScreen = true`). Key presses are `tea.KeyPressMsg`, matched with `.String()` (`"enter"`, `"esc"`, `"ctrl+c"`).

## Run it

```sh
go test ./...
cp -R <path-to>/06-system-prompt/sample-repo /tmp/textkit && (cd /tmp/textkit && git init -q)
go run ./cmd/harness --root /tmp/textkit --trace /tmp/session.jsonl
```

Try it:
- Ask "implement Reverse so the tests pass".
- While it's working, type "also add a doc example" and press Enter. That steers the run.
- Approve the bash command with `y`.
- Press Esc during a long answer to cancel.

## Demo

```sh
go run ./cmd/replay -speed 2 /tmp/session.jsonl
```

This replays any recorded trace, including those from lessons 02–07, through the same `Update` path the live agent uses, at the original pace. No network, no tools. A replay of lesson 06's first-task trace at 100×34:

```
→ grep mode=content pattern=Reverse ✓
  textkit_test.go:32:func TestReverse(t *testing.T) {
  textkit_test.go:40:        if got := Reverse(tt.in); got != tt.want {
  textkit_test.go:41:            t.Errorf("Reverse(%q) = %q, want %q", tt.in, got, tt.want)

→ read path=textkit.go ✓
       1    // Package textkit provides small string utilities.
       2    package textkit
       3
       4    import (
  … 20 more lines

→ bash cat >> textkit.go <<'EOF' // Reverse returns s with its runes in reverse order. func
Reverse(s string) string { r := []rune(s) for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 { r[i],
r[j] = r[j], r[i]… ✓
  ok      textkit    0.326s
  [exit code: 0]

I added `Reverse` to `textkit.go`. It reverses the string rune by rune, so multi-byte characters
like `héllo` and `日本語` come out right. `go test ./...` passes and `gofmt -l .` prints nothing.

It reverses code points, not grapheme clusters. A base letter followed by a combining accent would
come out with the accent in front of the wrong letter. The tests don't cover that case, so I left it
alone.
  · turn 4 · context 4257 tok · session $0.0335 · idle
> ask the agent — Enter sends, Esc cancels a run, Ctrl-C quits
```

What it shows:

- **The screen is a function of the event log.** Replaying the log gives the same screen. `TestReplayDeterministic` checks that byte for byte. Lesson 10 builds on this: if events are durable, a session can be resumed, and with it everything the UI showed.
- **"context 4257 tok"** is the input size of the last request. Lesson 06 measured 1,461 tokens for the system prompt and tool schemas, so the other ~2,800 are the first-message context and this session's messages and tool results.
- A bug found while building this: Go's JSON encoder escapes `<`, `>` and `&` in strings by default, so the trace stored `cat >> f <<'EOF'` as `cat \u003e\u003e f \u003c\u003c'EOF'`. The recorder now calls `SetEscapeHTML(false)`. The fix is in `internal/trace` in every lesson's `solution/` and in lesson 02's `support/`.

Cost: replay is free.

## Exercises

1. **Follow-ups versus steering.** Queue a follow-up until the agent would otherwise stop. Compare its event order and effect with steering during a tool batch.
2. **Client/server split.** Send events over SSE to a separate UI. How must confirmation requests and replies cross the process boundary?

## Further reading

- Codex app-server: https://openai.com/index/unlocking-the-codex-harness/
- Codex protocol (submission/event queues): `codex-rs/docs/protocol_v1.md` in https://github.com/openai/codex
- pi TUI rendering and the scrollback vs. full-screen discussion: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
- Bubble Tea v2: https://github.com/charmbracelet/bubbletea
