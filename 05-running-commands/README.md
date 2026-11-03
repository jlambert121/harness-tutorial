# Lesson 05: Running Commands

## What you'll build

A `bash` tool that survives hostile commands:

- **Timeouts that kill the whole process tree**, including children that ignore SIGTERM.
- **Bounded memory** however much a command prints.
- **Empty stdin**, so interactive programs can't hang the agent.
- **No hang when a background child keeps stdout open.**
- **A working directory that persists** between calls, and resets if it leaves the workspace.

Until lesson 09, the REPL asks for a y/n before every command. With this tool the agent can build and test the code it edits, which makes it a coding agent.

## How the real harnesses do it

| | pi | opencode | Codex | Claude Code |
|---|---|---|---|---|
| Process model | one command per call | one command per call | `exec_command` with optional PTY; long-running processes return a `session_id` you feed with `write_stdin` | fresh process per call; `cd` persists, env vars don't |
| Timeout | optional, seconds, no default | 2 min default; kills, then suggests retrying with a longer timeout | `yield_time_ms` (default 10 s): returns what it has so far, the process keeps running | 2 min default, 10 min max; a timed-out foreground command moves to the background instead of dying |
| Output | keeps the **last** 2000 lines / 50 KB; full output to a temp file | tail kept (30k chars of metadata) | 1 MiB head + tail buffer; `max_output_tokens` (10k default) | ~30k chars inline; beyond that a file path + 2,000-char preview |
| Background | none: "use tmux" | n/a | yes: PTY sessions, max 64 processes | `run_in_background: true` |

Two designs stand out:

- **pi** keeps the tool trivial and pushes long-running work onto `tmux`: "Bash is all you need."
- **Codex** turns the shell into a session API (`exec_command` / `write_stdin`), so the model can drive a REPL, a dev server, or an interactive installer.

Claude Code sits between them: background mode, plus automatically backgrounding a command that times out.

## Design for our harness

### What can go wrong

| Command | Failure without care | Fix |
|---|---|---|
| `npm test` (spawns workers), then the user hits Ctrl-C | Killing `bash` leaves the workers running | `Setpgid: true`; signal `-pgid` (the whole group) |
| `trap '' TERM; sleep 600` | SIGTERM ignored, so the timeout never ends it | SIGTERM, wait 2 s, then SIGKILL |
| `yes \| head -c 50000000` | 50 MB in memory and in the context | `HeadTail` buffer: first and last 512 KiB, the middle counted and dropped |
| `npm init`, `git commit` (opens an editor), `read x` | Waits for input forever | stdin is `/dev/null` (the `exec.Cmd` default), plus `PAGER=cat`, `GIT_PAGER=cat`, `GIT_TERMINAL_PROMPT=0` |
| `python -m http.server &` | The shell exits, but the child holds the stdout pipe, so `cmd.Wait()` blocks forever | `cmd.WaitDelay` (Go 1.20+) stops waiting for pipes; then SIGKILL the group |
| `cd /` | Later commands run outside the workspace | Read the final directory and reset it if it's outside the root |

### Tracking the working directory without parsing output

The command runs inside a wrapper:

```bash
{
<command>
}
__harness_ec=$?
pwd -P >&3          # fd 3 is a pipe only the harness reads
exit $__harness_ec
```

The harness reads fd 3 to learn the final directory. The command's own output is untouched. If the command calls `exit` itself, `pwd` never runs and the directory stays as it was. The demo shows that case.

### No background processes, on purpose

Any child still alive when the shell exits is killed with the group. That keeps the harness from leaking processes, but the agent can't start a dev server and then curl it in a later call. Exercise 2 adds Claude Code-style background jobs. The tool description tells the model about this limit. A model that knows a constraint works around it; a model that doesn't keeps retrying.

### Output: what the model sees

The tool returns combined stdout and stderr (interleaved, as a terminal shows them), plus `[exit code: N]`, or `[timed out after 2m0s; process group killed]`. A non-zero exit sets `IsError` so UIs can colour it, but it's still ordinary data. A failing test is information the model needs, not a harness error. The 1 MiB buffer is the memory bound. The registry's cap from lesson 03 (head + tail, 50 KB, overflow file) is what bounds the context.

### The interim confirmation guard

`Env.Confirm func(ctx, command) bool` runs before every command. `repl.Run` installs a y/n prompt. Headless runs in lesson 07 leave it `nil` and run only inside throwaway temp directories. Lesson 09 replaces this with rules, approvals and a sandbox.

## The contract

`harness/internal/tools`:

```go
type Env struct {
    Root, OverflowDir string
    Reads   *ReadTracker
    Cwd     string                                          // bash working dir; "" = Root
    Confirm func(ctx context.Context, command string) bool  // nil = allow
}

type HeadTail struct{ ... }                 // io.Writer, safe for concurrent writes
func NewHeadTail(limit int) *HeadTail
func (b *HeadTail) String() string          // head + "\n… N bytes omitted …\n" + tail
func (b *HeadTail) Dropped() int64

type BashDetails struct { PID, ExitCode int; Dropped int64; Duration time.Duration; Cwd string }
func Bash() Tool   // "bash": {command, timeout_ms?, workdir?}; Result.Details is BashDetails
```

`harness/internal/agent`: add `func (a *Agent) Tools() *tools.Registry`. The REPL uses it to install `Confirm`.

Provided in `support/`: `internal/ui/repl` (with the confirm prompt), `cmd/harness` (registers `bash`), `cmd/shelltorture`.

## Build it

1. **`HeadTail`.** Test: `go test ./internal/tools -run TestHeadTail`.
   - Fill `head` up to `limit/2`. After that, write into a ring buffer of `limit - limit/2` bytes, counting each overwritten byte as dropped.
   - Copy in chunks, not byte by byte: 50 MB through a per-byte loop is noticeably slow.
   - In `String()`, trim a partial rune at the end of the head and at the start of the tail.

2. **Run and wait.** Tests: `-run 'TestBashExit|TestBashSuccess|TestBashRunsIn|TestBashStdin'`.
   - `exec.Command("/bin/bash", "-c", script)` with `SysProcAttr{Setpgid: true}`, `Dir`, extra env vars, and `Stdout = Stderr = headTail`. The PID is also the process group ID.
   - Exit code: take it from `*exec.ExitError`. A signal death is `128 + signal`.

3. **Timeouts and cancellation.** Tests: `-run 'TestBashTimeout|TestBashKills|TestBashCancel'`.
   - Run `cmd.Wait()` in a goroutine and `select` on it, a timer, and `ctx.Done()`.
   - `killGroup`: `syscall.Kill(-pgid, SIGTERM)`, wait up to 2 s, then `SIGKILL`.

4. **Background children.** Test: `TestBashBackgroundChildDoesNotHang`.
   - Set `cmd.WaitDelay`. When it expires, `Wait` returns `exec.ErrWaitDelay`, but `cmd.ProcessState` still holds the real exit code. Use it.
   - After the shell exits, SIGKILL the group.

5. **Working directory.** Tests: `-run 'TestBashCwd|TestBashWorkdir'`.
   - Wrap the command as above. Pass the write end of an `os.Pipe` in `cmd.ExtraFiles`; it becomes fd 3. Close your copy of the write end after `Start`, or the reader never sees EOF.
   - Validate the new directory with `env.Resolve`. If it's outside the root, reset to the root and add a note to the result.

6. **Confirm.** Test: `TestConfirmDeniedReturnsError`. Ask after validating the arguments and before starting anything. A denial says "do not retry" so the model doesn't ask again in a loop.

7. Add `Agent.Tools()`, copy `support/`, and run the whole suite with `-race`.

## Run it

```sh
go test -race ./...
cp -R <path-to>/02-the-agent-loop/sample /tmp/sample && (cd /tmp/sample && go mod init sample)
go run ./cmd/harness --root /tmp/sample
> run the program, then make it print the port in hex as well, and run it again
```

## Demo

`go run ./cmd/shelltorture` (offline, about 5 s):

```
SCENARIO                       TIME   EXIT  KEPT    DROPPED  LEFTOVER PROCS  PEAK HEAP  LAST LINE
50 MB of output                30ms   0     1.0 MB  46.7 MB  0               4.1 MB     a log liEND-OF-OUTPUT [exit code: 0]
hangs forever (1s timeout)     1s     143   43 B    0 B      0               6.0 MB     [timed out after 1s; process group killed]
ignores SIGTERM (1s timeout)   3s     137   43 B    0 B      0               6.3 MB     [timed out after 1s; process group killed]
spawns 20 background sleepers  230ms  0     23 B    0 B      0               6.4 MB     spawned [exit code: 0]
interleaved stdout/stderr      10ms   0     45 B    0 B      0               6.5 MB     err3 [exit code: 0]
waits for stdin                10ms   0     22 B    0 B      0               6.6 MB     got:[] [exit code: 0]
cd then exit 7                 10ms   7     15 B    0 B      0               6.7 MB     [exit code: 7]
pwd after previous cd          10ms   0     90 B    0 B      0               6.8 MB     <root> [exit code: 0]
```

What it shows:

- **50 MB of output cost about 4 MB of heap and kept the end** (`END-OF-OUTPUT`). The model then sees at most 50 KB of it, through the registry cap from lesson 03.
- **Exit 143 = 128 + SIGTERM(15); exit 137 = 128 + SIGKILL(9).** The SIGTERM-ignoring command needed the 2 s grace period before SIGKILL. Zero leftover processes in every scenario (`pgrep -g <pgid>`).
- **Twenty background sleepers didn't hang the call**: 230 ms, which is the 200 ms `WaitDelay` plus overhead. They were killed with the group.
- **`cd sub && exit 7` lost the `cd`**, because `exit` skipped the wrapper's `pwd`. The next command ran in the root. (Claude Code's docs say only that `cd` persists while it stays inside the working directories.)

This ties back to lesson 02's cancellation contract. Ctrl-C now reaches the subprocess tree too, not only the model stream.

## Exercises

1. **Stream output to the UI.** Give `Tool.Run` a progress callback (pi's `onUpdate`; opencode's `ctx.metadata()`), and emit `tool_update` events with the latest output lines while a command runs.
2. **Background jobs.** Add `run_in_background: true`: start the command, return a job ID immediately, keep it in a table on `Env`, and add `bash_output {job_id}` and `bash_kill {job_id}` tools. Kill all jobs when the agent exits.
3. **Codex-style yield.** Instead of a hard timeout, return after `yield_time_ms` with partial output and a session ID, and let the model call `write_stdin`.

## Further reading

- Claude Code tools reference (Bash semantics): https://code.claude.com/docs/en/tools-reference
- Codex unified exec: `codex-rs/core/src/unified_exec/` and `core/src/tools/handlers/shell_spec.rs` in https://github.com/openai/codex
- Go `exec.Cmd.WaitDelay`: https://pkg.go.dev/os/exec#Cmd
- pi on tmux vs background bash: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
