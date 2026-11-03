// Command shelltorture runs hostile shell commands through the bash tool and
// reports duration, output kept and dropped, leftover processes, and memory.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"harness/internal/tools"
)

type scenario struct {
	name    string
	command string
	timeout int
}

var scenarios = []scenario{
	{"50 MB of output", "yes 'a log line that repeats' | head -c 50000000; echo END-OF-OUTPUT", 0},
	{"hangs forever (1s timeout)", "sleep 600", 1000},
	{"ignores SIGTERM (1s timeout)", "trap '' TERM; (trap '' TERM; sleep 600) & sleep 600", 1000},
	{"spawns 20 background sleepers", "for i in $(seq 20); do sleep 600 & done; echo spawned", 0},
	{"interleaved stdout/stderr", "for i in 1 2 3; do echo out$i; echo err$i >&2; done", 0},
	{"waits for stdin", "read line; echo got:[$line]", 0},
	{"cd then exit 7", "cd sub && exit 7", 0},
	{"pwd after previous cd", "pwd -P", 0},
}

func main() {
	root, _ := os.MkdirTemp("", "torture")
	defer os.RemoveAll(root)
	os.Mkdir(filepath.Join(root, "sub"), 0o755)
	env := &tools.Env{Root: root}
	bash := tools.Bash()
	realRoot, _ := filepath.EvalSymlinks(root)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SCENARIO\tTIME\tEXIT\tKEPT\tDROPPED\tLEFTOVER PROCS\tPEAK HEAP\tLAST LINE")
	for _, s := range scenarios {
		var peak atomic.Uint64
		stop := make(chan struct{})
		go sampleHeap(&peak, stop)
		args, _ := json.Marshal(map[string]any{"command": s.command, "timeout_ms": s.timeout})
		start := time.Now()
		res := bash.Run(context.Background(), env, args)
		el := time.Since(start)
		close(stop)
		d := res.Details.(tools.BashDetails)
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%d\t%s\t%s\n", s.name, el.Round(10*time.Millisecond), d.ExitCode,
			size(int64(len(res.Content))), size(d.Dropped), leftovers(d.PID), size(int64(peak.Load())), lastLine(strings.ReplaceAll(res.Content, realRoot, "<root>")))
	}
	tw.Flush()
}

func sampleHeap(peak *atomic.Uint64, stop <-chan struct{}) {
	var m runtime.MemStats
	for {
		runtime.ReadMemStats(&m)
		if m.HeapAlloc > peak.Load() {
			peak.Store(m.HeapAlloc)
		}
		select {
		case <-stop:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// leftovers counts processes still in the command's process group.
func leftovers(pgid int) int {
	out, _ := exec.Command("pgrep", "-g", fmt.Sprint(pgid)).Output()
	return len(strings.Fields(string(out)))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := lines[len(lines)-1]
	if len(lines) > 1 && strings.HasPrefix(l, "[exit code") {
		l = lines[len(lines)-2] + " " + l
	}
	if len(l) > 60 {
		l = l[:60] + "…"
	}
	return l
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
