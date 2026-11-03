# Lesson 03: Tool Design

## What you'll build

Four read-only tools (`read`, `glob`, `grep`, `ls`), built on three pieces of infrastructure:

- **Schemas generated from Go structs** by supplied code.
- **Argument validation** whose error messages the model can act on.
- **Output limits** that keep one tool call from flooding the context.

The registry also learns to run independent read-only calls in parallel. The demo measures the speedup and shows a model working its way past an empty result.

## How the real harnesses do it

Tool definitions are prompts. The model decides which tool to call, and with what arguments, based only on the name, description and schema. Anthropic's "Writing effective tools for agents" makes the same point: consolidate tools, return meaningful context instead of raw IDs, write errors that say how to fix the call, and cap responses. Claude Code caps tool output at 25k tokens.

| | pi | opencode | Codex | Claude Code |
|---|---|---|---|---|
| Read limits | 2000 lines / 50 KB, `offset`/`limit` | 2000 lines, 2000 chars per line, 50 KB | no read tool in the researched source; the model reads through the shell tool | 25k tokens, `offset`/`limit`, files over 256 KB refused |
| Generic output cap | bash: last 2000 lines / 50 KB, full output to a temp file | every tool: 2000 lines / 50 KB, full output saved, hint to use grep or a subagent | head + tail 50/50, "…N tokens truncated…" | ~30k chars, then path + preview |
| Validation | TypeBox schema; failure becomes an `isError` result | Effect schema; "invalid arguments… Please rewrite the input" | not covered in research | not documented |
| Parallelism | parallel by default; per-file write queue | AI SDK calls each tool's `execute` | per-turn RwLock: parallel-safe tools share, others exclusive | not documented in detail |

Three ideas recur:

1. **Truncate, but say so and say how to get the rest.** A silent cut makes the model think it saw everything. Saving the full output and returning its path (pi, opencode, Claude Code) turns the cut into a paging problem the model can solve itself.
2. **Validation errors are the model's feedback channel.** pi formats them as `Validation failed for tool "X": - path: msg`. The model fixes the call on the next step without the user getting involved.
3. **Read-only tools are safe to run concurrently.** Mutating tools need ordering.

## Design for our harness

### Schemas from structs

```go
type readArgs struct {
    Path   string `json:"path" desc:"File path, relative to the workspace root or absolute"`
    Offset int    `json:"offset,omitempty" desc:"1-based line number to start from (default 1)"`
    Limit  int    `json:"limit,omitempty" desc:"Maximum number of lines to return (default 2000)"`
}
tools.New("read", readDescription, true, func(ctx context.Context, env *tools.Env, a readArgs) tools.Result { ... })
```

`SchemaFor[T]` reflects over the struct. A field without `omitempty` is required. `additionalProperties: false` is set so that a misspelled field produces an error and isn't silently ignored. `tools.New` wires `SchemaFor` and `Decode` together, so each tool body receives typed, validated arguments.

### Errors written for the model

| Bad call | Error |
|---|---|
| `{"limit":5}` | `invalid arguments: missing required field "path".` |
| `{"path":"a","colour":"red"}` | `invalid arguments: unknown field "colour"; valid fields are: path, offset, limit.` |
| `{"path":"a","limit":"ten"}` | `invalid arguments: field "limit" must be integer, got string "ten".` |
| grep `fmt.Printf(` | `pattern is not a valid regular expression: … To match literal text, escape special characters, e.g. fmt\.Printf\(` |

Each one names the field, what was expected, and how to fix it.

### Output limits: three layers

1. **The tool limits itself where it understands its data.** `read` returns 2000 lines, clips lines over 2000 chars, and appends `(Showing lines 1-2000 of 2500. Use offset=2001 to continue.)`. `glob` and `grep` stop at 100 results and say how many there were.
2. **The registry caps everything** at 2000 lines / 50 KB using head + tail (`TruncHeadTail`). The full output goes to `Env.OverflowDir`, and the model gets the path.
3. **`read` may open files in the overflow directory** even though it's outside the workspace, so the model can page through what was cut.

`Truncate` must cut on rune boundaries. Half a UTF-8 sequence produces invalid JSON, or replacement characters, in the next request.

### Parallel execution

```
calls:   read  read  grep  edit  read  read
batches: [read read grep] [edit] [read read]
```

Consecutive read-only calls run together. A mutating call waits for everything before it and blocks everything after it. Codex uses an RwLock instead: tools marked parallel-safe share the read lock, every other tool takes the exclusive write lock. Our batching is stricter, and its result is the same as running in call order, which is what the model expects when it says "edit, then read". `onStart` fires in call order; `onEnd` fires as calls finish, with callbacks serialized so sinks don't need locks.

The model has to actually *request* calls together for this to help. The system prompt now says: "When several files or searches are independent, request them in the same response."

### Searching without ripgrep

Real harnesses shell out to `rg`. Ours walks the tree in Go. That's slower, but has no dependencies. `support/internal/tools/ignore.go` provides `walk`, `matchGlob` (with `**`) and a subset of `.gitignore`: no negation, root file only. The ignore rules matter more than they look. Without them, `grep` over a JavaScript project returns `node_modules` and burns the context.

## The contract

`harness/internal/tools` (additions):

```go
type Env struct { Root, OverflowDir string }      // OverflowDir "" = $TMPDIR/harness-overflow
type Registry struct { Parallel bool; ... }        // NewRegistry sets Parallel = true

func SchemaFor[T any]() json.RawMessage
func Decode[T any](args json.RawMessage) (T, error)   // errors start with "invalid arguments"
func New[Args any](name, description string, readOnly bool,
    fn func(ctx context.Context, env *Env, args Args) Result) Tool

type TruncMode int
const (TruncHead TruncMode = iota; TruncTail; TruncHeadTail)
const (MaxLines = 2000; MaxBytes = 50 * 1024; MaxLineChars = 2000)
func Truncate(s string, maxLines, maxBytes int, mode TruncMode) (out string, truncated bool)
func SaveOverflow(env *Env, s string) (path string, err error)

func Read() Tool   // "read":  {path, offset?, limit?}; lines as "%6d\t%s"
func Glob() Tool   // "glob":  {pattern, path?}
func Grep() Tool   // "grep":  {pattern, path?, include?, mode?: files|content|count}
func Ls() Tool     // "ls":    {path?, depth?}
```

**Delete `read_file.go`.** `read` replaces it. The updated `tests/internal/agent/agent_test.go` uses `tools.Read()`.

Provided in `support/`: `internal/tools/schema.go` (schema generation), `internal/tools/ignore.go` (`walk`, `matchGlob`, `loadIgnore`), `cmd/toolbench`, and an updated `cmd/harness` that registers all four tools.

## Build it

Continue from lesson 02 and copy this lesson’s `tests/` and `support/`. Work in your existing `internal/tools/` package; `schema.go` and `ignore.go` are supplied.

1. **Inspect `SchemaFor`.** The supplied function generates the schema that tells the model what arguments a tool accepts. Compare the struct and expected schema in `TestSchemaFor`, including required fields and descriptions; you will connect it to validation in the next steps.
2. **`Decode`.** Validate model-supplied arguments and return errors the model can correct. Tests: `-run TestDecode`.
   - Unmarshal into `map[string]json.RawMessage` first. That lets you report unknown fields (listing the valid ones) and missing required fields.
   - Then unmarshal each field on its own to find type mismatches.
   - `kindOf` reports what the model actually sent.
3. **`New`.** About ten lines: `SchemaFor` for the spec, `Decode` in the wrapper.
4. **`Truncate` and `SaveOverflow`.** Tests: `-run 'TestTruncate|TestSaveOverflow'`. Work line by line within both budgets. When a single line exceeds the byte budget, cut it at a `utf8.RuneStart` boundary. The marker states lines kept vs. total.
5. **`read`.** Tests: `-run TestRead`.
   - Refuse binaries: a NUL byte or invalid UTF-8 in the first 8 KB. Trim a partial trailing rune before the validity check.
   - Number lines `%6d\t`, and add the continuation footer when the result is partial.
   - The 2000-line default and the 50 KB budget both apply; stop at whichever comes first.
6. **`glob`, `grep`, `ls`.** Tests: `-run 'TestGlob|TestGrep|TestLs'`. Use `walk` and `matchGlob`. For an invalid regex, return the compiler error plus a `regexp.QuoteMeta` suggestion.
7. **Registry: capping and parallel batches.** Tests: `-run 'TestRegistry|TestParallel|TestMutating|TestSequential'`.
   - Apply `capOutput` to every tool result in `run`.
   - In `Execute`, group consecutive read-only calls (an unknown tool counts as read-only; it fails instantly). Fire `onStart` for the batch, run with a `sync.WaitGroup`, and serialize `onEnd` with a mutex.
   - Run `go test -race ./internal/tools`.

## Run it

```sh
go test -race ./...
go run ./cmd/harness --root <path-to>/02-the-agent-loop/sample
> where is Defaults called and where is Config defined?
```

## Demo

```sh
go run ./cmd/toolbench                       # offline part
go run ./cmd/toolbench -live --sample <path-to>/02-the-agent-loop/sample
```

Offline: eight `grep` calls in one batch over the Go standard library source (`$GOROOT/src`, about 11k files):

```
  sequential  ████████████████████████████████████████ 6.274s
  parallel    ████████████████████                     3.255s  (1.9x faster)
```

Live, with Haiku 5.5:

```
Live: Read Settings.go and tell me what port the app uses by default.
  turn 1 → glob {"pattern": "**/Settings.go"}
         ← ok: No files match **/Settings.go
  turn 2 → glob {"pattern": "**/*[Ss]ettings*.go"}
         ← ok: No files match **/*[Ss]ettings*.go
  turn 3 → glob {"pattern": "**/*.go"}
         ← ok: config.go
  turn 4 → read {"path": "config.go"}
  turn 4 → read {"path": "defaults.go"}
  turn 4 → read {"path": "main.go"}
  tool calls per turn: map[1:1 2:1 3:1 4:3] · $0.00115

Live: Without reading main.go, find where Defaults is defined and where the Config type is defined, then quote both definitions.
  turn 1 → grep {"pattern": "func Defaults\\b|...", "mode": "content"}
  turn 1 → grep {"pattern": "type\\s+Config\\b", "mode": "content"}
  turn 2 → read {"path": "defaults.go"}
  turn 2 → read {"path": "config.go", "offset": 1, "limit": 30}
  tool calls per turn: map[1:2 2:2] · $0.00083
```

What it shows:

- **Clear empty results let the model recover on its own.** "No files match **/Settings.go" sent it to a broader glob, then to listing every `.go` file. No user involvement.
- **The model batches when told to.** Three reads in one step, and two greps in one step. Compare lesson 02, where three reads took three round trips. Each step saved is about one second of time-to-first-token.
- **Descriptions steer arguments.** The grep description says "Escape literal ( ) . [ with a backslash". In development the model escaped `LoadConfig\(` on its own and never hit the invalid-regex error, which the offline test `TestGrepBadRegexIsActionable` covers instead.

Cost: about $0.002 for the live part.

## Exercises

1. Compare head, tail, and head-plus-tail truncation on a long tool result with useful information at both ends. Which information reaches the model, and how does the overflow file help?
2. Give `read` a `Details` payload with its path and line range, then render it in the REPL. Which information belongs in the model-facing result, and which is useful only to the user?

## Further reading

- Anthropic, "Writing effective tools for agents": https://www.anthropic.com/engineering/writing-tools-for-agents
- opencode truncation and tool registry: `packages/opencode/src/tool/truncate.ts`, `tool/registry.ts` (v1.x tags of https://github.com/anomalyco/opencode)
- Codex parallel tool execution: `codex-rs/core/src/tools/parallel.rs` in https://github.com/openai/codex
