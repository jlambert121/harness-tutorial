# SDD ledger — plan: docs/superpowers/plans/2026-10-20-harness-tutorial.md
Note: not a git repo; no commits. Checkpoints are lesson solution/ dirs.
Pre-flight: shared interfaces centralized in plan "Shared Interfaces"; consumers in Tasks 2-21 reference those names. No conflicts found at scan.
Task 1: Ruling: demo/bench model set = sonnet-5.5, haiku-5.5, openai/gpt-6-luna, google/gemini-3.8-flash, deepseek/deepseek-v4.1-flash — team allowlist blocks gpt-5.4-nano/gemini-3.5-flash-lite — cost if wrong: readers on other accounts edit a flag default.
Task 1: Ruling: FetchCatalog(ctx, baseURL, apiKey) gains apiKey ("" = anonymous) and ModelInfo gains Eligibility — needed to show the team allowlist — cost if wrong: one param.
Task 1: Ruling: Usage.CostUSD = usage.cost (inference); add Usage.SurchargeUSD from usage.surcharge_cost — account surcharges dominate small requests — cost if wrong: one field.
Task 1: complete (lesson 00: tests ok, reader-path ok, demo run live)
Task 2: Ruling: everything under cmd/ is support (provided, overwritten each lesson); reader writes internal/* except packages a lesson marks as provided (llm/fake, trace html, report html) — keeps lessons on harness concepts — cost if wrong: reader wants to write main wiring themselves (exercise).
Task 2: Ruling: llm.WithPricing(map[string]Pricing) option fills CostUSD when gateway omits usage.cost — cost if wrong: none.
Task 2: complete (lesson 01: go test ok 25 tests, reader-path ok, bench run live)
Task 3: Ruling: lesson 02 registry is minimal (Register/Specs/Execute sequential, hand-written schema); SchemaFor/Decode move to lesson 03 — keeps lesson 02 on the loop — cost if wrong: none.
Task 3: Ruling: max-steps final turn keeps tools but sends tool_choice:"none" plus a user-role reminder (request-only, not persisted) instead of opencode's assistant message — Anthropic rejects tool_use history without tools, newer models reject assistant prefill — cost if wrong: wording differs from opencode.
Task 3: Ruling: internal/ui/repl and internal/trace are support (UI/visualization, not loop concepts) — cost if wrong: none.
Task 3: complete (lesson 02: go test ok, reader-path ok, live demo trace run)
Task 4: Ruling: parallel execution = consecutive read-only calls overlap; a mutating call waits for earlier calls and blocks later ones (deterministic order) instead of Codex's free RwLock race — cost if wrong: slightly less parallelism.
Task 4: Ruling: lesson-02 agent_test.go updated to use tools.Read()/"read"; read_file.go deleted — README tells reader — cost if wrong: none.
Task 4: Ruling: grep/glob implemented in Go (no ripgrep dependency) with a simple root-.gitignore matcher — cost if wrong: slower than rg on huge repos.
Task 4: complete (lesson 03: go test -race ok, reader-path ok, toolbench offline+live run)
Task 5: Ruling: matching strategies are equal-length line windows (exact, trailing_ws, trimmed, unicode), so a "disproportionate match" cannot occur; TestDisproportionateMatchRejected replaced by TestNearMissDoesNotMatch — cost if wrong: none.
Task 5: Ruling: internal/diff (LCS unified diff) is support — algorithm, not harness concept — cost if wrong: none.
Task 5: Ruling: corpus for editbench is a Go table in cmd/editbench (not JSON files) — cost if wrong: none.
Task 5: complete (lesson 04: go test -race ok, reader-path ok, editbench + live edit run)
Task 6: Ruling: bash merges stdout+stderr into one head/tail buffer (1 MiB) and relies on the registry cap for model-facing truncation; no background processes — children left after the shell exits are killed with the group — cost if wrong: long-running servers need exercise/background support.
Task 6: Ruling: final cwd captured via fd 3 (pwd >&3) instead of parsing output — cost if wrong: none.
Task 6: Ruling: interim confirm installed by repl.Run via Agent.Tools().Env().Confirm — replaced in lesson 09 — cost if wrong: none.
Task 6: complete (lesson 05: go test -race ok, reader-path ok, shelltorture run)
Task 7: complete (lesson 06: go test ok, reader-path ok, inspect + live task run)
Task 8: Ruling: tasks live at eval/tasks/<name>/{task.md,repo/,check/,solution/}; solution/ overlays repo/ and is verified by TestReferenceSolutionsPass (instead of solution.patch) — cost if wrong: none.
Task 8: Ruling: exec mode lives in cmd/harness (support); reader writes internal/eval (runner, stats) — cost if wrong: none.
Task 8: Ruling: eval suite lives in testdata/evals (go tool ignores testdata, so task code is not compiled into the harness module) — cost if wrong: none.
Task 9: complete (lesson 08: go test ok, reader-path ok, pty live run + replay snapshot)
Task 10: Ruling: permission decisions are recorded in tool result text (e.g. "[denied by rule bash(rm *)]") instead of new agent event kinds — permission pkg cannot import agent (cycle) and the UI implements Approver directly like Confirm — cost if wrong: traces lack structured permission events (exercise).
Task 10: Ruling: bash -c/sh -c literal arguments are parsed recursively; eval, command substitution, and variable command names are "dynamic" and always ask — cost if wrong: none.
Task 10: complete (lesson 09: go test ok, reader-path ok, redteam run, sandboxed eval smoke 3/3)
Task 8: complete (lesson 07: go test ok, reader-path ok, 144-run eval, traces inspected; suite expanded to 16 tasks, hidden tests renamed TestHidden*)
Task 11: Ruling: agent persists through Config.Persist func(llm.Message) called on every history append, and Agent.Load(history) restores — simpler than the agent knowing about the store — cost if wrong: none.
Task 11: Ruling: snapshot per user prompt (agent_start), not per step; /undo restores files and branches the session to before that prompt — cost if wrong: coarser undo than opencode's per-step patches.
Task 11: Ruling: /undo is special-cased in the REPL via a small command map until lesson 14's command system; TUI gets it in lesson 14 — cost if wrong: TUI users lack /undo for 4 lessons.
Task 11: complete (lesson 10: go test ok, reader-path ok, demo.sh crash/resume/undo/branch live; fixed branch persistence + yolo dynamic bugs, backported dynamic fix to 09)
Task 12: Ruling: pruning is a request-level transform (pi's transformContext): Config.Transform rewrites the messages sent, history and session keep full content — cost if wrong: re-pruning each request (deterministic, so cache-stable).
Task 12: Ruling: agent emits a "context" event per step with estimate/usable/pruned from the transform — needed for the meter — cost if wrong: none.
Task 12: complete (lesson 11: go test ok, reader-path ok, ctxsim offline + live calibration)
Task 13: Ruling: compaction entry stores the full replacement history (Codex's replacement_history), and Store.Messages restarts from the latest compaction on the path — cost if wrong: larger session files.
Task 13: Ruling: summarize call = parent's system + tools + the summarized prefix of history + compaction prompt as a user message, tool_choice none (prefix cache hit, Claude Code pattern) — cost if wrong: none.
Task 13: Ruling: compaction trigger = estimate > 0.9 × usable (Codex), KeepRecent = min(20000, usable/3) — cost if wrong: tune.
Task 13: complete (lesson 12: go test ok, reader-path ok, cachelab 90 runs)
Task 15: complete (lesson 14: go test ok, reader-path ok, skillcost live)
Task 16: Ruling: the memory tool maintains MEMORY.md itself (index line from each topic file's frontmatter) rather than asking the model to edit the index — fewer broken indexes — cost if wrong: less flexible index.
Task 16: complete (lesson 15: go test ok, reader-path ok, memorydemo 3/3 vs 0/3)
Task 17: Ruling: subagent events and usage reach the parent through a context value set by the agent (agent.WithRunContext), since tools cannot import agent — child usage is added to the parent's totals so eval cost stays correct — cost if wrong: none.
Task 17: Ruling: child Env is a copy of the parent's (own Cwd and ReadTracker; shared Perm, Sandbox, Root) — parallel children must not share bash cwd — cost if wrong: none.
Task 17: complete (lesson 16: go test -race ok, reader-path ok, subagentlab 6 runs)
Task 19: complete (lesson 18: go test ok, reader-path ok, mcpcost live; fixed empty tool-args serialization bug in all lessons' llm client)
Task 14: complete (lesson 13: go test ok, reader-path ok, tinywindow long-session demo 13/13 vs 8/13; fixed split-point and re-injection sizing bugs)
Task 20: Ruling: per-step model choice via Config.PickModel(step, history) hook in the agent (routers live in internal/route); history is transformed for the target model inside the agent before each request — cost if wrong: none.
Task 21 (partial): lesson 20 README written from research notes (no new code)
Task 18: complete (lesson 17: go test ok, reader-path ok, hooklab 192 runs + manual hook check)
Task 20: Ruling: /model pins the model and disables the router for the rest of the session — a manual choice the router silently undid was a bug (TestModelCommandOverridesRouter RED→GREEN) — cost if wrong: the user must restart to get routing back.
Task 20: Ruling: routelab reports step-1 picks separately from mid-run switches — the heuristic's 42 "switches" all happened before the first request and cost no cache — cost if wrong: none (needed a rerun, $1.32).
Task 20: complete (lesson 19: go test ok, tests/support cmp ok, routelab 192 runs; README written)
Final: fixed retry jitter past cap (TestBackoffNeverExceedsCap), tool-args always valid JSON (TestToolArgsAlwaysValidJSON), template shell output substitution (TestExpandLeavesShellOutputAlone), memory frontmatter/index guard (TestStrReplaceKeepsFrontmatter, TestIndexNameAnyCase), subagent hooks (TestSubagentToolsRunHooks), hook errors reported (TestHookFailureIsReported), shared tool_search (TestDeferredSearchSpansServers), /model vs router (TestModelCommandOverridesRouter) — all 20 solutions gofmt/vet/test green.
Final: Ruling: README claims the review found unsourced were checked against the original research reports and added to research-notes.md ("Details used by the lessons"); five doubtful ones re-verified against live docs 2026-10-20 — all held — cost if wrong: a stale claim in a README.
Final: Ruling: lessons 14–19 carried the old eval-task tinywindow; replaced with lesson 13's long-session version — cost if wrong: none.
Final: minor (deferred): with --router heuristic in an interactive session, each new prompt re-picks the model, so a session can switch (and pay a cold cache) between prompts; documented in lesson 19, not changed.
Final: minor (deferred): lesson 13 ShouldCompact/Compact see unpruned history, so pruning never delays compaction and the summarize prefix can miss the cache after pruning; documented in lesson 13, not changed.
Task 21: complete (fix pass done, all 20 solutions green, tests/ and support/ byte-identical to solutions, no banned word, no files in ~/.local/share/harness or ~/Library/Caches/harness-redteam-*)
