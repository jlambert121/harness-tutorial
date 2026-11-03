# Lesson 20: Where to Go Next

You've built a harness with the parts every serious coding agent has. Here is what was left out, where to read the real implementations, and what to build next.

## What you built, in one picture

```
              ┌──────────── cmd/harness (REPL · TUI · exec) ────────────┐
 prompt ──▶ commands (14) ──▶ hooks: prompt (17) ──▶ agent loop (02)
                                                    │  router / model roles (19)
                                                    │  transform: prune (11) · cache markers (12)
                                                    │  compaction on threshold / overflow (13)
                                                    ▼
                                         llm client · SSE · retries (01) ──▶ gateway
                                                    │
                     tool calls ──▶ registry: parallel batches, caps (03) · hooks pre/post (17)
                        │             read/glob/grep/ls (03) · edit/write (04) · bash (05)
                        │             permissions + sandbox (09) · memory (15) · task/subagents (16) · MCP (18)
                        ▼
   events ──▶ trace (02) · TUI (08) · sessions + snapshots (10) · eval runner (07)
   context ◀── system prompt + AGENTS.md (06) · skills (14) · memory recall (15)
```

## What was left out

| Topic | What it is | Who has it |
|---|---|---|
| **LSP diagnostics** | After each edit, ask a language server for errors and attach them to the tool result | opencode: 38 servers, ERROR severity only, max 20 per file, "LSP errors detected in this file, please fix:"; Claude Code has an `LSP` tool |
| **Web fetch and search** | Fetch a URL as markdown and answer from it with a second model call ("lossy by design") | Claude Code `WebFetch`/`WebSearch` (100k chars, 15-min cache, refuses localhost); opencode `webfetch`/`websearch` |
| **Images and PDFs** | Content blocks other than text, both in tool results and user input | pi's `read` sends images as attachments; Claude Code reads PDFs 20 pages at a time |
| **Plan mode as a workflow** | A separate agent or mode that can only read and must present a plan file for approval | Claude Code `EnterPlanMode`/`ExitPlanMode` tools; opencode `plan` agent (edits denied except plan files); pi says use `PLAN.md` |
| **Client/server split** | The agent as a server with an event stream; UIs, IDEs and web as clients | opencode (HTTP + SSE `/event`); Codex app-server (JSON-RPC over stdio) |
| **Containers and worktrees** | Run each session in a container or a git worktree so parallel agents don't collide | Claude Code `isolation: worktree` for subagents; pi recommends containers |
| **Background processes** | Long-running commands the agent can poll or feed input to | Codex `exec_command` + `write_stdin` (PTY sessions); Claude Code `run_in_background` |
| **ACP** | Agent Client Protocol, so editors can drive any agent | opencode `acp` command |
| **Auto-approval classifiers** | A model that reviews each risky action instead of a person | Claude Code `auto` mode |
| **Structured output** | Force the final answer into a JSON schema | Codex `--output-schema`; opencode `StructuredOutput` tool with `tool_choice: required` |

## Reading guide

File paths are from the research behind this tutorial (see `../research-notes.md`): opencode **v1.19.3** (`packages/opencode/src/…`; v2 moved to `packages/core/src`), Codex at **main, 2026-10-19** (`codex-rs/…`), pi at **`earendil-works/pi` main, 2026-10-19** (`packages/…`). Paths move; search for the names if one has.

| Lesson | pi | opencode v1 | Codex |
|---|---|---|---|
| 01 Model client | `ai/src/types.ts`, `ai/src/api/openai-completions.ts`, `ai/src/utils/event-stream.ts` | `session/llm.ts`, `session/retry.ts` | `core/src/client.rs`, `codex-api/src/sse/responses.rs` |
| 02 Agent loop | `agent/src/agent-loop.ts` (`runLoop`) | `session/prompt.ts` (`runLoop`), `session/processor.ts` | `core/src/session/turn.rs` (`run_turn`, `run_sampling_request`) |
| 03 Tools | `coding-agent/src/core/tools/`, `ai/src/utils/validation.ts` | `tool/registry.ts`, `tool/tool.ts`, `tool/truncate.ts` | `core/src/tools/parallel.rs`, `core/src/stream_events_utils.rs` |
| 04 Editing | `coding-agent/src/core/tools/edit-diff.ts`, `file-mutation-queue.ts` | `tool/edit.ts` (the 9 replacers) | `apply-patch/src/parser.rs`, `file_update.rs`, `seek_sequence.rs` |
| 05 Shell | `coding-agent/src/core/tools/` (bash, `truncate.ts`) | `tool/shell.ts` | `core/src/unified_exec/`, `core/src/tools/handlers/shell_spec.rs` |
| 06 Prompts | `coding-agent/src/core/system-prompt.ts`, `resource-loader.ts` | `session/system.ts`, `session/instruction.ts`, `session/llm/request.ts` | `core/src/agents_md.rs`, `protocol/src/prompts/base_instructions/` |
| 07 Headless | `--print` mode | `run` command | `exec/src/` |
| 08 UI | `tui/src/tui-main-screen.ts` (differential rendering) | `cli/cmd/tui.ts`, `packages/tui` | `tui/src/app.rs`, `docs/protocol_v1.md`, `app-server/` |
| 09 Permissions | (none; project trust) | `permission/index.ts`, `permission/arity.ts`, `agent/agent.ts` (defaults) | `core/src/exec_policy.rs`, `core/src/safety.rs`, `sandboxing/src/seatbelt.rs`, `linux-sandbox/` |
| 10 Sessions | `coding-agent/docs/session-format.md` | `core/session/sql.ts`, `snapshot/index.ts`, `session/revert.ts` | `rollout/src/recorder.rs`, `core/src/session/rollout_reconstruction.rs` |
| 11–13 Context | `coding-agent/src/core/compaction/compaction.ts`, `ai/src/models.ts` (`calculateCost`) | `session/overflow.ts`, `session/compaction.ts`, `core/session/compaction.ts` | `core/src/compact.rs`, `core/src/compact_remote_v2_attempt.rs`, `core/src/context_manager/` |
| 14 Commands, skills | `coding-agent/docs/prompt-templates.md`, `docs/skills.md` | `config/agent.ts`, `session/prompt.ts` (`!` substitution) | `tui/src/slash_command.rs`, skills |
| 15 Memory | — | — | `memories/README.md`, `ext/memories/src/lib.rs` |
| 16 Subagents | (none, by design) | `tool/task.ts`, `agent/subagent-permissions.ts` | `agents.*` config |
| 17 Hooks | `coding-agent/docs/extensions.md` (event list) | plugin hooks `tool.execute.before/after` | Stop hooks in `turn.rs` |
| 18 MCP | `coding-agent/docs/mcp.md` (codemode, `tool_search`) | `mcp/index.ts`, `mcp/catalog.ts` | `codex-mcp/`, `rmcp-client/` |
| 19 Models | `coding-agent/src/core/model-resolver.ts`, `ai/src/api/transform-messages.ts`, `docs/virtual-models.md` | `provider/` (`defaultModel`, `getSmallModel`), `core/models-dev.ts` | `protocol/src/openai_models.rs`, config `review_model` |

Claude Code is closed source. Its documentation at https://code.claude.com/docs is unusually detailed about behavior, and the Claude Code team's blog posts explain the reasons: prompt caching, context engineering, long-running harnesses.

## Projects to try next

1. **Run your harness on SWE-bench Lite or Terminal-Bench** with lesson 07's runner, adapted to their task formats. Compare with the published numbers for Aider (26.3% on SWE-bench Lite in 2024) and pi.
2. **Make the eval discriminate.** Mine your own git history for bugs you fixed, and turn each into a task with its regression test as the hidden check. Lesson 07's suite saturated on current models; your own codebase won't.
3. **Add LSP.** Start `gopls` as a subprocess and send `textDocument/didChange` after each edit. Attach ERROR diagnostics to the edit result, then measure the effect with lesson 17's hooklab.
4. **Split client and server.** Serve events over SSE and approvals over HTTP. Then build a second front end, such as a web page or an editor extension, against the same core.
5. **Work in parallel safely.** Give each subagent a git worktree (`git worktree add`) and merge their diffs, the way Claude Code's `isolation: worktree` does.
6. **Try an optional exercise.** Re-indentation (lesson 04) and cache-stable deferred tools (lesson 18) let you explore tradeoffs you saw in the demos.

## Further reading

- Anthropic, "Effective context engineering for AI agents": https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- Anthropic, "Effective harnesses for long-running agents": https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
- Anthropic, "Writing effective tools for agents": https://www.anthropic.com/engineering/writing-tools-for-agents
- OpenAI, "Unrolling the Codex agent loop" and "Unlocking the Codex harness": https://openai.com/index/unrolling-the-codex-agent-loop/, https://openai.com/index/unlocking-the-codex-harness/
- Mario Zechner, "What I learned building an opinionated and minimal coding agent": https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
- Aider's blog on edit formats, repo maps and benchmarks: https://aider.chat/blog/
