//go:build unix

package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	bashDefaultTimeout = 2 * time.Minute
	bashMaxTimeout     = 10 * time.Minute
	bashBufferBytes    = 1 << 20 // Codex keeps a 1 MiB head/tail buffer
	killGrace          = 2 * time.Second
)

type bashArgs struct {
	Command   string `json:"command" desc:"Shell command, run with /bin/bash -c"`
	TimeoutMS int    `json:"timeout_ms,omitempty" desc:"Timeout in milliseconds (default 120000, max 600000)"`
	Workdir   string `json:"workdir,omitempty" desc:"Directory to run in, relative to the workspace root (default: the current directory)"`
}

const bashDescription = `Run a shell command and return its combined stdout and stderr plus the exit code.
The working directory persists between calls (cd works); environment variables do not.
Stdin is empty: never run interactive commands. Commands that do not finish within the timeout are killed together with their child processes; background processes (cmd &) are killed when the command returns.
Prefer read, grep, glob, and ls for inspecting files.`

type BashDetails struct {
	PID      int
	ExitCode int
	Dropped  int64
	Duration time.Duration
	Cwd      string
}

func Bash() Tool {
	return New("bash", bashDescription, false, func(ctx context.Context, env *Env, a bashArgs) Result {
		if strings.TrimSpace(a.Command) == "" {
			return errorf("command is empty")
		}
		dir := env.Cwd
		if dir == "" {
			dir = env.Root
		}
		if a.Workdir != "" {
			d, err := env.Resolve(a.Workdir)
			if err != nil {
				return errorf("%v", err)
			}
			dir = d
		}
		if env.Confirm != nil && !env.Confirm(ctx, a.Command) {
			return errorf("The user denied this command. Do not retry it; ask the user or take a different approach.")
		}
		timeout := bashDefaultTimeout
		if a.TimeoutMS > 0 {
			timeout = min(time.Duration(a.TimeoutMS)*time.Millisecond, bashMaxTimeout)
		}
		return runShell(ctx, env, dir, a.Command, timeout)
	})
}

func runShell(ctx context.Context, env *Env, dir, command string, timeout time.Duration) Result {
	// The trailing pwd goes to fd 3 so the final directory can be tracked
	// without mixing it into the command's output.
	script := "{\n" + command + "\n}\n__harness_ec=$?\npwd -P >&3\nexit $__harness_ec\n"
	cmd := exec.Command("/bin/bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PAGER=cat", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "TERM=dumb")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := NewHeadTail(bashBufferBytes)
	cmd.Stdout, cmd.Stderr = out, out
	cwdR, cwdW, err := os.Pipe()
	if err != nil {
		return errorf("%v", err)
	}
	cmd.ExtraFiles = []*os.File{cwdW}
	// Background children can hold stdout open after the shell exits; WaitDelay
	// stops Wait from blocking on them.
	cmd.WaitDelay = 200 * time.Millisecond

	start := time.Now()
	if err := cmd.Start(); err != nil {
		cwdW.Close()
		cwdR.Close()
		return errorf("cannot start shell: %v", err)
	}
	cwdW.Close()
	pgid := cmd.Process.Pid
	cwdCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(cwdR)
		last := ""
		for sc.Scan() {
			last = sc.Text()
		}
		cwdR.Close()
		cwdCh <- last
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var why string
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(timeout):
		why = fmt.Sprintf("timed out after %s; process group killed", timeout)
		waitErr = killGroup(pgid, done)
	case <-ctx.Done():
		why = "cancelled by the user; process group killed"
		waitErr = killGroup(pgid, done)
	}
	// Anything still in the group (background jobs) dies with the command.
	syscall.Kill(-pgid, syscall.SIGKILL)

	code := exitCode(waitErr)
	if errors.Is(waitErr, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	details := BashDetails{PID: pgid, ExitCode: code, Dropped: out.Dropped(), Duration: time.Since(start)}
	var notes []string
	select {
	case cwd := <-cwdCh:
		if cwd != "" && why == "" {
			if real, err := env.Resolve(cwd); err == nil {
				env.Cwd = real
			} else {
				env.Cwd = env.Root
				notes = append(notes, "cd left the workspace; working directory reset to "+env.Root)
			}
		}
	case <-time.After(time.Second):
	}
	details.Cwd = env.Cwd

	text := out.String()
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if why != "" {
		text += "[" + why + "]\n"
	} else {
		text += fmt.Sprintf("[exit code: %d]\n", details.ExitCode)
	}
	for _, n := range notes {
		text += "[" + n + "]\n"
	}
	return Result{Content: text, Details: details, IsError: why != "" || details.ExitCode != 0}
}

// killGroup sends SIGTERM to the whole process group, then SIGKILL if it has
// not exited after the grace period.
func killGroup(pgid int, done <-chan error) error {
	syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case err := <-done:
		return err
	case <-time.After(killGrace):
		syscall.Kill(-pgid, syscall.SIGKILL)
		return <-done
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}
