# Lesson 07: Headless Mode and Evaluation

## What you'll build

- **`harness exec`**: a non-interactive mode that runs one prompt and prints events as JSONL.
- **An eval runner**: it copies each task into a temp directory, runs the harness on it, then reveals hidden tests and scores the result. Every run's trace is saved.
- **A 16-task suite** of verifiable Go tasks.

From here on, a change to the harness can be measured, not just tried once. The demo compares three models: 144 runs, with repeats and spread.

## How the real harnesses do it

Every harness has a headless mode, because CI, scripts and evals need one:

| | Command | Output |
|---|---|---|
| Codex | `codex exec "…"` | `--json` JSONL events (`thread.started`, `turn.started/completed`, `item.*`); approval defaults to `never`; `--output-schema`, `--output-last-message` |
| Claude Code | `claude -p "…"` | text or `--output-format stream-json` |
| pi | `pi --print "…"` | text; pi's own slash commands spawn `pi --print` for things like code review |
| opencode | `opencode run "…"` | text or JSON; also the HTTP server API |

**Public benchmarks.** SWE-bench gives the agent a GitHub issue and scores it on that repo's hidden tests. Terminal-Bench gives terminal tasks with scripted checks. Zechner reported pi with Opus 4.5 on Terminal-Bench 2.0 as evidence that "a minimal approach can do just as well". Aider's lint/test results on SWE-bench Lite are another example (lesson 17).

**What harness teams say about evals:**
- Anthropic's "Writing effective tools for agents" builds tools by iterating against evals.
- The Claude Code team monitors prompt-cache hit rate and treats drops as incidents (lesson 12).

Prompts, tool descriptions and edit strategies are all hypotheses until an eval says otherwise.

## Design for our harness

### Exec mode

`harness exec --json --root DIR "prompt"`:
- runs one `Agent.Run`;
- prints every event except text and thinking deltas as one JSON line (the `message_end` event carries the full text);
- exits with 0 on success, 1 on agent failure, 2 on bad usage.

There's nobody to confirm commands. Run `exec` in an isolated environment: a throwaway working directory alone does not stop commands from accessing other files or the network. Lesson 09 adds a macOS sandbox; it does not implement one for Linux.

### A task is a directory

```
testdata/evals/03-feature-slugify/
  task.md        the prompt, exactly what a user would type
  repo/          starting workspace (its own go.mod)
  check/         copied in AFTER the agent finishes: hidden tests + check.sh
  solution/      reference answer, used only by TestReferenceSolutionsPass
```

`check.sh` runs `gofmt -l`, `go vet` and `go test`. A task passes iff it exits 0. The suite lives under `testdata/`, so the Go tool ignores it and task code never compiles into the harness.

The tasks cover different skills:
- **bug fixes (01, 02):** finding the cause from a symptom;
- **features from a spec (03, 04, 05, 11, 14):** semver precedence, and a rate limiter specified only in a doc file;
- **refactors that must keep behavior (06, 07):** checked by inspecting the AST and grepping for leftover names;
- **multi-file changes (08, 16)**;
- **exploration in a 14-file repo (09)**;
- **following `AGENTS.md` conventions (10)**;
- **concurrency under `-race` (12), performance under a deadline (13), a needle in a repetitive file (15).**

`TestReferenceSolutionsPass` checks every task both ways: the starting repo fails and repo + solution passes. A task that already passes, or that can't be passed, measures nothing.

### The runner

```
for config × repeat × task (Parallel at a time):
  tmp := copy(task/repo)
  harness exec --json --root tmp --model M --trace traces/<config>/<task>-<rep>.jsonl  "<task.md>"
      parse JSONL: turns (turn_start count), usage (agent_end)
      timeout → kill the process group (lesson 05's lesson, again)
  copy(task/check) → tmp;  sh check.sh → pass/fail
```

**Repeats and spread.** Models are stochastic. One run per task reports noise as signal. `Summarize` reports the pass rate and the sample standard deviation of the per-repeat pass rates, plus mean cost, turns and wall time. **Always save traces.** A failing check tells you *that* something went wrong; the trace tells you *why* (see the demo).

## The contract

`harness/internal/eval`:

```go
type Task struct { Name, Prompt, Dir string }
func LoadSuite(dir string) ([]Task, error)             // subdirs with task.md, sorted by name

type RunConfig struct {
    Name, Model string
    Repeats, Parallel int
    Timeout time.Duration     // default 10m
    HarnessArgs []string
    TraceDir string           // save traces as <TraceDir>/<Name>/<task>-<rep>.jsonl
}
type Outcome struct {
    Task, Config string; Rep int; Pass bool
    Turns, InTok, OutTok, CacheRead int; CostUSD float64; Wall time.Duration
    Err, CheckOutput, Trace string
}
func Run(ctx context.Context, tasks []Task, cfgs []RunConfig, harnessBin string) ([]Outcome, error)

type Summary struct {
    Config string; Runs, Passes, Errors int
    PassRate, PassStd, MeanTurns, MeanCost float64; MeanWall time.Duration
}
func Summarize(outs []Outcome) []Summary               // one per config, first-seen order
func ByTask(outs []Outcome) map[string]map[string]float64
```

Provided in `support/`:
- `cmd/harness`, with the `exec` subcommand and its test;
- `internal/eval/report.go` (`PrintSummary`, `WriteHTML`);
- `internal/eval/tasks_test.go`;
- `testdata/evals/` (the 16 tasks);
- `cmd/evalrun`.

## Build it

Continue from lesson 06 and copy this lesson’s `tests/` and `support/`. The `internal/eval/` implementation is new; the headless command, report code, and evaluation tasks are supplied.

1. **`LoadSuite`.** Discover the tasks the harness will attempt before their checks are revealed. Test: `go test ./internal/eval -run TestLoadSuite`.
2. **`Run`.** Tests: `-run 'TestRunHides|TestRunRecords|TestRunTimeout'`. The test harness is `testdata/fake-harness.sh`: it fails if it can see `check.sh`, so you can't accidentally copy the checks in early.
   - Use `exec.CommandContext` with `SysProcAttr{Setpgid: true}`, `cmd.Cancel` killing `-pgid`, and `cmd.WaitDelay`. `TestRunTimeout` runs a script that does `sleep 30`. The default `Cancel` kills only `sh`; its `sleep` child keeps the stdout pipe open, so `Wait` returns when `WaitDelay` (2 s) expires, or after 30 s without `WaitDelay`. With the group kill the test takes about 0.3 s. Lesson 05 fixed the same bug in the bash tool.
   - Parse stdout line by line into `agent.Event` while the process runs.
3. **`Summarize` and `ByTask`.** Tests: `-run 'TestSummarize|TestByTask'`. The standard deviation is taken over repeats, not individual runs, because the variance you care about is "if I ran the whole suite again".
4. Run `go test ./...`. This includes `TestReferenceSolutionsPass`, about 15 s; skip it with `-short`.

## Run it

```sh
go run ./cmd/harness exec --root /tmp/textkit "Implement Reverse so the tests pass."      # readable output
go run ./cmd/harness exec --json --root /tmp/textkit "…" | jq -c '{kind, turn}'          # the event stream
go run ./cmd/evalrun --config haiku=anthropic/claude-haiku-5.5 --repeats 1 --tasks 01-bugfix-paginate
```

## Demo

```sh
go run ./cmd/evalrun \
  --config sonnet-5.5=anthropic/claude-sonnet-5.5 \
  --config haiku-5.5=anthropic/claude-haiku-5.5 \
  --config gpt-6-luna=openai/gpt-6-luna \
  --repeats 3 --parallel 6
```

Result (16 tasks × 3 repeats × 3 models = 144 runs, 8 min, $2.16):

```
CONFIG      PASS   RATE ± SD  MEAN COST  MEAN TURNS  MEAN TIME  ERRORS
sonnet-5.5  48/48  100% ± 0   $0.0407    4.1         11s        0
haiku-5.5   47/48  98% ± 4    $0.0031    5.0         15s        0
gpt-6-luna  48/48  100% ± 0   $0.0012    6.4         22s        0

per task (pass rate over repeats)
TASK                      sonnet-5.5  haiku-5.5  gpt-6-luna
07-refactor-rename        ███ 100%    ██· 67%    ███ 100%
(all other tasks          ███ 100%    ███ 100%   ███ 100%)

  fail  haiku-5.5/07-refactor-rename rep 0: vet: ./hidden_test.go:11:8: undefined: User
        (trace: eval-traces/haiku-5.5/07-refactor-rename-0.jsonl)
```

`eval-report.html` shows the same data with error bars and one square per run.

What it shows:

- **The suite is close to saturated.** All three models pass nearly everything. A suite that everything passes can't tell configurations apart. The first version had only tasks 01–10 and scored 100/100/100; tasks 11–16 were added to make it harder, and still mostly pass. On pass rate, these models are indistinguishable here.
- **Cost and speed still separate them sharply.** Sonnet costs 13× Haiku and 34× Luna per task. Luna takes twice as long, from hidden reasoning (lesson 01) and more turns. Later lessons that compare harness changes (caching, hooks, routing) rely mostly on cost, turns and time, plus the rare failures.
- **The trace explains the failure.** Haiku ran `sed -i '' 's/\bUsr\b/User/g' …`. macOS ships BSD `sed`, which doesn't support `\b`, so nothing changed. The model then reported "no `Usr` remains in the package" without checking. Two lessons in one trace:
  - The environment block should probably say which `sed` is installed. (lesson 06; inference, not measured)
  - The system prompt (`internal/prompt/templates/anthropic.md`) already says "build it and run the relevant tests" and "Do not claim something works unless you ran it". Haiku said it verified, and didn't. An instruction is not enforcement; lesson 17's stop hook runs the tests itself.
- **The eval itself had a bug.** In an earlier run Haiku wrote its own `TestSumCancel` in task 12, which collided with the hidden test of the same name: `TestSumCancel redeclared`. The model's work was fine; the harness of the eval was wrong. Hidden tests are now all named `TestHidden*`. Read failing traces before blaming the model.

## Exercises

1. Add a task your model fails at least once in three runs. Write the reference solution first, and confirm the starting repo fails its hidden check. What behavior does the task measure?
2. Compare the original prompt with a shorter variant (such as the one proposed in lesson 06). Change the template and rebuild the harness for each run. Run repeated evaluations and inspect failures before attributing any difference to the prompt.

## Further reading

- SWE-bench: https://www.swebench.com
- Terminal-Bench: https://www.tbench.ai
- Anthropic, "Writing effective tools for agents" (evaluation-driven tool design): https://www.anthropic.com/engineering/writing-tools-for-agents
- Codex `exec --json`: `codex-rs/exec/` in https://github.com/openai/codex
