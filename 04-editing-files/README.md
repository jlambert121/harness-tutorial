# Lesson 04: Editing Files

## What you'll build

`edit` and `write` tools. Edits match `old_string` with a cascade of increasingly lenient strategies, refuse ambiguous matches, require the model to have read the file (and not to be working from a stale copy), and preserve the file's line endings and byte-order mark (BOM). You'll also replace the lexical workspace check with one that follows symlinks. The demo is a 39-case benchmark showing what each matching strategy buys you.

## How the real harnesses do it

How edits are represented is one of the most consequential choices in a harness. Models make small transcription errors when they copy code back: trailing spaces, tabs turned into spaces, curly quotes. The edit format decides whether those errors turn into failed edits, wrong edits, or correct ones.

| Harness | Format | Matching |
|---|---|---|
| **Claude Code** | `Edit(old_string, new_string, replace_all)` | **Exact only.** "Doesn't use regex or fuzzy matching." `old_string` must be unique. Read-before-edit is required, at least for older models. |
| **pi** | `edit(path, edits: [{oldText, newText}])` | Exact first. Then fuzzy: NFKC normalization, trailing whitespace stripped, Unicode quotes and dashes mapped to ASCII. Every edit is matched against the *original* file and must be unique and non-overlapping. |
| **opencode v1** | `edit(oldString, newString)` | A **9-strategy cascade**: exact → line-trimmed → block-anchor (first and last lines anchor, middle lines scored by Levenshtein ≥ 0.65) → whitespace-normalized → indentation-flexible → escape-normalized → trimmed-boundary → context-aware → multi-occurrence. A guard rejects "disproportionate" matches. **v2 cut this back** to exact plus light normalization. GPT models get `apply_patch` instead. |
| **Codex** | `apply_patch`, a custom diff format (below) | Per hunk: exact → ignore trailing whitespace → ignore leading and trailing whitespace → normalize Unicode punctuation. |
| **Aider** | Text blocks in the reply: whole file, `SEARCH/REPLACE`, unified diff | With flexible patching disabled, editing errors went up 9×. In its 2023 benchmark, JSON function-call formats did worse than text formats for every model tested. |

Codex's patch format, which models from OpenAI are trained on:

```
*** Begin Patch
*** Update File: server/handler.go
@@ func (h *Handler) ServeHTTP
-	if r.Method != http.MethodGet {
+	if r.Method != http.MethodGet && r.Method != http.MethodHead {
*** End Patch
```

It has no line numbers ("the context is enough to uniquely identify code"), 3 lines of context, and `@@` anchors that name the enclosing function.

Two lessons from this history:

1. **Leniency has a cost.** opencode's cascade fixed many failed edits, but a fuzzy matcher that finds a *wrong* unique match corrupts a file silently. Its v2 rewrite back to near-exact matching suggests the team found the cost too high (an inference; the research found no stated reason).
2. **A unique match is the safety property.** An edit that matches twice is refused, whatever strategy found it. Never resolve ambiguity by guessing.

## Design for our harness

### Strategy cascade, stopping at the first strategy with any match

```
exact ──0──▶ trailing_ws ──0──▶ trimmed ──0──▶ unicode ──0──▶ "Could not find…"
  │1            │1                 │1              │1
  ▼             ▼                  ▼               ▼
apply         apply              apply           apply        ≥2 at any level → "Found N matches…"
```

The fuzzy strategies compare **line windows of the same length**. A match always spans whole lines, and a window can never swallow extra lines. That's why we don't need opencode's disproportionate-match guard. The edit result names the strategy that matched (`Edited config.go (strategy trimmed)`), so you can see in traces how often the fallback was needed.

### Read before edit, and not from a stale copy

`ReadTracker` records each file's modification time when `read` returns it. `edit`, and `write` over an existing file, then require that:

- the file was read, and
- its mtime hasn't changed since.

The second check catches the user (or a formatter, or another process) changing the file after the model read it. Without it, the model's `old_string` comes from an outdated view. After a successful edit, the new mtime is recorded, so consecutive edits don't need a re-read. `ReadTracker.Lock(path)` serializes edits to the same file.

### Line endings and BOM

The model sees and writes `\n`. `decodeText` strips the BOM, converts CRLF to LF, and remembers both; `encode` restores them. Without this, a model editing a Windows file either fails every match (its `old_string` has no `\r`) or converts the whole file to LF.

### Results include a diff

The edit result includes a unified diff (`internal/diff`, provided). This gives the model a precise record of what it did. In the live run below, the model's first edit put the wrong JSON tag on a field. The diff in the result showed it, and the next call fixed it.

### Interim workspace guard (until lesson 09)

`Env.Resolve` resolves symlinks before checking that a path is inside the root. That includes paths that don't exist yet and dangling symlinks. Without it, `link → /etc` inside the workspace makes `write link/passwd` an escape. Every file tool now calls `env.Resolve`.

## The contract

`harness/internal/tools` (additions):

```go
type Env struct { Root, OverflowDir string; Reads *ReadTracker } // nil Reads disables read checks
func (e *Env) Resolve(path string) (string, error)   // error text contains "outside the workspace"

type ReadTracker struct{ ... }
func NewReadTracker() *ReadTracker
func (t *ReadTracker) MarkRead(path string, mtime time.Time)
func (t *ReadTracker) Check(path string) error         // "read the file first" / "modified since"
func (t *ReadTracker) Lock(path string) (unlock func())
// All three methods must work on a nil *ReadTracker (no-op / nil error).

type Strategy string
const (StrategyExact = "exact"; StrategyTrailingWS = "trailing_ws"; StrategyTrimmed = "trimmed"; StrategyUnicode = "unicode")
var AllStrategies = []Strategy{StrategyExact, StrategyTrailingWS, StrategyTrimmed, StrategyUnicode}
var ErrNoMatch = errors.New("no match")
type AmbiguousError struct { Strategy Strategy; Count int }   // implements error
func FindMatch(content, old string, strategies []Strategy) (start, end int, used Strategy, err error)

func Edit() Tool    // "edit":  {path, old_string, new_string, replace_all?}
func Write() Tool   // "write": {path, content}
```

`read` must call `env.Reads.MarkRead(abs, info.ModTime())`. `read`, `glob`, `grep` and `ls` switch from `insideRoot` to `env.Resolve`.

Provided in `support/`: `internal/diff` (`Unified(a, b, name string, context int) string`), `cmd/editbench`, and `cmd/harness`, which now registers `edit` and `write` with a `ReadTracker`.

## Build it

Continue from lesson 03 and copy this lesson’s `tests/` and `support/`. Work in your existing `internal/tools/` package; the diff package is supplied.

1. **`Resolve`.** Tests: `go test ./internal/tools -run 'TestWorkspace|TestResolve'`.
   - `filepath.EvalSymlinks` fails on paths that don't exist, so write `realpath`: if the path exists, evaluate it. If it's a dangling symlink, follow its target. Otherwise resolve the parent and append the base name.
   - Compare against the **resolved** root as well. On macOS, `t.TempDir()` is under `/var`, which is a symlink to `/private/var`.

2. **`FindMatch`.** Tests: `-run 'TestFindMatch|TestFuzzy|TestNearMiss'`.
   - Exact: count all occurrences with `strings.Index` in a loop.
   - Line strategies: split `old` into lines, normalize each, slide a window over the content lines. Compute byte offsets from the line starts. Include the final `\n` in the span only if `old` ends with one.
   - Return `*AmbiguousError` at the first strategy with more than one match. Don't fall through to the next strategy.

3. **`ReadTracker`.** Record what the model has read so an edit can reject a stale view. Use a map from path to mtime and a map from path to mutex, both behind one mutex. Make every method nil-safe.

4. **`edit`.** Tests: `-run 'TestEdit|TestConsecutive|TestPreserves'`.
   - Order: validate args → `Resolve` → `Lock` → `Check` → read → `decodeText` → match → replace → `encode` → write, keeping the file mode → `MarkRead` → diff.
   - Error text matters. The tests check for `Found 2 matches`, `more surrounding context`, `replace_all`, `Could not find` and `Read the file again`.
   - An empty `old_string` should point the model to `write`.

5. **`write`.** Test: `TestWriteCreatesAndRequiresReadToOverwrite`. Create parent directories. If the file exists, run `Check`.

6. **Wire it in.** Add `Reads` to `Env`, call `MarkRead` in `read`, and replace `insideRoot` with `env.Resolve` everywhere.

## Run it

```sh
go test -race ./...
cp -R <path-to>/02-the-agent-loop/sample /tmp/sample && go run ./cmd/harness --root /tmp/sample
> Add a Timeout field (time.Duration, json "timeout") to Config, default it to 30 seconds in Defaults, and include it in the line main prints.
```

Use a copy: the agent edits files for real, and nothing asks permission yet (lesson 09).

## Demo

`go run ./cmd/editbench` (offline):

```
39 edit cases

CATEGORY                    N   exact only (Claude Code)  + trailing_ws  + trimmed  + unicode (Codex order)
exact                       8   8/8                       8/8            8/8        8/8
trailing whitespace         6   0/6                       6/6            6/6        6/6
indentation (tabs→spaces)   6   1/6                       1/6            6/6        6/6
unicode punctuation         5   1/5                       1/5            1/5        5/5
CRLF file, LF old_string    4   4/4                       4/4            4/4        4/4
ambiguous                   3   3/3                       3/3            3/3        3/3
ambiguous under fuzzy       1   1/1                       1/1            1/1        1/1
near miss (must not match)  6   6/6                       6/6            6/6        6/6
TOTAL                       39  24/39                     30/39          35/39      39/39
```

What it shows:

- **Each strategy fixes one class of transcription error.** Exact-only fails every trailing-whitespace case and 5 of 6 tabs cases; the one it passes has a block with no tab-indented lines.
- **No strategy produced a wrong edit.** Uniqueness, plus same-length line windows, kept every near miss refused. The "ambiguous under fuzzy" case (unique under no strategy) was refused, not guessed.
- **CRLF passes everywhere** because normalization happens before matching, not inside it.
- **What the benchmark doesn't measure:** when `trimmed` matches, `new_string` is inserted as the model wrote it. If the model used spaces in a tab-indented file, the result is mis-indented. Go's `gofmt` fixes that; Python can't. Aider re-indents replacements relative to the match. That's exercise 1.

Live (Haiku 5.5, on a copy of the sample):

```
→ grep {"pattern": "Config|Defaults|Println|Printf", "mode": "files", "include": "*.go"}
→ read config.go · read defaults.go · read main.go          (one batch)
→ edit config.go  ← Edited config.go (strategy exact)
→ edit config.go  ← Edited config.go (strategy exact)      fixes a wrong tag visible in the previous diff
→ edit defaults.go ← Edited defaults.go (strategy exact)
→ edit main.go    ← Edited main.go (strategy exact)
```

The result passes `go vet`. Cost: about $0.002.

## Exercises

1. **Re-indent on fuzzy match.** When the `trimmed` strategy matches, adjust `new_string` to the indentation of the matched lines. Add a case to `editbench`: does a successful match also produce a correct edit?
2. **Multi-edit.** Accept several edits in one call, match them against the original content, and reject overlaps. What changes when the model needs related edits to succeed or fail together?

## Further reading

- Aider, unified diffs and flexible patching: https://aider.chat/docs/unified-diffs.html
- Aider, edit formats: https://aider.chat/docs/more/edit-formats.html
- Codex `apply_patch` grammar: `codex-rs/apply-patch/src/parser.rs` in https://github.com/openai/codex
- GPT-4.1 prompting guide (the patch format): https://developers.openai.com/cookbook/examples/gpt4-1_prompting_guide
- Claude Code tools reference (Edit semantics): https://code.claude.com/docs/en/tools-reference
