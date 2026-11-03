# Lesson 15: Memory

## What you'll build

Notes that outlive a session:

- **A `memory` tool.** It writes one markdown file per memory, with typed frontmatter.
- **An automatically maintained `MEMORY.md` index.**
- **Recall at session start.** The index (capped at 200 lines / 25 KB) goes into the first-message context; topic files are read on demand.
- **Guidance on what's worth saving.**

The demo states a convention in one session and checks whether a fresh session follows it: 3/3 with memory, 0/3 without.

## How the real harnesses do it

"Memory" covers several mechanisms that differ in who writes, who reads, and when:

| Kind | Written by | Loaded | Examples |
|---|---|---|---|
| Instruction files | people | every session, in full | `AGENTS.md`, `CLAUDE.md` (lesson 06) |
| Notes files in the repo | the agent, when asked | when the agent reads them | pi's advice: plans in `PLAN.md`, todos in `TODO.md`; Anthropic's long-running harness writes `claude-progress.txt` and a JSON feature list |
| Auto memory | the agent, as it works | index at start, topics on demand | Claude Code |
| Extracted memory | a background model call | summary at start | Codex |
| Memory tool (API) | the model, through a tool you implement | the model views it | Anthropic's `memory_20250818` |

- **Claude Code auto memory** (docs, "Memory"):
  - **What gets saved.** "Claude doesn't save something every session. It decides what's worth remembering…" Saying "remember X" saves it explicitly. It skips anything derivable from the codebase or already in CLAUDE.md.
  - **Layout.** `~/.claude/projects/<project>/memory/MEMORY.md` is an index with one line per memory, plus one topic file per memory. Frontmatter carries a `type` of user / feedback / project / reference.
  - **Loading.** The first 200 lines or 25 KB of the index load at session start; topic files load on demand. Subagents don't get it.
- **Codex memories** are off by default (`[features] memories = true`).
  - **Extraction** runs as a background job when the *next* session starts. Phase 1 summarizes each eligible past session with `extract_model`, and the prompt says "No-op is allowed and preferred". Phase 2, a sandboxed consolidation agent, rewrites `MEMORY.md` and `memory_summary.md`.
  - **Loading.** The summary (capped at 2,500 tokens) goes into developer instructions; the model greps the rest when needed.
  - **The docs' warning:** treat memories as "a helpful recall layer, not as the only source for rules that must always apply". Rules belong in AGENTS.md.
  - **A docs bug:** the docs say `max_rollout_age_days` defaults to 30 and `max_rollouts_per_startup` to 16; the source says 10 and 2.
- **Anthropic's memory tool** (API) has commands `view`, `create`, `str_replace`, `insert`, `delete` and `rename` under `/memories`. You implement the storage. The API injects "ALWAYS VIEW YOUR MEMORY DIRECTORY BEFORE DOING ANYTHING ELSE… ASSUME INTERRUPTION", and you must guard against path traversal.
- **opencode and pi** have no built-in memory beyond context files. pi's extensions can persist state in the session.

Two design questions every memory system answers:

1. **Who decides to write?** In-session (Claude Code: the model, as it works) or after the fact (Codex: a separate extraction pass). In-session catches "remember this" immediately. After-the-fact sees the whole session and doesn't distract the working model.
2. **What's loaded up front?** An index or summary is small and always relevant. Full contents are on demand. The same progressive-disclosure trade as lesson 14's skills.

## Design for our harness

Our harness follows Claude Code's layout, with one change: **the tool maintains the index.**

```
~/.local/share/harness/memory/<project-slug>/
  MEMORY.md          - [test-style](test-style.md) — Tests in textkit must be table-driven, …
  test-style.md      ---\nname: test-style\ndescription: …\ntype: feedback\n---\n<body with **Why:**>
```

- **`create`, `str_replace` and `delete`** act on topic files. After each one, `MEMORY.md` is rebuilt from every file's frontmatter. The model can't write a broken index or forget to update it. A memory without `name` and `description` is rejected with an example of the required frontmatter.
- **Paths are bare file names.** No directories, no absolute paths, no `..`, no writing `MEMORY.md`. The memory directory is outside the workspace, so lesson 04's workspace guard doesn't cover it; this check does.
- **Recall** wraps the index in `<memory dir=…>` with a note that memories "may be out of date, so check them against the code". It goes into the **first-message context**, like skills, so the cached system prompt is unchanged.
- **`memory.Guidance`** (what to save, what not to, update before adding) also goes into the context. It's adapted from Claude Code's memory types and Codex's extraction rules.
- **`/memory`** shows the index. `--no-memory` turns the whole thing off.

## The contract

`harness/internal/memory`:

```go
func Open(dir string) *Store
func (s *Store) Dir() string
func (s *Store) Tool() tools.Tool               // "memory": {command: view|create|str_replace|delete, path?, content?, old_str?, new_str?}
func (s *Store) Recall(maxLines, maxBytes int) string   // "" when there are no memories
const Guidance = "<memory_guidance>…</memory_guidance>"
```

Behavior the tests check:
- `view` with no path lists the index, or says "There are no memories yet." when empty.
- `create` requires frontmatter with `name` and `description`, and the error message mentions `description`.
- `str_replace` fails on a missing or non-unique `old_str`.
- Index lines read `- [name](file.md) — description`.
- Recall truncation says "truncated".

Provided in `support/`: `cmd/harness` (registers the tool, appends guidance + recall to the context, adds `/memory` and `--no-memory`) and `cmd/memorydemo`.

## Build it

1. **Paths.** Test: `go test ./internal/memory -run TestPathTraversal`. Reject anything that isn't a plain file name before touching the filesystem.
2. **CRUD and index.** Tests: `-run 'TestMemoryToolCRUD|TestIndex|TestCreate|TestView'`. Write `reindex()`: glob `*.md`, skip `MEMORY.md`, parse frontmatter, write one line per memory, sorted.
3. **Recall.** Test: `-run TestRecall`. Cut at lines first, then bytes on a rune boundary, and say so.

## Run it

```sh
go test ./...
go run ./cmd/harness --root /tmp/textkit
> Remember: in this repo, commit messages use the imperative mood.
> /memory
```

## Demo

`go run ./cmd/memorydemo -repo <path-to>/06-system-prompt/sample-repo`. Six runs of two sessions each, Haiku 5.5, about $0.02.

- **Session 1:** "From now on in this repo, every test you write must be table-driven with a name field, and run each case with `t.Run(tt.name, ...)`. Remember this for future sessions. For now just acknowledge; don't change any files."
- **Session 2** (a new process, fresh context): "Add a test for WordCount with three more cases."

```
memory on  run 1: memory files after session 1: 2 · second session used t.Run(tt.name: true
    MEMORY.md:
      - [test-style](test-style.md) — Tests in textkit must be table-driven, with a name field and t.Run(tt.name, ...) per case
    test-style.md:
      ---
      name: test-style
      description: Tests in textkit must be table-driven, with a name field and t.Run(tt.name, ...) per case
      type: feedback
      ---
      Every test written in this repo must be table-driven, with a `name` field on each case, and each case run via `t.Run(tt.name, ...)`.
      **Why:** the user asked for this explicitly, for consistency across sessions. It extends the AGENTS.md convention (fields `in`, `want`).
memory on  run 2: … used t.Run(tt.name: true
memory on  run 3: … used t.Run(tt.name: true
  → followed in 3/3 fresh sessions

memory off run 1: … used t.Run(tt.name: false
memory off run 2: … used t.Run(tt.name: false
memory off run 3: … used t.Run(tt.name: false
  → followed in 0/3 fresh sessions
```

What it shows:

- **Memory carried the convention across sessions, 3/3 vs 0/3.** Without it, the second session followed the existing file's style, the same pull that defeated the skill in lesson 14. The difference: here the user stated the rule, it was recorded as `feedback` with a reason, and the index line was in context from the first token.
- **The model wrote a good memory.** It was typed, carried a reason (the `**Why:**` line, as the guidance asks), and *noticed* that it extends the AGENTS.md convention rather than contradicting it.
- **What memory is not for:** a rule that must always apply belongs in `AGENTS.md`, as Codex's docs also say. Memory is the right place for preferences learned along the way, before anyone writes them down.

## Exercises

1. **Staleness.** Add a `modified` timestamp to each memory (Claude Code does) and show memories older than 30 days with a "may be stale" marker in recall.
2. **Extraction instead of in-session writes.** At session end, send the transcript (lesson 13's `Serialize`) to a small model with Codex's "No-op is allowed and preferred" instruction, and let it propose memories. Compare quality and cost with the in-session tool.
3. **Limits.** Return an error from `create` when the index would exceed 200 lines, asking the model to merge memories and shorten the index. Claude Code reminds the model near its limit and, once over, returns an error asking it to rewrite the index.

## Further reading

- Claude Code memory: https://code.claude.com/docs/en/memory
- Codex memories: https://learn.chatgpt.com/codex/customization/memories
- Anthropic memory tool: https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool
- Anthropic, "Effective harnesses for long-running agents" (progress files): https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
