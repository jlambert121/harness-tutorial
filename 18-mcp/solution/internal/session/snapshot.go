package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Snapshots records workspace states in a separate bare git repository
// (opencode's design), so undo never touches the user's own .git: no
// commits, no index changes, no stash.
type Snapshots struct {
	gitDir, root string
}

func NewSnapshots(dataDir, root string) (*Snapshots, error) {
	sum := sha256.Sum256([]byte(root))
	s := &Snapshots{gitDir: filepath.Join(dataDir, "snapshots", hex.EncodeToString(sum[:8])), root: root}
	if _, err := os.Stat(filepath.Join(s.gitDir, "HEAD")); err != nil {
		if err := os.MkdirAll(s.gitDir, 0o755); err != nil {
			return nil, err
		}
		if out, err := exec.Command("git", "init", "-q", "--bare", s.gitDir).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git init: %v: %s", err, out)
		}
	}
	return s, nil
}

func (s *Snapshots) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--git-dir", s.gitDir, "--work-tree", s.root}, args...)...)
	cmd.Dir = s.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Track stages every non-ignored file and returns the tree hash.
func (s *Snapshots) Track(ctx context.Context) (string, error) {
	if _, err := s.git(ctx, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	return s.git(ctx, "write-tree")
}

// Diff lists files that differ between tree and the current workspace.
func (s *Snapshots) Diff(ctx context.Context, tree string) ([]string, error) {
	if _, err := s.git(ctx, "add", "--all", "--", "."); err != nil {
		return nil, err
	}
	out, err := s.git(ctx, "diff", "--cached", "--name-only", tree)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Restore makes the workspace match tree: changed and deleted files come
// back, files created since are removed. Ignored files are left alone.
func (s *Snapshots) Restore(ctx context.Context, tree string) error {
	if _, err := s.git(ctx, "add", "--all", "--", "."); err != nil {
		return err
	}
	added, err := s.git(ctx, "diff", "--cached", "--name-only", "--diff-filter=A", tree)
	if err != nil {
		return err
	}
	for _, f := range strings.Split(added, "\n") {
		if f != "" {
			os.Remove(filepath.Join(s.root, f))
		}
	}
	if _, err := s.git(ctx, "read-tree", tree); err != nil {
		return err
	}
	_, err = s.git(ctx, "checkout-index", "--all", "--force")
	return err
}
