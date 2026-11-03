// Command memorydemo checks whether a convention stated in one session is
// followed in a later, fresh session: with memory on and with memory off.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const teach = "From now on in this repo, every test you write must be table-driven with a name field, and run each case with t.Run(tt.name, ...). Remember this for future sessions. For now just acknowledge; don't change any files."

const task = "Add a test for WordCount with three more cases. Run the tests when you're done."

func main() {
	repo := flag.String("repo", "", "sample repo (e.g. <path>/06-system-prompt/sample-repo)")
	model := flag.String("model", "anthropic/claude-haiku-5.5", "model")
	n := flag.Int("n", 3, "repeats per configuration")
	flag.Parse()
	bin := filepath.Join(os.TempDir(), "memorydemo-harness")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/harness").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		os.Exit(1)
	}
	for _, withMemory := range []bool{true, false} {
		followed := 0
		for i := 0; i < *n; i++ {
			data, _ := os.MkdirTemp("", "memdata")
			work, _ := os.MkdirTemp("", "memwork")
			exec.Command("cp", "-R", *repo+"/.", work).Run()
			exec.Command("git", "-C", work, "init", "-q").Run()
			flags := []string{"exec", "--no-session", "--root", work, "--model", *model}
			if !withMemory {
				flags = append(flags, "--no-memory")
			}
			env := append(os.Environ(), "HARNESS_DATA_DIR="+data)
			run := func(prompt string) {
				cmd := exec.Command(bin, append(flags, prompt)...)
				cmd.Env = env
				cmd.Run()
			}
			run(teach)
			memFiles, _ := filepath.Glob(filepath.Join(data, "memory", "*", "*.md"))
			run(task)
			test, _ := os.ReadFile(filepath.Join(work, "textkit_test.go"))
			ok := strings.Contains(string(test), "t.Run(tt.name")
			if ok {
				followed++
			}
			label := "memory off"
			if withMemory {
				label = "memory on "
			}
			fmt.Printf("%s run %d: memory files after session 1: %d · second session used t.Run(tt.name: %v\n", label, i+1, len(memFiles), ok)
			if withMemory && i == 0 {
				for _, f := range memFiles {
					b, _ := os.ReadFile(f)
					fmt.Printf("    %s:\n      %s\n", filepath.Base(f), strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "\n      "))
				}
			}
			os.RemoveAll(data)
			os.RemoveAll(work)
		}
		fmt.Printf("  → followed in %d/%d fresh sessions\n\n", followed, *n)
	}
}
