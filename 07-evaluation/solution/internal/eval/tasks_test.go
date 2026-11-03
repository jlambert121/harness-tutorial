package eval_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"harness/internal/eval"
)

// TestReferenceSolutionsPass checks every task in testdata/evals is well formed:
// the starting repo fails its hidden check, and repo + solution/ passes it.
func TestReferenceSolutionsPass(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for every task")
	}
	tasks, err := eval.LoadSuite("../../testdata/evals")
	if err != nil || len(tasks) == 0 {
		t.Fatalf("tasks=%d err=%v", len(tasks), err)
	}
	for _, task := range tasks {
		t.Run(task.Name, func(t *testing.T) {
			t.Parallel()
			if check(t, task, false) {
				t.Error("starting repo already passes the check; the task is trivial")
			}
			if !check(t, task, true) {
				t.Error("reference solution fails the check")
			}
		})
	}
}

func check(t *testing.T, task eval.Task, withSolution bool) bool {
	dir := t.TempDir()
	dirs := []string{"repo"}
	if withSolution {
		dirs = append(dirs, "solution")
	}
	for _, d := range append(dirs, "check") {
		copyOver(t, filepath.Join(task.Dir, d), dir)
	}
	cmd := exec.Command("/bin/sh", "./check.sh")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if withSolution && err != nil {
		t.Logf("%s", out)
	}
	return err == nil
}

// copyOver copies src onto dst, replacing existing files.
func copyOver(t *testing.T, src, dst string) {
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
