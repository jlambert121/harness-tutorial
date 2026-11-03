package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve maps a model-supplied path to an absolute path inside the workspace
// (or the overflow directory). Symlinks are followed before the check, so a
// link inside the root that points outside it is rejected, including links
// whose target does not exist yet.
func (e *Env) Resolve(path string) (string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.Root, path)
	}
	abs = filepath.Clean(abs)
	real, err := realpath(abs, 0)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %v", path, err)
	}
	for _, dir := range []string{e.Root, e.overflowDir()} {
		rd, err := realpath(filepath.Clean(dir), 0)
		if err == nil && within(rd, real) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%s is outside the workspace (%s)", path, e.Root)
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realpath is filepath.EvalSymlinks extended to paths that do not exist yet:
// the deepest existing ancestor is resolved and the rest appended.
func realpath(p string, depth int) (string, error) {
	if depth > 40 {
		return "", errors.New("too many levels of symbolic links")
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	}
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		return realpath(filepath.Clean(target), depth+1)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p, nil
	}
	rp, err := realpath(parent, depth+1)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(p)), nil
}
