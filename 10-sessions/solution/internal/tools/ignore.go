package tools

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ignorer implements the common subset of .gitignore: comments, blank lines,
// trailing "/" (directories only), leading "/" or an inner "/" (anchored to
// the root), and globs. Negation ("!") is not supported. Only the root
// .gitignore is read. ".git" is always ignored.
type ignorer struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern  string
	dirOnly  bool
	anchored bool
}

func loadIgnore(root string) *ignorer {
	ig := &ignorer{}
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return ig
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		r := ignoreRule{}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			r.anchored = true
			line = strings.TrimPrefix(line, "/")
		} else if strings.Contains(line, "/") {
			r.anchored = true
		}
		r.pattern = line
		ig.rules = append(ig.rules, r)
	}
	return ig
}

// match reports whether rel (slash-separated, relative to root) is ignored.
func (ig *ignorer) match(rel string, isDir bool) bool {
	base := path.Base(rel)
	if base == ".git" {
		return true
	}
	for _, r := range ig.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.anchored {
			if ok, _ := path.Match(r.pattern, rel); ok {
				return true
			}
		} else if ok, _ := path.Match(r.pattern, base); ok {
			return true
		}
	}
	return false
}

// walk visits non-ignored files under dir (absolute), calling fn with paths
// relative to root. Ignored directories are not descended into.
func walk(root, dir string, ig *ignorer, fn func(rel string, info os.FileInfo) error) error {
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if ig.match(rel, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		return fn(rel, info)
	})
}

// matchGlob matches slash-separated paths against patterns with "**"
// (any number of directories), "*", "?", and "[...]".
func matchGlob(pattern, name string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
