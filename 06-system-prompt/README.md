# Lesson 06: System Prompt and Project Instructions

## What you'll build

The harness assembles everything the model reads before the user's first word: a system prompt chosen per model family, the tool definitions, project instructions discovered from `AGENTS.md`/`CLAUDE.md` files, and an environment block. A new inspector shows what each part costs in tokens. At the end the harness completes its first real coding task: implementing a function so a test suite passes, following the repo's own conventions.

## How the real harnesses do it

**How big is the prompt?**

- **pi** argues for a tiny prompt: frontier models are "RL-trained up the wazoo" and don't need "10,000 tokens of system prompt". Its prompt plus tool definitions come in under 1,000 tokens. The prompt is a preamble ("You are an expert coding assistant operating inside pi…"), then `<tools>`, `<rules>`, `<project_context>` and `<cwd>` sections.
- **opencode** picks a prompt file per model family by matching the model ID: `anthropic.txt` ("You are OpenCode, the best coding agent on the planet."), `gpt.txt`, `gemini.txt`, `codex.txt`, `beast.txt` for older OpenAI models, and `default.txt`. It appends an `<env>` block (working dir, git yes/no, platform, date).

**Where does the project context go?** This is the less obvious decision, and the more important one.

- **Codex** sends the base instructions as `instructions`. Everything project-specific goes in input messages, in this order: permissions (a developer message), `developer_instructions`, `AGENTS.md` and skills (a user message), `<environment_context>` (a user message), then the user's actual message.
- **Claude Code** delivers CLAUDE.md "as a user message after the system prompt, not as part of the system prompt."

Both keep the system prompt byte-identical across sessions and projects, which makes it the shared, cacheable prefix (lesson 12).

**How instruction files are discovered:**

| | Files per directory (first match wins) | Search range | Budget |
|---|---|---|---|
| Codex | `AGENTS.override.md`, `AGENTS.md`, configured fallbacks | project root (nearest `.git`) down to cwd, concatenated root-first | 32 KiB total |
| pi | `AGENTS.override.md`, `AGENTS.md`, `CLAUDE.md` | global, then every ancestor from `/` down to cwd | n/a |
| opencode | `AGENTS.md`, else `CLAUDE.md` | global file plus findUp to the worktree; nested `AGENTS.md` loaded lazily when `read` touches that directory | n/a |
| Claude Code | `CLAUDE.md` (`AGENTS.md` only if no CLAUDE.md) | managed → user → project → local; ancestors at launch, subdirectories lazily | advice: under 200 lines per file |

The common rule: **root first, nearest last.** Later text wins when instructions conflict, so the file closest to the work has the final say.

## Design for our harness

```
System  (byte-identical every session; cacheable)
  └─ templates/<variant>.md            ← chosen by model prefix: anthropic/ → anthropic, openai/ → gpt, else base
Tools   (sorted by name; identical every session)
First user message
  ├─ block 1: Context                  ← varies per project and day
  │    <project_instructions path="~/.config/harness/AGENTS.md">…</project_instructions>
  │    <project_instructions path="/repo/AGENTS.md">…</project_instructions>
  │    <environment> cwd · platform · git · date </environment>
  └─ block 2: what the user typed
```

- **Nothing variable goes in `System`.** The date is the classic mistake. Put a date in the system prompt and every session's prefix is different, so no session can reuse another's cache. Claude Code's team lists "timestamps in the static prompt" among the things that broke their caching.
- **Context goes in the first user message, once.** Later user messages don't repeat it. In history it's a separate text block. The encoder from lesson 01 sends multiple blocks as content parts.
- **Variants are small edits of one prompt.** `gpt.md` adds the persistence and planning reminders from OpenAI's GPT-4.1 prompting guide ("You are an agent: keep going until the user's request is completely resolved…"). `anthropic.md` adds scope discipline and terse reporting. Lesson 07's eval is how you'd find out whether a variant actually helps a given model. Prompts are hypotheses until measured.
- **Discovery** follows Codex: root = nearest ancestor with `.git`, else cwd only. In each directory, the first of `AGENTS.override.md`, `AGENTS.md`, `CLAUDE.md`. 32 KiB budget. Plus a global `~/.config/harness/AGENTS.md`. Without a `.git` boundary, a harness run in `~/src/project` would load `~/AGENTS.md`, and `/`'s if it had one.

## The contract

`harness/internal/prompt`:

```go
type Instruction struct { Path, Content string }
type EnvInfo struct { Cwd, Platform, Date string; GitRepo bool }
type Section struct { Name, Text string; Tokens int }
// Section names: "system prompt", "tool: <name>", "instructions: <path>", "environment"
type Prompt struct {
    Variant  string    // "anthropic" | "gpt" | "base"
    System   string
    Context  string
    Sections []Section
}

func EstimateTokens(s string) int                         // ceil(bytes/4)
func DetectEnv(cwd string) EnvInfo
func Build(model string, env EnvInfo, instr []Instruction, tools []llm.ToolSpec) Prompt
func Discover(cwd, home string, budget int) []Instruction // global from home/.config/harness
```

Context format (the tests check these strings): `<project_instructions path="…">` with `%q` quoting, and `<environment>` containing the cwd, platform and date.

`harness/internal/agent`: `Config.Context string`, prepended as its own block to the first user message of the conversation.

Provided in `support/`: `internal/prompt/templates/{base,anthropic,gpt}.md` (load them with `//go:embed templates/*.md`), `cmd/inspect`, `cmd/harness` (now builds the prompt, and adds `--yes` for throwaway workspaces), `internal/ui/repl`. The lesson also includes `sample-repo/` for the demo.

## Build it

1. **Read the templates.** About 440 tokens each. Decide what you'd cut. Every sentence is paid for on every request.
2. **`Discover`.** Tests: `go test ./internal/prompt -run TestDiscover`.
   - Find the git root by walking up for `.git`, which can be a directory or, in worktrees, a file. Build the directory chain root→cwd.
   - Truncate on a rune boundary (`strings.ToValidUTF8`) and mark the cut.
   - A whitespace-only `AGENTS.override.md` falls through to the next file, as in Codex.
3. **`Build`.** Tests: `-run 'TestPrompt|TestModel|TestContext|TestSections|TestEstimate'`. Section tokens use `EstimateTokens`. Tool sections count name + description + schema.
4. **Agent context.** Test: `go test ./internal/agent -run TestContextPrepended`. Only when `len(history) == 0`.

## Run it

```sh
go test ./...
cp -R <lesson>/sample-repo /tmp/textkit && (cd /tmp/textkit && git init -q)
go run ./cmd/inspect --root /tmp/textkit            # add --show to print it all
go run ./cmd/harness --root /tmp/textkit
> Implement Reverse so the tests pass.
```

## Demo

`cmd/inspect` on the sample repo:

```
model anthropic/claude-sonnet-5.5 → prompt variant "anthropic"

  system prompt                                  442  ████████████████████████████████████████
  tool: bash                                     208  ██████████████████
  tool: edit                                     187  ████████████████
  tool: glob                                     100  █████████
  tool: grep                                     177  ████████████████
  tool: ls                                        75  ██████
  tool: read                                     168  ███████████████
  tool: write                                    104  █████████
  …/textkit/AGENTS.md                            103  █████████
  environment                                     44  ███

  total before the first user word              1608 tokens (estimated, bytes/4)
    system prompt + tools                       1461 tokens (identical every session → cacheable)
    first-message context                        147 tokens (varies by project and day)
```

Tool definitions cost more than the system prompt. That's typical, and it's why pi keeps four tools, and why MCP servers with dozens of tools hurt (lesson 18). The full transcript of the first real task (Sonnet 5.5, `--yes` in a throwaway copy):

```
loaded /tmp/textkit/AGENTS.md
> Implement Reverse so the tests pass.
→ grep {"pattern": "Reverse", "mode": "content"}     → ls          (one batch)
→ read textkit.go                                     → read textkit_test.go
→ bash cat >> textkit.go <<'EOF' … // Reverse returns s with its runes in reverse order. … EOF
       gofmt -l .; go test ./...
  ← ok textkit 0.326s [exit code: 0]
I added `Reverse` to `textkit.go`. It reverses the string rune by rune, so multi-byte characters
like `héllo` and `日本語` come out right. `go test ./...` passes and `gofmt -l .` prints nothing.
It reverses code points, not grapheme clusters. A base letter followed by a combining accent would
come out with the accent in front of the wrong letter. The tests don't cover that case, so I left it alone.
[in 14161 (cached 0) · out 520 · $0.03352]
```

What it shows:

- **AGENTS.md was followed.** A doc comment starting with the function's name, rune-based handling, and `gofmt -l .` plus `go test ./...` before finishing, all from the repo's file and not from the system prompt.
- **The model appended code with `bash` heredoc instead of `edit`.** That skips read-before-edit, the match cascade and the diff. Harnesses can't prevent this with tools alone. Bash can do anything. Lesson 09 covers why permission rules on shell commands are "not a security boundary" (Claude Code's docs), and what a sandbox adds.
- **14k input tokens for four steps, uncached.** About 1,600 of each request are the prefix measured above, re-sent every step. Lesson 12 makes them cost a tenth as much.
- **During development, piping extra "y" lines** (intended for confirm prompts that never appeared) made the model treat each "y" as a user turn. It then added unrequested features. That's why `--yes` exists for scripted runs, and why lesson 07 adds a proper headless mode.

Cost: about $0.035 per run with Sonnet, about $0.002 with Haiku.

## Exercises

1. Cut `anthropic.md` to under 150 tokens. Keep that version for lesson 07 and compare pass rates.
2. Load nested `AGENTS.md` lazily, as opencode does. When `read` opens a file in a directory with an instruction file that isn't loaded yet, attach it to the tool result inside `<system-reminder>`.
3. Support `@path` imports in instruction files (Claude Code: relative to the importing file, depth ≤ 4, skip imports inside code spans).

## Further reading

- Codex, prompt layout: https://openai.com/index/unrolling-the-codex-agent-loop/
- Claude Code memory and CLAUDE.md: https://code.claude.com/docs/en/memory
- AGENTS.md: https://agents.md
- OpenAI GPT-4.1 prompting guide (agentic reminders): https://developers.openai.com/cookbook/examples/gpt4-1_prompting_guide
- Claude Code caching lessons: https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/
