// Command sessions lists saved sessions and draws a session's branch tree.
//
//	sessions [--root DIR] list
//	sessions [--root DIR] tree [path-or-id]     (default: the latest session)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"harness/internal/llm"
	"harness/internal/session"
)

func dataDir() string {
	if d := os.Getenv("HARNESS_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "harness")
}

func main() {
	root := flag.String("root", ".", "workspace whose sessions to show")
	flag.Parse()
	abs, _ := filepath.Abs(*root)
	dir := session.DirFor(dataDir(), abs)
	switch flag.Arg(0) {
	case "list", "":
		list, err := session.List(dir)
		if err != nil || len(list) == 0 {
			fmt.Println("no sessions for", abs)
			return
		}
		for _, s := range list {
			fmt.Printf("%s  %s  %3d entries  %s\n", s.ID, s.Modified.Format("2006-01-02 15:04"), s.Entries, oneLine(s.FirstPrompt, 60))
		}
	case "tree":
		path := flag.Arg(1)
		if path == "" {
			path, _ = session.Latest(dir)
		} else if _, err := os.Stat(path); err != nil {
			list, _ := session.List(dir)
			for _, s := range list {
				if s.ID == path {
					path = s.Path
				}
			}
		}
		s, err := session.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		drawTree(s)
	default:
		fmt.Fprintln(os.Stderr, "usage: sessions [--root DIR] list | tree [path-or-id]")
		os.Exit(2)
	}
}

func drawTree(s *session.Store) {
	children := map[string][]session.Entry{}
	var root session.Entry
	for _, e := range s.Entries() {
		if e.ParentID == "" {
			root = e
			continue
		}
		children[e.ParentID] = append(children[e.ParentID], e)
	}
	onPath := map[string]bool{}
	for _, e := range s.PathTo(s.Leaf()) {
		onPath[e.ID] = true
	}
	fmt.Printf("%s  (● = current branch)\n", s.Path())
	var walk func(e session.Entry, prefix string, last bool)
	walk = func(e session.Entry, prefix string, last bool) {
		branch, next := "├─ ", prefix+"│  "
		if last {
			branch, next = "└─ ", prefix+"   "
		}
		mark := " "
		if onPath[e.ID] {
			mark = "●"
		}
		fmt.Printf("%s%s%s %s %s\n", prefix, branch, mark, e.ID, describe(e))
		kids := children[e.ID]
		// A long linear chain prints flat to keep the tree readable.
		for len(kids) == 1 {
			e = kids[0]
			mark = " "
			if onPath[e.ID] {
				mark = "●"
			}
			fmt.Printf("%s%s %s %s\n", next, mark, e.ID, describe(e))
			kids = children[e.ID]
		}
		for i, k := range kids {
			walk(k, next, i == len(kids)-1)
		}
	}
	fmt.Printf("%s %s\n", root.ID, describe(root))
	for i, k := range children[root.ID] {
		walk(k, "", i == len(children[root.ID])-1)
	}
}

func describe(e session.Entry) string {
	if e.Type != "message" || e.Message == nil {
		return "[" + e.Type + "]"
	}
	m := e.Message
	switch {
	case m.Role == llm.RoleTool:
		return "tool  " + m.Result.Name + ": " + oneLine(m.Result.Content, 50)
	case len(m.ToolCalls()) > 0:
		var names []string
		for _, c := range m.ToolCalls() {
			names = append(names, c.Name)
		}
		return "model " + oneLine(m.Text(), 30) + " → " + strings.Join(names, ", ")
	case m.Role == llm.RoleUser:
		return "user  " + oneLine(m.Blocks[len(m.Blocks)-1].Text, 60)
	}
	return "model " + oneLine(m.Text(), 60)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}
