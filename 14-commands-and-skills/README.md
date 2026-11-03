# Lesson 14: Slash Commands, Templates, and Skills

## What you'll build

A command system shared by the REPL and the TUI:

- **Built-ins:** `/help`, `/model`, `/mode`, `/compact`, `/context`, `/undo`, `/clear` and `/skills`.
- **Prompt templates.** Markdown files that become commands, with `$ARGUMENTS`, `$1`, `${1:-default}` and `` !`shell` `` substitution (through the permission checker).
- **Skills.** Instructions the model loads on demand. Only their names and descriptions sit in context.

The TUI gets a command palette with Tab completion. The demo measures what progressive disclosure saves, and watches a model decide to load a skill.

## How the real harnesses do it

Commands are where users extend a harness without writing code:

| | Templates | Syntax | Skills |
|---|---|---|---|
| **Claude Code** | `.claude/commands/x.md` → `/x`; "custom commands have been merged into skills" | `$ARGUMENTS`, `$0`; `` !`cmd` `` runs before sending; frontmatter: `description`, `allowed-tools`, `model`, `disable-model-invocation`, … | `SKILL.md` with name and description in context (descriptions truncated at 1,536 chars); body loaded on invocation; supporting files only when read |
| **pi** | `~/.pi/agent/prompts/review.md` → `/review` | `$1`, `$@`/`$ARGUMENTS`, `${1:-default}`, `${@:N:L}`; shell-like quoting | Agent Skills spec; name, description and path in the system prompt; `/skill:name args` forces one |
| **opencode** | `{command,commands}/**/*.md` | `$ARGUMENTS`, `$1..$n`, `` !`cmd` `` | `skill` tool |
| **Codex** | `~/.codex/prompts` was **removed** (2026-03-28) in favor of skills | — | `SKILL.md` |

The trend is clear: templates (user-invoked prompts) and skills (model-invoked instructions) are converging. Both are markdown with frontmatter. What differs is who decides to load them.

**Progressive disclosure** is the same idea as lesson 11's pruning, applied before the fact. Don't pay context for instructions the task doesn't need. A skill costs one line until the model asks for it with the `skill` tool. Its supporting files cost nothing until the model reads them.

## Design for our harness

```
input ──▶ Parse ──▶ "/name args"? ──┬─ registry lookup ─▶ built-in Run(ctx, args) → text shown to the user
          │                          │                ─▶ template: Expand(body, args, shell) → sent as a prompt
          │                          │                ─▶ /skill:x args → "Load the x skill…" prompt
          │                          └─ not registered ─▶ REPL: "unknown command (try /help)"; TUI: sent to the model as text
          └─ not a command (incl. paths like /usr/bin/env) ─▶ prompt to the agent
```

- **`Parse`** requires `/` followed by a letter, and the name can't contain another `/`, so pasting a path isn't mistaken for a command.
- **`Expand`** substitutes in this order: `` !`cmd` `` first (stop on error), then `$ARGUMENTS`, `${N:-default}`, `$N`. `\$5` is a literal `$5`. Arguments split like a shell (`"old file.go"` is one argument).
- **Shell substitution goes through `CheckBash`.** A template is code from a file, and a `!`git push`` inside it must hit the same rules as the model's own commands.
- **Templates load from two places:** `~/.config/harness/commands/` and then `<root>/.harness/commands/`. The project overrides the user. The file name is the command name.
- **Skills load** from `<dir>/<name>/SKILL.md` under `~/.config/harness/skills` and `<root>/.harness/skills`. A file with no `name` or `description` is skipped. The listing goes into the first-message context (lesson 06), never the system prompt, so adding a skill doesn't change the cached prefix of other projects.
- **`/mode`** uses lesson 12's `Remind`: the mode switch reaches the model as a `<system-reminder>`. **`/model`** records a `model_change` session entry and warns that the new model's cache starts cold.

### Commands and the UI

Commands run outside the agent loop. Two cases need care:

- **A template ends by sending a prompt**, so it needs the UI's way to start a run. The REPL hands one over through `Options.SetSubmit`. The TUI exposes `Model.Submit(text)`, which sends a message into Bubble Tea's loop.
- **TUI commands run in a goroutine** (`/compact` makes a model call) and report back with a message. The palette appears while the input is `/` plus a partial name; Tab completes the first match.

## The contract

`harness/internal/commands`:

```go
type Command struct { Name, Description, ArgumentHint string; Run func(ctx context.Context, args string) (string, error) }
func NewRegistry() *Registry
func (r *Registry) Register(c Command); Lookup(name string) (Command, bool); List() []Command; Complete(prefix string) []Command
func Parse(input string) (name, args string, ok bool)

type Shell func(ctx context.Context, command string) (string, error)
func Expand(tmpl, args string, shell Shell) (string, error)   // nil shell + !`…` → error

type Template struct { Name, Description, ArgumentHint, Body, Path string }
func LoadTemplates(dirs ...string) []Template
func (r *Registry) AddTemplates(ts []Template, send func(ctx context.Context, prompt string) error, shell Shell)

type Skill struct { Name, Description, Path string }
func LoadSkills(dirs ...string) []Skill
func SkillListing(skills []Skill) string        // "<available_skills>…</available_skills>", metadata only
func SkillTool(skills []Skill) tools.Tool       // "skill": {name} → body + supporting file paths
```

`harness/internal/agent`: `func (a *Agent) Model() string`, `func (a *Agent) SetModel(m string)`.

`harness/internal/ui/tui`: `func (m *Model) SetCommands(*commands.Registry)`, `func (m *Model) Submit(text string)`, `func RunWith(m *Model, extra agent.Sink) error`. Enter on a registered command runs it. The palette lists `/name description` for matches, and Tab completes.

Provided in `support/`:
- **`cmd/harness`**: all built-ins, template loading, skills (listing, tool and `/skill:*` commands), and shell substitution through the checker.
- **`internal/ui/repl`**: now takes a `*commands.Registry`.
- **`cmd/skillcost`**.

The lesson directory also has `sample/.harness/`: 12 skills and a `/review` template.

## Build it

1. **Parse and Expand.** Tests: `go test ./internal/commands -run 'TestParse|TestExpand'`. A regexp per substitution works; run the shell substitution first so a failure stops before anything else.
2. **Templates.** Test: `-run 'TestLoadTemplates|TestTemplateCommand'`. A minimal frontmatter parser (`key: value` lines between `---` markers) is enough.
3. **Registry.** Test: `-run TestRegistry`.
4. **Skills.** Test: `-run TestSkills`. The tool's result wraps the body in `<skill name=… path=…>` and lists the other files in the skill directory.
5. **Agent model switch.** Test: `go test ./internal/agent -run TestSetModel`. Guard the model with the agent's mutex; `/model` can run while the TUI is idle but the agent goroutine is still finishing up.
6. **TUI.** Test: `go test ./internal/ui/tui -run TestPalette`. The test types `/co` through real key events and then presses Tab.

## Run it

```sh
go test ./...
cp -R <path-to>/06-system-prompt/sample-repo /tmp/textkit && cp -R <lesson>/sample/.harness /tmp/textkit/ && (cd /tmp/textkit && git init -q)
go run ./cmd/harness --root /tmp/textkit
> /help
> /review error handling
> /mode plan
> /skill:go-testing add tests for Title
```

## Demo

`go run ./cmd/skillcost -skills <lesson>/sample/.harness/skills -live -repo <path-to>/06-system-prompt/sample-repo`. About $0.003.

```
12 skills
  listing only (names + descriptions)    329 tokens  ██
  every SKILL.md inlined                5671 tokens  ██████████████████████████████████████████████████
  saved per request: 5342 tokens (94%)

live (anthropic/claude-haiku-5.5): "Add tests for WordCount covering its edge cases."
  → bash  ls -la && grep -rn "WordCount" --include=*.go . ; cat go.mod
  → bash  cat textkit.go && cat textkit_test.go && ls -R .harness
  → bash  go version; go test ./...; gofmt -l .; git status --short
  → edit  textkit_test.go (adds 9 cases: whitespace-only, tabs/newlines, Unicode, NBSP, zero-width space)
  → read  textkit_test.go
  → skill {"name":"go-testing"}
  → edit  textkit_test.go
  → bash  go test … ; gofmt -l .
  test uses t.Run with named cases (the skill's convention): false
```

What it shows:

- **Listing 12 skills costs 329 tokens; inlining them costs 5,671 on every request.** That's the whole argument for progressive disclosure, and the same arithmetic as lesson 18's MCP tool definitions.
- **The model found the skill, but late.** It loaded `go-testing` after its first edit attempt. That attempt was refused by read-before-edit, because `cat` through bash doesn't count as a read. Then it **followed the existing file's style** (`{in, want}` without `t.Run`) instead of the skill's convention. When the code and the skill disagree, the code in front of the model won here. A skill that says "use this convention" competes with every example in the repository.
- **The model also looked inside `.harness/`** (`ls -R .harness`) after seeing the listing. Skills are files, and models treat them as files.

The cost of a skill call is one extra round trip. The saving is thousands of tokens on every request that doesn't need it.

## Exercises

1. Support `disable-model-invocation: true` (the skill is listed only as a command, never in `<available_skills>`) and `allowed-tools` (the run restricts tools while the skill is active).
2. Add `/init`, which asks the model to write an `AGENTS.md` for the repository (opencode and Codex both ship one). Then run lesson 07's task 10 with and without it.
3. Make the palette show `ArgumentHint`, and complete template arguments from file names.

## Further reading

- Claude Code skills and commands: https://code.claude.com/docs/en/skills
- pi prompt templates and skills: `packages/coding-agent/docs/prompt-templates.md`, `docs/skills.md` in https://github.com/earendil-works/pi
- opencode commands: `packages/opencode/src/config/` and `session/prompt.ts` (v1.x tags) in https://github.com/anomalyco/opencode
