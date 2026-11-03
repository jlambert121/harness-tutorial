# Research Notes

Condensed findings from source/doc research performed 2026-10-19. Used as the factual basis for the lessons. Tags: [V] verified in source/docs, [I] inference by the researcher.

## Vercel AI Gateway

- OpenAI-compatible base URL: `https://ai-gateway.vercel.sh/v1` — `GET /models`, `GET /models/{id}`, `POST /chat/completions`, `POST /embeddings`. Also `POST /v1/responses` (Responses API) and Anthropic Messages at base `https://ai-gateway.vercel.sh` (`POST /v1/messages`, `/v1/messages/count_tokens`). [V] https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions
- Other: `GET /v1/models/{creator}/{model}/endpoints` (per-provider pricing/latency/throughput), `GET /v1/credits`, `GET /v1/generation?id=gen_...` (async ingest; may 404 briefly). [V]
- `GET /v1/models` is unauthenticated; ~477 entries. Entry shape: `id`, `type`, `context_window`, `max_tokens`, `tags` (`tool-use`, `reasoning`, `implicit-caching`, `explicit-caching`, `vision`, ...), `supported_parameters`, `reasoning_options`, `pricing` (USD per token as strings: `input`, `output`, `input_cache_read`, `input_cache_write`). [V]
- Auth: `Authorization: Bearer $AI_GATEWAY_API_KEY`. [V]
- Model ids: `creator/model` with dots, e.g. `anthropic/claude-sonnet-5.5`, `anthropic/claude-haiku-5.5` ($0.10/$0.50 per M), `openai/gpt-5.4-nano`, `google/gemini-3.5-flash-lite`. xAI models are prefixed `spacexai/` but the provider slug in routing options is `xai`. [V]
- Tool calling: standard OpenAI `tools` / `tool_calls` / `role:"tool"` + `tool_call_id`. Streaming tool-call deltas and `stream_options.include_usage` are not documented for Chat Completions — must be tested. [V/unverified]
- Streaming: SSE `data: {chunk}` ... `data: [DONE]`. [V]
- Cost/routing in `provider_metadata.gateway`: `routing{originalModelId,resolvedProvider,finalProvider,fallbacksAvailable,modelAttempts}`, `cost`, `marketCost`, `generationId`. [V]
- Caching: on Chat Completions, `cache_control: {type:"ephemeral"}` on messages passes through to Anthropic/Bedrock/Vertex. `providerOptions.gateway.caching: "auto"` places markers automatically (last message + message before last user message). Without it, Anthropic does not cache; OpenAI/Google/DeepSeek cache implicitly. Cached tokens: `usage.prompt_tokens_details.cached_tokens`. [V]
- Session affinity on by default; pin via `providerOptions.gateway.sessionId` / `x-session-id`. "AI Gateway can't move a cache from one model to another." [V]
- Reasoning: request `"reasoning": {"enabled","effort": none|minimal|low|medium|high|xhigh|max,"max_tokens","exclude"}`; response `message.reasoning`, `message.reasoning_details[]` (`reasoning.text` w/ signature, `reasoning.encrypted`, `reasoning.summary`); stream `delta.reasoning`. Pass `reasoning_details` back during tool loops. [V]
- Routing options under `providerOptions.gateway`: `order`, `only`, `sort: cost|ttft|tps`, `models` (fallbacks), `byok`, `providerTimeouts`, `caching`, `sessionId`. 429 may carry `retry-after`; 402 when budget exceeded. [V]
- Go: no official Vercel example. `github.com/openai/openai-go/v3` (v3.82.0, requires go 1.26) works with `option.WithBaseURL`. README bug: one section calls `JSON.ExtraFields()` as a method; it is a map field. [V]

## Model routing

- Claude Code: main model via `/model` > `--model` > env > settings; `opusplan` (opus for plan, sonnet for exec); Haiku-class "background" model for summaries/titles; subagent `model:` frontmatter with precedence call-param > frontmatter > env > main. [V]
- Codex: `model`, `review_model`, `agents.default_subagent_model`, `memories.extract_model`. [V]
- opencode: `model` + `small_model` (titles only; compaction uses session model); per-agent model; subagents inherit. [V]
- Aider: architect (main) + `--editor-model` + `--weak-model` (commit messages, summaries). [V]
- Cursor Auto: trained classifier on 600k+ requests, ~60% savings, explicitly cache-aware. [V]
- RouteLLM (arXiv 2406.18665): strong/weak router, >85% cost cut on MT-Bench at 95% GPT-4 quality. OpenRouter auto: task-type classifier (since 2026-08-10). SWE-Router (arXiv 2607.00053): prompt-only routing hits error floor; run cheap model a few turns then decide escalation. [V]
- Downsides: cache is per-model (switching = rewrite cost); thinking signatures only valid for producing model (real router hit `400 Invalid signature in thinking block`; fix: pin session once signed thinking exists). [V]

## pi (Mario Zechner, now github.com/earendil-works/pi)

- Blog: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/ — minimal: system prompt + tools < 1000 tokens; 4 tools (read, write, edit, bash); no MCP (Playwright MCP ~13.7k tokens, 7–9% of context); no subagents ("black box within a black box"); no plan mode (use PLAN.md); no todo tool (use TODO.md); no permission prompts ("security theater", run in container); no max-steps. Code has since grown: MCP (deferred by default), compaction, fullscreen TUI default, project trust. [V]
- `ai` package: normalized `Message = System | User | Assistant | ToolResult`; assistant blocks `text`, `thinking` (signature), `toolCall`. Stop reasons `stop|length|toolUse|error|aborted`. Streaming events `start, text_start/delta/end, thinking_*, toolcall_start/delta/end, done, error`; providers never throw after stream starts — errors become a final message with `stopReason:"error"`. Partial JSON parsing of tool args during streaming. [V]
- Cross-provider handoff (`transform-messages.ts`): drop thinking blocks when model differs; normalize tool-call ids (Anthropic `^[a-zA-Z0-9_-]+$`, ≤64 chars); skip errored/aborted assistant turns; insert synthetic "No result provided" for orphaned tool calls. [V]
- Agent loop: stream → if `length`, fail all tool calls ("arguments may be truncated") → execute tools → append → poll steering messages; when no tool calls, poll follow-ups. Events `agent_start/end, turn_start/end, message_start/update/end, tool_execution_start/update/end`. Tools return `{content (LLM), details (UI)}`. Parallel by default; per-realpath file mutation queue. Validation failures become `isError` results the model fixes. Steering = inject after current tool batch; follow-up = inject when agent would stop. [V]
- Tools: read (2000 lines / 50KB, offset/limit), write, edit (`edits:[{oldText,newText}]`, each unique, non-overlapping, matched against original; fallback fuzzy: NFKC, trailing ws, unicode quotes/dashes), bash (keeps last 2000 lines/50KB, full output to temp file). [V]
- Sessions: JSONL, header line then entries with `id`/`parentId` forming a tree (branching in-file). Entry types: message, model_change, compaction, branch_summary, custom, label... [V]
- Compaction: trigger `contextTokens > contextWindow - 16384`; keep ~20k recent tokens; never cut at a tool result; serialize old messages as text (`[User]: ... [Assistant tool calls]: read(path=...)`) so summarizer doesn't continue the convo; structured summary (Goal / Constraints & Preferences / Progress Done/In Progress/Blocked / Key Decisions / Next Steps / Critical Context); repeat compaction updates previous summary; original entries never deleted; overflow error → one compact-and-retry. [V]
- TUI: retained mode components `render(width) []string`, diff from first changed line, synchronized output `\x1b[?2026h…l`. [V]
- Context files: per dir first of `AGENTS.override.md, AGENTS.md, CLAUDE.md`; global then root→cwd; wrapped in `<project_instructions path=…>`. Prompt templates `$1 $@ ${1:-default}`. Skills: name+description in prompt, body read on demand. [V]

## opencode (anomalyco/opencode, v1.19.3 read)

- Loop (`session/prompt.ts runLoop`): one LLM step per iteration; exit when finish ∉ {tool-calls, unknown} and no pending tool parts (workaround: some providers return `stop` with tool calls). [V]
- Doom loop: 3 identical consecutive calls → permission ask. Max steps: on last step inject "CRITICAL - MAXIMUM STEPS REACHED … Tools are disabled … Respond with text only." [V]
- Tool-call repair: retry lowercased name, else route to `invalid` tool carrying the error. [V]
- Retry: 2s initial, ×2, jitter 0.25, cap 30s, 5 retries, honors `retry-after(-ms)`. [V]
- Abort: running tools marked error "Tool execution aborted"; replayed as "[Tool execution was interrupted]". [V]
- Truncation: 2000 lines / 50KB; full output saved to disk (7-day cleanup), preview + hint to use grep/offset or a subagent. [V]
- Edit cascade (v1): Simple → LineTrimmed → BlockAnchor (≥3 lines, first/last anchors, Levenshtein ≥0.65) → WhitespaceNormalized → IndentationFlexible → EscapeNormalized → TrimmedBoundary → ContextAware → MultiOccurrence; candidate must be unique unless replaceAll; reject disproportionate matches. v2 simplified to exact + light normalization. Per-file lock, preserve CRLF/BOM, run formatter. GPT models get `apply_patch` instead of edit/write. [V]
- Read: 2000 lines, 2000 chars/line, `N: line` numbering, continuation footer. Bash: 2-min default timeout, tree-sitter-bash parse for permission patterns. [V]
- Overflow: usable = input limit − reserved (min(20k, maxOutput)); checked after every step; provider overflow error also triggers. Compaction: hidden no-tools agent over serialized transcript (tool outputs cut 2000 chars), template Objective / Important Details / Work State / Next Move / Relevant Files; incremental with `<prior-summary>`; preserve recent tail clamp(25% usable, 2k..15k); after auto-compaction inject "Continue if you have next steps…". Pruning: protect last 40k tokens of tool output, replay pruned as "[Old tool result content cleared]" (off by default). Token estimate len/4. Cache markers: first 2 system messages + last 2 messages. [V]
- Permissions: rules `{permission, pattern, action}`; `findLast` wins; default ask; `.env` reads ask; reply once/always/reject; reject-with-message becomes feedback to model; command "arity" to build `always` patterns (`git checkout *`). [V]
- Storage: SQLite (session/message/part). Snapshots: separate bare git dir per project, `git add --all` + `write-tree` at step start, diff at step end; restore via read-tree/checkout-index; revert is soft until next prompt. [V]
- Agents: build, plan (edits denied except plan files), general, explore (read-only); hidden compaction/title/summary agents. Task tool creates child session, depth 1 default, returns `<task_result>`. Mode switch via `<system-reminder>` messages. [V]
- System prompt per model family (anthropic.txt, gpt.txt, gemini.txt, codex.txt…); `<env>` block; instruction files AGENTS.md > CLAUDE.md; lazy nested AGENTS.md on read. [V]
- LSP: after edit, report ERROR diagnostics (max 20) "LSP errors detected in this file, please fix:". [V]
- Client/server: HTTP API + SSE event bus; TUI is a client. MCP stdio/HTTP; custom commands `$ARGUMENTS $1 !\`cmd\``. Small model used for titles. [V]

## Codex CLI (openai/codex, codex-rs)

- Blogs: "Unrolling the Codex agent loop" (2026-01-23), "Unlocking the Codex harness" (App Server). [V]
- Turn loop (`core/src/session/turn.rs`): drain steering input → rebuild prompt from full history every request → sample → follow-up if tool calls or pending input → auto-compact if over limit → else Stop hooks. Requests `store:false`, include encrypted reasoning, `prompt_cache_key` = session id. [V]
- Tools start as soon as each output item completes (before stream ends); outputs recorded in call order. Concurrency: per-turn RwLock — parallel-safe tools take read lock, others write lock. Aborted turns get synthetic "aborted" outputs. [V]
- Cache discipline: each request is an exact prefix-extension; config changes appended as new messages (`<permissions instructions>`, `<environment_context>`) not edits; unstable MCP tool order once caused misses. Initial input order: permissions dev msg → developer_instructions → AGENTS.md/skills user msg → environment_context → user message. [V]
- `apply_patch` grammar (`*** Begin Patch`, `*** Add/Delete/Update File:`, `*** Move to:`, `@@` context, `+/-/space` lines, `*** End Patch`), freeform tool with Lark grammar; no line numbers; 3 lines context. Matching: exact → ignore trailing ws → ignore leading+trailing ws → unicode normalize. [V]
- Shell: `exec_command` + `write_stdin` (PTY sessions, yield_time_ms 10s default, max_output_tokens 10000), 64 processes max, 1 MiB head/tail buffer. [V]
- Sandbox modes read-only / workspace-write / danger-full-access; `.git` stays read-only. macOS: `/usr/bin/sandbox-exec -p <policy> -DWRITABLE_ROOT_0=… -- cmd`, base `(deny default)`, writable roots via `(allow file-write* (subpath (param …)))`, network policy only when enabled. Linux: bubblewrap + seccomp (Landlock legacy). Approval policies untrusted / on-request / never (on-failure now alias). Escalation: sandbox denial → ask user → rerun unsandboxed. [V]
- Auto-compact limit = min(config, 90% of context window). Compaction prompt "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM…"; new history = initial context + recent user messages up to 20k tokens + summary. Truncation: head+tail 50/50 with "…N tokens truncated…"; rollout keeps full payload. Token estimate 4 bytes/token. [V]
- AGENTS.md: walk to `.git` root, concat root→cwd, first of `AGENTS.override.md`, `AGENTS.md` per dir, 32 KiB budget. [V]
- Rollouts: `~/.codex/sessions/YYYY/MM/DD/rollout-…jsonl`, lines `{timestamp,type,payload}`; resume replays from newest `compacted` checkpoint. [V]
- MCP tool names `mcp__<server>__<tool>`; custom prompts replaced by skills; `codex exec --json` JSONL events. Core uses submission queue (Op) + event queue (EventMsg); TUI is an app-server (JSON-RPC) client. [V]

## Claude Code (official docs, Oct 2026)

- Edit: exact string replace, `old_string` must be unique or `replace_all`; no fuzzy matching. Read-before-edit (older models always; newer may skip if reading wouldn't prompt). Read: line-numbered, 25k token cap, offset/limit. Bash: fresh process per command, cwd persists, 2 min default / 10 min max timeout, ~30k chars inline else file path + preview. Tool responses capped at 25k tokens. [V]
- Auto-compact: clears older tool outputs first, then summarizes. After compaction re-injects root CLAUDE.md, memory, plan, git status, up to 5 recent files (≤5k tokens each), invoked skills. Summarize call reuses parent's exact system+tools+messages and appends the compaction prompt as a user message (cache hit). [V] (blog "Lessons from building Claude Code: prompt caching is everything", 2026-04-30)
- Memory: CLAUDE.md hierarchy managed → user → project → local, root-first, concatenated; delivered as a user message after system prompt; `@path` imports depth 4; subdir CLAUDE.md lazy; auto memory `MEMORY.md` index + typed topic files (first 200 lines / 25KB loaded). [V]
- Permissions: modes default / acceptEdits / plan / auto (classifier) / dontAsk / bypassPermissions. Evaluation deny → ask → allow, first match. `Bash(npm run *)`; compound commands split, each part must match; wrappers stripped; "not a security boundary". Sandbox: Seatbelt / bubblewrap, writes to cwd+tmp, network via proxy allowlist. [V]
- Hooks: PreToolUse, PostToolUse, UserPromptSubmit, Stop, SessionStart, PreCompact, … command handler receives JSON on stdin; exit 2 blocks (stderr to model); JSON output `permissionDecision`, `updatedInput`, `additionalContext`. [V]
- Skills: name+description in context (progressive disclosure), body loaded on invocation; custom commands merged into skills; `$ARGUMENTS`; `!\`cmd\`` inline shell. MCP tool schemas deferred via tool search. [V]
- Subagents: own context window and system prompt, tool allowlist, model, max turns; only final result returns; Explore/Plan read-only built-ins. [V]
- Caching rules: never add/remove tools mid-session; plan mode is tools + message not a tool swap; dynamic info goes in `<system-reminder>` in next message; don't switch models mid-session (delegate to subagent); known breakages: timestamps in static prompt, nondeterministic tool order. Prefix order tools → system → messages; ≤4 breakpoints; 5m/1h TTL; write 1.25x, read 0.1x. [V]
- Context engineering blog (2025-09-29): context rot, just-in-time retrieval, tool-result clearing as lightest compaction, NOTES.md, subagents return 1–2k token summaries. Long-running harness blog (2025-11-26): init.sh, progress file, JSON feature list. [V]

## Aider

- Edit formats: whole, diff (SEARCH/REPLACE), diff-fenced, udiff, editor-*; JSON function-call formats were worse for every model (2023 benchmark). Unified diffs: no line numbers; flexible patching disabled → 9x more edit errors. Architect/editor split improves scores (o1-preview+DeepSeek 85.0% vs 79.7%). [V]
- Repo map: tree-sitter defs/refs → file graph → PageRank personalized to chat files; 1k token default budget. [V]
- Lint/test loop: auto-lint after edits, errors shown with enclosing function context; `--test-cmd` failures trigger fixes. [V]

## Gateway probe results (2026-10-20, `docs/probe/probe.sh`)

Run against this account's gateway key. [V] = observed on the wire.

- **Team allowlist.** This account's team restricts models: `openai/gpt-5.4-nano` returned HTTP 403 `no_providers_available` / `RestrictedModelsError`. `GET /v1/models?include_availability=true` with auth adds `model_eligibility: {status: "eligible"|"ineligible", reason: "model_not_allowlisted"|"zero_data_retention"}`. 40 models eligible, including `anthropic/claude-{sonnet,haiku,opus}-5.5`, `openai/gpt-6-luna` ($0.10/$0.50 per M), `openai/gpt-6.1-sol`, `google/gemini-3.8-flash`, `deepseek/deepseek-v4.1-flash` ($0.075/$0.30), `zai/glm-5.3-flash`. [V]
- **Streaming tool calls** use the OpenAI delta shape: first chunk per call has `index`, `id`, `type`, `function.name`, `arguments:""`; later chunks carry only `index` + `function.arguments` fragments. Two parallel calls arrived with indexes 0 and 1 (sequential, not interleaved, in this run — interleaving must still be handled). Final chunk carries `finish_reason:"tool_calls"` **and** `usage` in the same chunk, then `data: [DONE]`. [V]
- **Usage** (Chat Completions, with `stream_options.include_usage:true`): `usage.prompt_tokens` is the *total* input including cached and cache-written tokens; `usage.prompt_tokens_details.cached_tokens` = cache read; `usage.cache_creation_input_tokens` = cache write; `usage.completion_tokens_details.reasoning_tokens`. Cost: `usage.cost` (number, inference cost), `usage.gateway_cost` (inference + surcharges), `usage.surcharge_cost`. This account pays a flat $0.0002/request surcharge (ZDR $0.0001 + provider allowlist $0.0001) — larger than the inference cost of a small Haiku request. Same data appears as strings in `choices[0].delta.provider_metadata.gateway.{cost,gatewayCost,surchargeCost,generationId,routing}` on the final chunk. [V]
- **Caching:** `providerOptions.gateway.caching:"auto"` works for Anthropic: request 1 `cache_creation_input_tokens: 8412`, request 2 `cached_tokens: 8412`; Sonnet 5.5 cost dropped from $0.0211 to $0.0009. `openai/gpt-6-luna` cached implicitly (5211 of 5214). `google/gemini-3.8-flash` showed no cache hit on two requests 2s apart (implicit caching is best-effort). [V]
- **Reasoning:** `reasoning:{effort:"medium"}` on trivial prompts produced no reasoning for Claude/GPT (adaptive thinking). With `reasoning:{enabled:true}` (or `effort:"high"`, or `max_tokens`) Claude Haiku 5.5 returns `reasoning_details:[{type:"reasoning.text", text:"", signature:"…", format:"anthropic-claude-v1", index:0}]` and `message.reasoning: null` — **thinking text is empty, only a signature comes back**. Gemini 3.8 Flash returns `reasoning.text` with visible text, `format:"google-gemini-v1"`, no signature. Streaming: `delta.reasoning` (string) and `delta.reasoning_details` fragments keyed by `index` (text and signature arrive in separate fragments; merge by concatenation). [V]
- **Replaying reasoning:** Claude tool-loop follow-up succeeded with merged `reasoning_details`, also succeeded with them omitted, and **failed with a tampered signature**: `messages.1.content.0: Invalid 'signature' in 'thinking' block`. Sending Anthropic `reasoning_details` to Gemini succeeded (gateway dropped or ignored them). Gemini replay without its details also succeeded. [V]
- **Context overflow:** HTTP 400 *before* any stream starts (also with `stream:true`): `{"error":{"message":"prompt is too long: 599026 tokens > 200000 maximum","type":"AI_APICallError","param":{…,"isRetryable":false}}}`. Gateway attempted 3 providers first. The test text `word0 word1 …` (1.2 MB) was 599k tokens ≈ 2 bytes/token, half the bytes/4 rule of thumb. [V]
- **Prefill and tool-less requests (2026-10-20):** Claude Haiku 5.5 and Sonnet 5.5 reject a conversation ending in an assistant message: "This model does not support assistant message prefill. The conversation must end with a user message." A history containing tool calls/results sent with **no** `tools` array was accepted by both via the gateway. [V]

## Memory across sessions (researched 2026-10-20)

- **Codex memories**: off by default (`[features] memories = true`; `codex-rs/features/src/lib.rs`). Stored in `~/.codex/memories/` (`memory_summary.md`, `MEMORY.md`, `raw_memories.md`, `rollout_summaries/`, `skills/`, `extensions/ad_hoc/notes/`), git-tracked. Extraction runs as a background job at the start of the next root session: phase 1 per eligible past session with `extract_model` (low effort), JSON `raw_memory`/`rollout_summary`, secrets redacted, "No-op is allowed and preferred"; phase 2 a consolidation sub-agent (no network, local writes) rewrites `MEMORY.md`/`memory_summary.md`/`skills/`. `memory_summary.md` is injected into developer instructions, capped at 2,500 tokens; the model greps `MEMORY.md` on demand and must cite memory use. Docs: treat memories as "a helpful recall layer, not as the only source for rules that must always apply." [V] https://learn.chatgpt.com/codex/customization/memories, `codex-rs/memories/README.md`, `codex-rs/ext/memories/src/lib.rs`. Doc/source mismatch (docs bug): `max_rollout_age_days` docs 30 vs source 10; `max_rollouts_per_startup` docs 16 vs source 2 (`codex-rs/config/src/types.rs`). [V]
- **opencode, pi**: no built-in memory beyond AGENTS.md/CLAUDE.md context files (opencode `rules.mdx`; pi `docs/configuration.md`); pi extensions can persist state via `pi.appendEntry()`. Absence inferred from source/doc search. [V/I]
- **Claude Code auto memory**: model decides what is worth remembering; "remember X" saves explicitly; types user/feedback/project/reference in `type` frontmatter; adds a `modified` timestamp; skips what can be derived from the codebase or CLAUDE.md; `~/.claude/projects/<project>/memory/` with `MEMORY.md` index (one line per memory) + topic files; first 200 lines or 25KB of MEMORY.md loaded at start, topic files on demand; reminders near the limit; subagents don't get it, forks do; toggles `/memory`, `autoMemoryEnabled`, `CLAUDE_CODE_DISABLE_AUTO_MEMORY`. [V] https://code.claude.com/docs/en/memory

## MCP protocol (researched 2026-10-20)

- Revisions: 2024-11-05, 2025-03-26, 2025-06-18, 2025-11-25, **2026-07-28** (latest; `LATEST_PROTOCOL_VERSION` in `schema.ts`). [V] https://github.com/modelcontextprotocol/modelcontextprotocol/tree/main/schema
- **2026-07-28 removes `initialize`/`notifications/initialized`.** Every request carries `params._meta` with `io.modelcontextprotocol/protocolVersion` and `.../clientCapabilities` (required), `.../clientInfo` (SHOULD); unsupported version → error `-32022` with `data:{supported,requested}`; servers MUST implement `server/discover` (supportedVersions, capabilities, serverInfo, instructions, ttlMs, cacheScope); results carry `resultType`. [V] https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning
- **2025-11-25 (legacy handshake):** client sends `initialize {protocolVersion, capabilities, clientInfo{name,version}}`; server echoes the version if supported, else returns one it supports; client then sends `notifications/initialized` (no id). [V] https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle
- `tools/list` → `{tools:[{name,title?,description?,inputSchema,outputSchema?,annotations?{readOnlyHint,destructiveHint,idempotentHint,openWorldHint}}], nextCursor?}`; `tools/call {name, arguments}` → `{content:[{type:"text",text}], isError?, structuredContent?}`; unknown tool = JSON-RPC error -32602; tool failure = `isError:true` in the result. [V]
- stdio: newline-delimited JSON-RPC, no embedded newlines, nothing else on stdout, logs on stderr (not an error), shutdown = close stdin → wait → SIGTERM → SIGKILL. [V] https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio
- Official Go SDK `github.com/modelcontextprotocol/go-sdk` v1.8.0 (2026-09-14) negotiates up to 2026-07-28. [V]
- Docs inconsistency: the 2026-07-28 tools page's JSON examples omit the required `_meta` that the schema repo examples include. [V]

## Details used by the lessons (from the 2026-10-19 source/doc reports)

The condensed sections above dropped some details the lessons quote. These come from the same research reports; tags as above, plus [B] = Zechner's blog post, [S] = pi docs/source, [O] = official docs page.

**pi**
- `bash`: `timeout?` in seconds, no default; keeps the last 2000 lines / 50KB, full output to a temp file. No background bash: "Use tmux instead … Bash is all you need." [B][S]
- System prompt: frontier models are "RL-trained up the wazoo", no need for "10,000 tokens of system prompt". The prompt has `<addendum>`, `<project_context>`, `<skills>`, `<cwd>` sections; each context file inside is wrapped in `<project_instructions path="…">`. [B][S]
- No max-steps: "I never found a use case for that." Terminal-Bench 2.0 run with Opus 4.5; the minimal Terminus 2 agent "also holds up". [B]
- Missing abort support is "entirely unacceptable". `KnownApi` (`ai/src/types.ts`) lists `openai-completions`, `openai-responses`, `anthropic-messages`, Google and others. [B][S]
- Tool args validated with TypeBox; failure text `Validation failed for tool "X": - path: msg Received arguments: {...}` becomes an `isError` result. [S]
- Safeguards are "mostly security theater"; once an agent can write and run code it's "pretty much game over"; run it in a container. [B]
- Sub-agents: "Using a sub-agent mid-session for context gathering is a sign you didn't plan ahead." Accepted use: a slash command that spawns `pi --print` (e.g. code review). [B]
- Sessions at `~/.pi/agent/sessions/--<cwd-with-dashes>--/<timestamp>_<uuid>.jsonl`; `--continue`, `--resume`, `/tree`, `/fork`. [S]
- Prompt templates `~/.pi/agent/prompts/*.md`; `$1`, `$@`/`$ARGUMENTS`, `${1:-default}`, `${@:N:L}`, shell-like quoting. Skills follow the Agent Skills spec; name, description and path go in the system prompt; `/skill:name args`. [S]
- Agent hooks: `transformContext` (prune/inject), `beforeToolCall`, `afterToolCall`. Extension events include `input`, `context`, `tool_call`, `tool_result`, `turn_end`, `session_before_compact`. [S]

**opencode** (source paths in `anomalyco/opencode`)
- `bash`: params `command`, `timeout` (ms), `workdir`; on timeout kills and returns a "retry with a larger timeout" message; metadata output capped at 30,000 chars, keeping the tail. [V]
- Per-model prompts in `session/system.ts`: `gpt-4|o1|o3` → `beast.txt`, `claude` → `anthropic.txt` ("You are OpenCode, the best coding agent on the planet."), fallback `default.txt`. [V]
- `session/llm.ts` calls AI SDK `streamText`; the AI SDK runs each tool's `execute`. Bad args: "The X tool was called with invalid arguments: … Please rewrite the input…" (`tool/tool.ts`). [V]
- TUI: OpenTUI + Solid; `cli/cmd/tui.ts` starts the server in a Worker; `serve`, `attach`, `web`, `acp` commands. [V]
- Storage: SQLite via drizzle (`~/.local/share/opencode/opencode.db`); HTTP routes for `fork`, `share`, `revert`, `unrevert`, `summarize`. [V]
- `prune()` runs after the loop exits, skips the last 2 user turns, protects 40,000 tokens (`PRUNE_PROTECT`), prunes only above 20,000 (`PRUNE_MINIMUM`); `compaction.prune` is "default: false". [V]
- Compaction update prompt uses `<prior-summary>`: "anything you do not carry into the new summary is lost". [V]
- Commands `{command,commands}/**/*.md` with `$ARGUMENTS`, `$1..$n`, `` !`cmd` ``; built-ins `/init`, `/review`; `skill` is an always-registered tool (`tool/registry.ts`). [V]
- Task tool params `description`, `prompt`, `subagent_type`, optional `task_id` (resume child session). [V]
- Plugin hooks `tool.execute.before` / `tool.execute.after`. [V]
- MCP: `local` = stdio; `remote` tries StreamableHTTP then SSE, with OAuth; tools named `sanitize(server)_sanitize(tool)`; MCP prompts become commands. [V]

**Codex** (source paths in `openai/codex`, `codex-rs/`)
- Provider defaults: stream idle timeout 300 s, stream_max_retries 5, request_max_retries 4 (`model-provider-info/src/lib.rs`). [V]
- Aborted calls get synthetic `"aborted"` outputs: `ensure_call_outputs_present` (`core/src/context_manager/normalize.rs`). [V]
- `codex exec`: in-process app-server client, approval `never`; `--json` events `thread.started`, `turn.*`, `item.*`; `--output-schema`, `--output-last-message`. [V]
- Submission queue bounded at 512 (`core/src/session/mod.rs`). TUI on ratatui + crossterm, an app-server client: JSON-RPC `thread/start|resume|fork`, `turn/start|interrupt`, `item/*` notifications; approvals are server-to-client requests. [V]
- Approval: dangerous commands prompt (forbidden under `never`); `untrusted` prompts unless a `.rules` file (`~/.codex/rules/default.rules`) allows it. `apply_patch` inside shell commands is intercepted with Tree-sitter Bash. Default `SandboxMode` is `read-only`. [V]
- Rollouts `~/.codex/sessions/YYYY/MM/DD/rollout-…-<uuid>.jsonl`, may be `.jsonl.zst`; `codex resume [--last|id]`, `codex fork`. [V]
- Compaction also runs pre-turn and async/speculative (`core/src/session/async_compaction.rs`); summary prefixed with `summary_prefix.md` ("Another language model started to solve this problem..."). [V]
- MCP tools listed in unstable order once caused cache misses (PR #2611). The Codex harness blog says a mid-conversation `tools/list_changed` causes a cache miss. [V][O]
- MCP client: names `mcp__<server>__<tool>`, max 128 chars, SHA-1 suffix on collision/truncation; timeouts startup 30 s, tool call 300 s. `codex mcp-server` removed 2026-09. Custom prompts removed 2026-03-28 (PR #16115), replaced by skills. [V]

**Claude Code** (code.claude.com docs unless noted)
- Bash: `cd` persists if it stays inside the working dirs; env vars don't persist; ~30,000 chars inline, beyond that a file path plus 2,000-char preview; `run_in_background: true`; a timed-out foreground command moves to the background. [O] /docs/en/tools-reference
- Read: 25,000-token cap; a whole file over 256 KB is refused. Tool responses capped at 25k tokens; tool-design advice: consolidate tools, concise/detailed `response_format`, semantic IDs, actionable errors. [O] /docs/en/tools-reference, https://www.anthropic.com/engineering/writing-tools-for-agents
- Docs advise keeping each CLAUDE.md under 200 lines. [O] /docs/en/memory
- Cache invalidators: timestamps in the static prompt, non-deterministic tool order, changing Agent-tool parameters; Anthropic alerts on cache hit rate and declares SEVs when it drops. [O] https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/
- Permissions: wrappers `timeout`/`nice`/`nohup`/`xargs` stripped; redirect targets checked against Edit/Read rules; rules are "not a security boundary" (`/bin/rm`, `sh -c`). Sandbox: Claude may retry with `dangerouslyDisableSandbox`, disabled by `allowUnsandboxedCommands: false`. [O] /docs/en/permissions, /docs/en/sandboxing
- `/rewind` can "Summarize from/up to here". [O] /docs/en/costs
- Compaction: about a 20k-token summary; configure with `/autocompact`; after a few thrashing attempts it stops with an error. [O] /docs/en/how-claude-code-works, prompt-caching blog
- Skills: descriptions truncated at 1,536 chars; supporting files load only when read; frontmatter `allowed-tools`, `model`, `disable-model-invocation`, …; `$ARGUMENTS` / `$0`. [O] /docs/en/skills
- Auto memory: "Claude doesn't save something every session…"; near the 200-line/25KB limit it reminds Claude to shorten the index; over the limit it returns an error asking Claude to rewrite it. [O] /docs/en/memory
- Subagents: fields `tools`, `disallowedTools` (wins), `model`, `permissionMode`, `maxTurns`, `isolation: worktree`; built-ins Explore, Plan, general-purpose; subagents get "only this system prompt plus basic environment details… not the Claude Code system prompt". [O] /docs/en/sub-agents
- Hooks: events include PermissionRequest, PostToolUseFailure, PostToolBatch, SubagentStart/Stop, PreCompact/PostCompact, Notification, SessionEnd, FileChanged; handler types `command`, `http`, `mcp_tool`, `prompt`, `agent`; matching hooks run in parallel; PostToolUse `updatedToolOutput`; `decision: "block"`. [O] /docs/en/hooks
- MCP: scopes local (`~/.claude.json`), project (`.mcp.json`), user; tool search defers schemas ("only names enter context"); `ENABLE_TOOL_SEARCH=auto` loads them up front if they fit in 10% of the window; deferred stubs use `defer_loading: true`; output warning at 10k tokens, `MAX_MCP_OUTPUT_TOKENS` 25k, results over 50k chars saved to a file. [O] /docs/en/mcp, /docs/en/context-window

**Anthropic API**
- Context editing `clear_tool_uses_20250919`: trigger default 100k input tokens, keep 3 tool uses, `clear_at_least` (clearing invalidates the cache). Server-side compaction: `compact_20260112`. [O] https://platform.claude.com/docs/en/build-with-claude/context-editing
- Memory tool `memory_20250818`, client-side; commands `view`, `create`, `str_replace`, `insert`, `delete`, `rename` under `/memories`; injected "ALWAYS VIEW YOUR MEMORY DIRECTORY BEFORE DOING ANYTHING ELSE…". [O] /docs/en/agents-and-tools/tool-use/memory-tool
- Long-running harness post: initializer writes `init.sh`, `claude-progress.txt`, a feature JSON. [O] https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents

**Aider**
- Failed hunks are re-applied with relative-indentation matching. [O] https://aider.chat/docs/unified-diffs.html
- Lint errors shown with enclosing function/class "because LLMs handle line numbers poorly"; `--test-cmd` + `--auto-test`. SWE-bench Lite 26.3%; "plausible" = no edit/lint/test errors; up to 6 attempts; the first two produced about 90% of resolved solutions. [O] https://aider.chat/2024/05/22/linting.html

**Measured in this tutorial**
- Gateway catalog: `anthropic/claude-haiku-5.5` `input_tiers` = $0.10/M up to 100k tokens, $0.50/M above (5×). Checked 2026-10-20 with `GET /v1/models`.
- Lesson 01 bench: DeepSeek V4.1 Flash streams its reasoning (4,492 reasoning tokens, first token 0.75 s, answer at 14 s); Claude Haiku 5.5 returns only a signature, Gemini returns text without a signature.

**Re-verified 2026-10-20** (live docs, after the lesson review flagged them)
- `claude -p` is print (non-interactive) mode; `--output-format text|json|stream-json`. [O] /docs/en/headless, /docs/en/cli-reference
- `opencode run [message..]` runs non-interactively; `--format default|json` (json = raw JSON events). [O] https://opencode.ai/docs/cli/
- Claude Code MCP: "The SSE (Server-Sent Events) transport is deprecated. Use HTTP servers instead"; scopes local / project / user (plus managed). [O] /docs/en/mcp
- Subagents can spawn subagents "up to three layers below the main conversation" (`CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH`); 20 running subagents is the default concurrency limit (`CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS`). Earlier versions differed (v2.1.172–216 nested up to five; 217–218 defaulted to one; 219 raised it to three). [O] /docs/en/sub-agents
- Hooks reference lists `PreModelSwitch` (can block a model switch) and `PostModelSwitch`, `FileChanged` (matcher = filenames), `PostToolBatch` (no matcher). [O] /docs/en/hooks
- Gateway stream chunks carry `model` = the requested ID (checked 2026-10-20 for `anthropic/claude-haiku-5.5`, `google/gemini-3.8-flash`, `openai/gpt-6-luna`). Lesson 19's transform relies on it.
