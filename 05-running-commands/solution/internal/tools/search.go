package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const maxResults = 100

type globArgs struct {
	Pattern string `json:"pattern" desc:"Glob such as **/*.go or cmd/*/main.go. ** matches any number of directories."`
	Path    string `json:"path,omitempty" desc:"Directory to search, relative to the workspace root (default: root)"`
}

func Glob() Tool {
	return New("glob", "Find files by name pattern. Respects .gitignore. Returns up to 100 paths, sorted.", true,
		func(ctx context.Context, env *Env, a globArgs) Result {
			dir, err := env.Resolve(a.Path)
			if err != nil {
				return errorf("%v", err)
			}
			var matches []string
			err = walk(env.Root, dir, loadIgnore(env.Root), func(rel string, _ os.FileInfo) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				sub := rel
				if dr := relPath(env, dir); dr != "." && dr != "" {
					sub = strings.TrimPrefix(rel, dr+"/")
				}
				if matchGlob(a.Pattern, sub) {
					matches = append(matches, rel)
				}
				return nil
			})
			if err != nil {
				return errorf("glob: %v", err)
			}
			return list(matches, "No files match "+a.Pattern)
		})
}

func list(items []string, empty string) Result {
	if len(items) == 0 {
		return Result{Content: empty}
	}
	slices.Sort(items)
	n := len(items)
	if n > maxResults {
		items = items[:maxResults]
	}
	out := strings.Join(items, "\n") + "\n"
	if n > maxResults {
		out += fmt.Sprintf("(Showing %d of %d matches. Narrow the pattern or path.)\n", maxResults, n)
	}
	return Result{Content: out}
}

type grepArgs struct {
	Pattern string `json:"pattern" desc:"RE2 regular expression, e.g. func\\s+Load or (?i)todo. Escape literal ( ) . [ with a backslash."`
	Path    string `json:"path,omitempty" desc:"Directory or file to search (default: workspace root)"`
	Include string `json:"include,omitempty" desc:"Only search files whose name matches this glob, e.g. *.go"`
	Mode    string `json:"mode,omitempty" enum:"files,content,count" desc:"files (default): matching file paths; content: path:line:text; count: matches per file"`
}

func Grep() Tool {
	return New("grep", "Search file contents with a regular expression. Respects .gitignore and skips binary files. Returns up to 100 results.", true,
		func(ctx context.Context, env *Env, a grepArgs) Result {
			re, err := regexp.Compile(a.Pattern)
			if err != nil {
				return errorf("pattern is not a valid regular expression: %v. To match literal text, escape special characters, e.g. %s", err, regexp.QuoteMeta(a.Pattern))
			}
			dir, err := env.Resolve(a.Path)
			if err != nil {
				return errorf("%v", err)
			}
			var out []string
			counts := map[string]int{}
			err = walk(env.Root, dir, loadIgnore(env.Root), func(rel string, info os.FileInfo) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if info.Size() > 5<<20 {
					return nil
				}
				if a.Include != "" && !matchGlob(a.Include, path.Base(rel)) && !matchGlob(a.Include, rel) {
					return nil
				}
				grepFile(filepath.Join(env.Root, rel), rel, re, a.Mode, &out, counts)
				return nil
			})
			if err != nil {
				return errorf("grep: %v", err)
			}
			switch a.Mode {
			case "content":
				return lines(out, "No matches for "+a.Pattern)
			case "count":
				var rows []string
				for f, n := range counts {
					rows = append(rows, fmt.Sprintf("%s:%d", f, n))
				}
				return list(rows, "No matches for "+a.Pattern)
			default:
				var files []string
				for f := range counts {
					files = append(files, f)
				}
				return list(files, "No matches for "+a.Pattern)
			}
		})
}

func grepFile(abs, rel string, re *regexp.Regexp, mode string, out *[]string, counts map[string]int) {
	f, err := os.Open(abs)
	if err != nil {
		return
	}
	defer f.Close()
	head := make([]byte, 8000)
	n, _ := f.Read(head)
	if isBinary(head[:n]) {
		return
	}
	f.Seek(0, 0)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for ln := 1; sc.Scan(); ln++ {
		text := sc.Text()
		if !re.MatchString(text) {
			continue
		}
		counts[rel]++
		if mode == "content" {
			if len(text) > 300 {
				text = text[:300] + "…"
			}
			*out = append(*out, fmt.Sprintf("%s:%d:%s", rel, ln, text))
		}
	}
}

// lines keeps result order (file walk order, then line order) instead of sorting.
func lines(rows []string, empty string) Result {
	if len(rows) == 0 {
		return Result{Content: empty}
	}
	n := len(rows)
	if n > maxResults {
		rows = rows[:maxResults]
	}
	out := strings.Join(rows, "\n") + "\n"
	if n > maxResults {
		out += fmt.Sprintf("(Showing %d of %d matching lines. Narrow the pattern, path, or include.)\n", maxResults, n)
	}
	return Result{Content: out}
}

type lsArgs struct {
	Path  string `json:"path,omitempty" desc:"Directory to list (default: workspace root)"`
	Depth int    `json:"depth,omitempty" desc:"Levels to descend, 1-3 (default 1)"`
}

func Ls() Tool {
	return New("ls", "List a directory as a tree. Directories end with /. Respects .gitignore.", true,
		func(ctx context.Context, env *Env, a lsArgs) Result {
			dir, err := env.Resolve(a.Path)
			if err != nil {
				return errorf("%v", err)
			}
			depth := min(max(a.Depth, 1), 3)
			ig := loadIgnore(env.Root)
			var rows []string
			var visit func(abs string, level int)
			visit = func(abs string, level int) {
				entries, err := os.ReadDir(abs)
				if err != nil {
					return
				}
				for _, e := range entries {
					if len(rows) >= 500 {
						return
					}
					child := filepath.Join(abs, e.Name())
					if ig.match(relPath(env, child), e.IsDir()) {
						continue
					}
					name := e.Name()
					if e.IsDir() {
						name += "/"
					}
					rows = append(rows, strings.Repeat("  ", level)+name)
					if e.IsDir() && level+1 < depth {
						visit(child, level+1)
					}
				}
			}
			visit(dir, 0)
			if len(rows) == 0 {
				return Result{Content: "(empty directory)"}
			}
			out := strings.Join(rows, "\n") + "\n"
			if len(rows) >= 500 {
				out += "(Listing stopped at 500 entries.)\n"
			}
			return Result{Content: out}
		})
}
