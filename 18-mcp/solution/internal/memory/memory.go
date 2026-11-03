// Package memory keeps notes that persist across sessions: one markdown file
// per memory plus a MEMORY.md index, the index loaded at session start and
// topic files read on demand (Claude Code's auto-memory layout).
package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"harness/internal/tools"
)

const index = "MEMORY.md"

type Store struct{ dir string }

func Open(dir string) *Store { return &Store{dir: dir} }

func (s *Store) Dir() string { return s.dir }

// resolve maps a model-supplied name to a file directly inside the memory
// directory. Subdirectories, absolute paths, "..", and the index are refused.
func (s *Store) resolve(p string) (string, error) {
	clean := filepath.Clean(p)
	if p == "" || filepath.IsAbs(p) || strings.Contains(clean, string(filepath.Separator)) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("memory paths are plain file names like test-style.md; got %q", p)
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		clean += ".md"
	}
	if strings.EqualFold(clean, index) {
		return "", fmt.Errorf("%s is maintained automatically; create or edit topic files instead", index)
	}
	return filepath.Join(s.dir, clean), nil
}

type meta struct{ name, description, typ string }

func parseMeta(content string) (meta, bool) {
	var m meta
	if !strings.HasPrefix(content, "---\n") {
		return m, false
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return m, false
	}
	for _, line := range strings.Split(content[4:4+end], "\n") {
		k, v, _ := strings.Cut(line, ":")
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			m.name = v
		case "description":
			m.description = v
		case "type":
			m.typ = v
		}
	}
	return m, m.name != "" && m.description != ""
}

// reindex rewrites MEMORY.md from the topic files' frontmatter.
func (s *Store) reindex() error {
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.md"))
	sort.Strings(files)
	var b strings.Builder
	for _, f := range files {
		if filepath.Base(f) == index {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m, ok := parseMeta(string(data)); ok {
			fmt.Fprintf(&b, "- [%s](%s) — %s\n", m.name, filepath.Base(f), m.description)
		}
	}
	return os.WriteFile(filepath.Join(s.dir, index), []byte(b.String()), 0o644)
}

// Recall returns the index for the first-message context, cut at maxLines
// lines or maxBytes bytes, or "" when there are no memories.
func (s *Store) Recall(maxLines, maxBytes int) string {
	data, err := os.ReadFile(filepath.Join(s.dir, index))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return ""
	}
	lines := strings.SplitAfter(strings.TrimRight(string(data), "\n"), "\n")
	truncated := false
	if len(lines) > maxLines {
		lines, truncated = lines[:maxLines], true
	}
	text := strings.Join(lines, "")
	if len(text) > maxBytes {
		text, truncated = strings.ToValidUTF8(text[:maxBytes], ""), true
	}
	if truncated {
		text += "\n[index truncated; use the memory tool's view command to list everything]"
	}
	return fmt.Sprintf("<memory dir=%q>\nNotes saved in earlier sessions. Read a topic file with the memory tool when it is relevant; it may be out of date, so check it against the code.\n%s\n</memory>", s.dir, strings.TrimRight(text, "\n"))
}

// Guidance tells the model when to write memories. It goes in the
// first-message context, so turning memory on does not change the system prompt.
const Guidance = `<memory_guidance>
You have a memory tool for notes that should outlive this session. Save:
- user: who the user is and how they like to work;
- feedback: corrections and confirmed preferences about how you work, with the reason;
- project: decisions, constraints, and goals that are not visible in the code or git history;
- reference: where to find things outside the repository.
Do not save what the code, git history, or AGENTS.md already says, or details only relevant to this conversation.
Before creating a memory, view the index; update an existing memory instead of adding a duplicate.
Each memory is one file with frontmatter: name, description (one line, used in the index), type.
</memory_guidance>`

type memArgs struct {
	Command string `json:"command" enum:"view,create,str_replace,delete" desc:"view (list memories, or show one with path), create, str_replace, delete"`
	Path    string `json:"path,omitempty" desc:"Memory file name, e.g. test-style.md"`
	Content string `json:"content,omitempty" desc:"create: full file content, starting with frontmatter (name, description, type)"`
	OldStr  string `json:"old_str,omitempty" desc:"str_replace: exact text to replace (must be unique)"`
	NewStr  string `json:"new_str,omitempty" desc:"str_replace: replacement text"`
}

func (s *Store) Tool() tools.Tool {
	return tools.New("memory", "Read and write notes that persist across sessions for this project. See <memory_guidance>.", false,
		func(ctx context.Context, env *tools.Env, a memArgs) tools.Result {
			fail := func(format string, v ...any) tools.Result {
				return tools.Result{IsError: true, Content: fmt.Sprintf(format, v...)}
			}
			if err := os.MkdirAll(s.dir, 0o755); err != nil {
				return fail("%v", err)
			}
			if a.Command == "view" && a.Path == "" {
				data, _ := os.ReadFile(filepath.Join(s.dir, index))
				if len(strings.TrimSpace(string(data))) == 0 {
					return tools.Result{Content: "There are no memories yet."}
				}
				return tools.Result{Content: string(data)}
			}
			path, err := s.resolve(a.Path)
			if err != nil {
				return fail("%v", err)
			}
			switch a.Command {
			case "view":
				data, err := os.ReadFile(path)
				if err != nil {
					return fail("no memory %s", a.Path)
				}
				return tools.Result{Content: string(data)}
			case "create":
				if _, ok := parseMeta(a.Content); !ok {
					return fail("content must start with frontmatter containing name and description:\n---\nname: short-slug\ndescription: one line for the index\ntype: user|feedback|project|reference\n---")
				}
				if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
					return fail("%v", err)
				}
			case "str_replace":
				data, err := os.ReadFile(path)
				if err != nil {
					return fail("no memory %s", a.Path)
				}
				switch n := strings.Count(string(data), a.OldStr); {
				case a.OldStr == "" || n == 0:
					return fail("old_str not found in %s", a.Path)
				case n > 1:
					return fail("old_str appears %d times in %s; include more context", n, a.Path)
				}
				updated := strings.Replace(string(data), a.OldStr, a.NewStr, 1)
				if _, ok := parseMeta(updated); !ok {
					return fail("this edit would break the frontmatter (name, description); the memory would drop out of the index")
				}
				if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
					return fail("%v", err)
				}
			case "delete":
				if err := os.Remove(path); err != nil {
					return fail("no memory %s", a.Path)
				}
			default:
				return fail("unknown command %q", a.Command)
			}
			if err := s.reindex(); err != nil {
				return fail("index: %v", err)
			}
			return tools.Result{Content: fmt.Sprintf("%s %s; MEMORY.md updated.", a.Command, filepath.Base(path))}
		})
}
