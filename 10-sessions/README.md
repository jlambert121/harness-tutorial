# Lesson 10: Sessions

## What you'll build

Conversations persist as append-only JSONL **trees**, written with an fsync after every message. You'll be able to:

- **Kill the harness** with `kill -9` mid-tool-call, run `--continue`, and pick up where it stopped.
- **`/undo` a prompt.** Files are restored from snapshots kept in a hidden git repository that never touches your own `.git`.
- **Branch.** The conversation moves back to before the undone prompt, and the abandoned work stays in the file as a branch.

The demo does all of this for real and draws the resulting tree.

## How the real harnesses do it

| | Format | Structure | Resume | Undo |
|---|---|---|---|---|
| **pi** | JSONL at `~/.pi/agent/sessions/--<cwd>--/<ts>_<uuid>.jsonl`; header line, then entries with `id`/`parentId` | **tree in one file**; types `message`, `model_change`, `compaction`, `branch_summary`, `label`, `custom`… | `--continue`, `--resume`, `/tree` to navigate, `/fork` to a new file | via branches |
| **Codex** | JSONL "rollouts" at `~/.codex/sessions/YYYY/MM/DD/rollout-…jsonl` (optionally zstd); `{timestamp, type, payload}` | linear; `compacted` entries are checkpoints | `codex resume [--last\|id]`; replays from the newest compaction checkpoint | `codex fork` |
| **opencode** | SQLite (`session`, `message`, `part` tables with JSON columns) | linear, plus child sessions for subagents | by session ID; HTTP `fork`/`revert` routes | **git snapshots in a separate bare repo** (`--git-dir … --work-tree …`): `add --all` + `write-tree` at each step, restore with `read-tree` + `checkout-index`; revert is "soft" until the next prompt |
| **Claude Code** | per-session transcript (on-disk format not covered by the research) | linear, with compaction boundaries | `--continue`, `--resume`, `/resume` | `/rewind` |

Three useful design principles for our implementation:

1. **Append session entries.** pi and Codex retain earlier entries across compaction.
2. **Rebuild in-memory history from persisted state.**
3. **Restore files and conversation together.** Restoring only one leaves the model believing in files that no longer exist. The demo hit exactly this before the fix below.

## Design for our harness

### The file

```jsonl
{"id":"d1f196c2","type":"header","data":{"version":1,"cwd":"/work/textkit"},"time":"…"}
{"id":"8819e85e","parent_id":"d1f196c2","type":"snapshot","data":{"tree":"4b825dc6…"},"time":"…"}
{"id":"10fcb25a","parent_id":"8819e85e","type":"message","message":{"role":"user","blocks":[…]},"time":"…"}
{"id":"cc8b14b8","parent_id":"10fcb25a","type":"message","message":{"role":"assistant","blocks":[{"tool_call":…}]},"time":"…"}
```

- **The current leaf is the last entry written.** The conversation is the parent chain from that leaf up to the header.
- **`Branch(id)` appends a `branch` entry** whose parent is `id`. That makes the move durable: reopening the file continues from the branch point. The first version only moved an in-memory pointer, and `/undo` was silently forgotten on the next `--continue`.
- **Crash safety:** one `write` + `fsync` per entry. On open, a torn last line is dropped and truncated, so the next append produces a valid file.
- **Every history append is persisted**, through a `Persist` callback on the agent, rather than by the agent knowing about files. Tests compare persisted messages against `History()`.

### Resume

`--continue` opens the newest session for this workspace, `session.Repair`s it, and `Agent.Load`s it. **Repair** is the lesson 02 invariant applied after a crash: a tool call whose result never got written receives a synthetic result:

> Tool execution was interrupted (the harness stopped before it finished). Check the current state before retrying.

Without it, the provider rejects the history. With it, the model knows to look before redoing anything.

The read-before-edit tracker (lesson 04) is **not** restored. After a resume the model must re-read files before editing them, because they may have changed while the harness was down. In the demo you can see the model hit "you must read the file first" once, read, then edit.

### Snapshots and undo

`OnRunStart` (a new agent hook) runs before each prompt:

```
git --git-dir ~/.local/share/harness/snapshots/<hash(root)> --work-tree <root> add --all
git … write-tree      → tree hash, stored as a "snapshot" entry in the session
```

`/undo`:
1. Find the last `snapshot` on the current branch.
2. `Diff` (`add --all` + `diff --cached --name-only <tree>`) to report what will change.
3. `Restore`: delete files added since the snapshot (`--diff-filter=A`), then `read-tree <tree>` + `checkout-index --all --force`.
4. `Branch(snapshot.ParentID)` and reload history.

- **The shadow repository honors `.gitignore`**, so build output and `node_modules` are neither snapshotted nor deleted.
- **The user's repository is untouched:** no commits, no index changes, no stash. `TestSnapshotRestore` checks `HEAD` and the index.

Snapshots are per prompt, not per step as in opencode. That's coarser, but one entry per prompt is easy to reason about.

## The contract

`harness/internal/session`:

```go
type Entry struct {
    ID, ParentID string; Time time.Time
    Type string                // "header", "message", "snapshot", "branch", ...
    Message *llm.Message; Data json.RawMessage
}                              // JSON tags: id, parent_id, time, type, message, data
func DirFor(dataDir, cwd string) string        // dataDir/sessions/<cwd with / and spaces → ->
func Create(dir, cwd string) (*Store, error)   // file: <2006-01-02T15-04-05>_<id>.jsonl
func Open(path string) (*Store, error)
func (s *Store) Append(e Entry) string          // fills ID, ParentID (= leaf), Time; fsyncs; returns ID
func (s *Store) AppendMessage(m llm.Message) string
func (s *Store) Branch(id string)
func (s *Store) Messages(leaf string) []llm.Message
func (s *Store) PathTo(leaf string) []Entry
func (s *Store) Last(typ string) (Entry, bool)  // on the current branch
func (s *Store) Leaf() string; Leaves() []string; Entries() []Entry
func (s *Store) Path() string; ID() string; Cwd() string; Err() error; Close() error
func Repair(msgs []llm.Message) []llm.Message
type Info struct { Path, ID string; Modified time.Time; FirstPrompt string; Entries int }
func List(dir string) ([]Info, error)           // newest first
func Latest(dir string) (string, error)

func NewSnapshots(dataDir, root string) (*Snapshots, error)
func (s *Snapshots) Track(ctx) (tree string, err error)
func (s *Snapshots) Diff(ctx, tree string) ([]string, error)
func (s *Snapshots) Restore(ctx, tree string) error
```

`harness/internal/agent`: `Config.Persist func(llm.Message)`, `Config.OnRunStart func(ctx, input string)`, `func (a *Agent) Load(history []llm.Message)`. Route every `a.history = append(...)` through one `appendHistory` method.

`harness/internal/permission`: dynamic commands no longer ask in yolo mode (this is backported into lesson 09). Headless runs were denying `test -z "$(gofmt -l .)"`.

Provided in `support/`:
- **`cmd/harness`**: adds `--continue`, `--resume <path|id>`, `--no-session` and `/undo` in the REPL. Data lives in `$HARNESS_DATA_DIR`, default `~/.local/share/harness`.
- **`internal/ui/repl`**: adds an `Options` struct and slash-command hooks.
- **`cmd/sessions`** (`list`, `tree`).
- **`cmd/evalrun`**: now passes `--no-session`.
- **`demo.sh`**, in the lesson directory.

## Build it

1. **Store.** Tests: `go test ./internal/session -run 'TestAppend|TestTorn|TestBranch|TestEntries|TestLatest|TestDirFor'`.
   - Keep `[]Entry` plus `map[id]index`.
   - For `Open`, scan line by line and stop at the first line that doesn't parse. Then `os.Truncate` to the last good byte and reopen with `O_APPEND`.
2. **Repair.** Test: `-run TestRepair`. After each assistant message with tool calls, consume the tool messages that follow, then add a synthetic result for every call without one. (A recursive first draft of this overflowed the stack. Keep it a single forward loop.)
3. **Snapshots.** Tests: `-run TestSnapshot`.
   - `git init --bare <dir>` takes no `--work-tree`. Every other command uses both `--git-dir` and `--work-tree`.
   - In tests, isolate git from your global config (`GIT_CONFIG_GLOBAL=/dev/null`). A developer with `commit.gpgsign=true` otherwise has the test hang waiting for a passphrase. That happened here.
4. **Agent hooks.** Tests: `go test ./internal/agent -run 'TestPersist|TestLoad'`.
5. **Permission tweak.** Test: `go test ./internal/permission -run TestYoloAllowsDynamic`.

## Run it

```sh
HARNESS_DATA_DIR=/tmp/hd go test ./...
go run ./cmd/harness --root /tmp/textkit --ui repl           # then Ctrl-D, and:
go run ./cmd/harness --root /tmp/textkit --ui repl --continue
> /undo
go run ./cmd/sessions --root /tmp/textkit tree
```

## Demo

`bash <lesson>/demo.sh <path-to>/06-system-prompt/sample-repo`, from your module root. It uses a throwaway data directory and workspace; about $0.02 with Haiku.

```
== 1. start a task and kill -9 the harness while its first tool call runs
== 2. resume the same session
…I added `Reverse` and `IsPalindrome` to `textkit.go`, with a… go test ./... passes
synthetic results added by Repair: 1
== 3. undo the last prompt (files and conversation)
> undid the last prompt; restored 2 file(s): textkit.go, textkit_test.go
== 4. take a different branch from there
== 5. the session tree
d1f196c2 [header]
└─ ● 8819e85e [snapshot]
   ● 10fcb25a user  Implement Reverse so the tests pass, then add an IsPalindrom…
   ● cc8b14b8 model  → bash
   ● 46f97611 tool  bash: Tool execution was interrupted (the harness stoppe…
   ├─   ac153984 [snapshot]
   │    4b5fd9e5 user  Continue where you left off.
   │    97be41c4 model  → bash, glob
   │    …
   │    1a82c510 model I added `Reverse` and `IsPalindrome` to `textkit.go`, with a…
   └─ ● 7586cfa4 [branch]
      ● d96edfc9 [snapshot]
      ● bbcc9577 user  Instead of IsPalindrome, add CountVowels with tests.
      ● 14e605f6 model Got it: CountVowels replaces I… → bash
      ● f52bd5a3 tool  edit: you must read the file first: use read on …
      …
      ● 905dfb64 model Reverse is implemented and CountVowels is added with table-d…
```

What it shows:

- **The `kill -9` landed mid-tool-call.** The session file held the call but no result. Repair added the "interrupted" result (`46f97611`), and the model re-checked the workspace (`bash`, `glob`) before continuing.
- **`/undo` restored two files and moved the conversation.** The abandoned turn is still in the file (`├─` branch); the new prompt grows from the `[branch]` entry. No message was deleted.
- **The first version of this demo found a real bug.** `/undo` restored the files, but the branch move lived only in memory. The next `--continue` reopened the old leaf, and the model's next reply began "The files on disk no longer co…" (cut off in the tree view): it had noticed the mismatch. That's the case for making every state change an appended entry.
- **The model hit `you must read the file first` after resume**, then read and edited. That's the intended behavior.

Ties to earlier lessons:
- Repair is lesson 02's "every call gets a result" invariant.
- Snapshots make lesson 09's yolo mode less frightening, because any prompt can be undone.
- Sessions in the same event-and-entry style are what lesson 13's compaction will append `compaction` entries to.

## Exercises

1. **`/tree` navigation.** List leaves with their last message and let the user `Branch` to any entry. pi also offers summarizing the branch you leave (`branch_summary`); try having a small model write one.
2. **Per-step snapshots.** Snapshot at every `TurnStart` and add `/undo N`. How large does the shadow repo get on a long session? (`git count-objects -vH`)
3. **Fork.** `/fork <entry>` copies the path to that entry into a new session file, as pi does.

## Further reading

- pi session format: `packages/coding-agent/docs/session-format.md` in https://github.com/earendil-works/pi
- Codex rollouts and resume: `codex-rs/core/src/session/rollout_reconstruction.rs` in https://github.com/openai/codex
- opencode snapshots: `packages/opencode/src/snapshot/index.ts` (v1.x tags) in https://github.com/anomalyco/opencode
- Claude Code context window and `/rewind`: https://code.claude.com/docs/en/context-window
