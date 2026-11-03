package session_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/session"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Ignore the developer's git config (commit signing would wait for a passphrase).
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSnapshotRestore(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	write(t, root, "a.txt", "original a\n")
	write(t, root, "dir/b.txt", "original b\n")
	git(t, root, "init", "-q")
	git(t, root, "-c", "user.email=x@y", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "user commit")
	headBefore := git(t, root, "rev-parse", "HEAD")

	snaps, err := session.NewSnapshots(data, root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tree, err := snaps.Track(ctx)
	if err != nil || len(tree) != 40 {
		t.Fatalf("tree=%q err=%v", tree, err)
	}

	write(t, root, "a.txt", "agent changed a\n")
	os.Remove(filepath.Join(root, "dir", "b.txt"))
	write(t, root, "new/c.txt", "agent created c\n")

	changed, err := snaps.Diff(ctx, tree)
	if err != nil || strings.Join(changed, ",") != "a.txt,dir/b.txt,new/c.txt" {
		t.Errorf("Diff = %v %v", changed, err)
	}
	if err := snaps.Restore(ctx, tree); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "original a\n" {
		t.Errorf("a.txt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "dir", "b.txt")); string(b) != "original b\n" {
		t.Errorf("deleted file not restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "new", "c.txt")); err == nil {
		t.Error("file created after the snapshot must be removed")
	}
	if git(t, root, "rev-parse", "HEAD") != headBefore || strings.Contains(git(t, root, "status", "--porcelain"), "c.txt") {
		t.Error("the user's own repository must not be touched")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "index")); err == nil {
		if out := git(t, root, "diff", "--cached", "--name-only"); out != "" {
			t.Errorf("the user's index changed: %q", out)
		}
	}
}

func TestSnapshotLeavesIgnoredFilesAlone(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	write(t, root, ".gitignore", "build/\n")
	write(t, root, "src.go", "package x\n")
	snaps, _ := session.NewSnapshots(data, root)
	tree, _ := snaps.Track(context.Background())
	write(t, root, "build/out.bin", "artifact")
	snaps.Restore(context.Background(), tree)
	if _, err := os.Stat(filepath.Join(root, "build", "out.bin")); err != nil {
		t.Error("ignored files are not part of snapshots and must survive a restore")
	}
}
