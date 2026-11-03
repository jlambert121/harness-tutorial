package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/llm"
	"harness/internal/session"
)

func msg(role llm.Role, text string) *llm.Message {
	m := llm.Message{Role: role, Blocks: []llm.Block{{Text: text}}}
	return &m
}

func texts(ms []llm.Message) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Text())
	}
	return strings.Join(out, ",")
}

func TestAppendAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := session.Create(dir, "/work/proj")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(s.Path()), time.Now().Format("2006")) || filepath.Ext(s.Path()) != ".jsonl" {
		t.Errorf("path = %s; want <timestamp>_<id>.jsonl", s.Path())
	}
	s.AppendMessage(*msg(llm.RoleUser, "hi"))
	s.AppendMessage(*msg(llm.RoleAssistant, "hello"))
	s.Close()

	r, err := session.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd() != "/work/proj" || r.ID() != s.ID() {
		t.Errorf("header: cwd=%q id=%q", r.Cwd(), r.ID())
	}
	if got := texts(r.Messages(r.Leaf())); got != "hi,hello" {
		t.Errorf("messages = %q", got)
	}
	entries := r.Entries()
	if entries[0].Type != "header" || entries[1].ParentID != entries[0].ID || entries[2].ParentID != entries[1].ID {
		t.Errorf("entries must form a parent chain from the header: %+v", entries)
	}
}

func TestTornLastLineIgnored(t *testing.T) {
	dir := t.TempDir()
	s, _ := session.Create(dir, "/w")
	s.AppendMessage(*msg(llm.RoleUser, "one"))
	s.AppendMessage(*msg(llm.RoleAssistant, "two"))
	s.Close()
	b, _ := os.ReadFile(s.Path())
	os.WriteFile(s.Path(), b[:len(b)-10], 0o644) // crash mid-write

	r, err := session.Open(s.Path())
	if err != nil {
		t.Fatalf("a torn final line must not make the session unreadable: %v", err)
	}
	if got := texts(r.Messages(r.Leaf())); got != "one" {
		t.Errorf("messages = %q", got)
	}
	r.AppendMessage(*msg(llm.RoleAssistant, "again"))
	r.Close()
	r2, err := session.Open(s.Path())
	if err != nil || texts(r2.Messages(r2.Leaf())) != "one,again" {
		t.Errorf("appending after a torn line must produce a valid file: %v %q", err, texts(r2.Messages(r2.Leaf())))
	}
}

func TestBranchCreatesTree(t *testing.T) {
	s, _ := session.Create(t.TempDir(), "/w")
	s.AppendMessage(*msg(llm.RoleUser, "q1"))
	a1 := s.AppendMessage(*msg(llm.RoleAssistant, "a1"))
	s.AppendMessage(*msg(llm.RoleUser, "q2"))
	s.AppendMessage(*msg(llm.RoleAssistant, "a2"))
	oldLeaf := s.Leaf()

	s.Branch(a1)
	s.AppendMessage(*msg(llm.RoleUser, "q2-alt"))
	s.AppendMessage(*msg(llm.RoleAssistant, "a2-alt"))

	if got := texts(s.Messages(s.Leaf())); got != "q1,a1,q2-alt,a2-alt" {
		t.Errorf("new branch = %q", got)
	}
	if got := texts(s.Messages(oldLeaf)); got != "q1,a1,q2,a2" {
		t.Errorf("old branch must still be readable = %q", got)
	}
	if leaves := s.Leaves(); len(leaves) != 2 {
		t.Errorf("leaves = %v", leaves)
	}
	s.Close()
	r, _ := session.Open(s.Path())
	if r.Leaf() != s.Leaf() {
		t.Errorf("reopen should continue from the most recent entry: %s vs %s", r.Leaf(), s.Leaf())
	}
}

func TestBranchPersistsAcrossReopen(t *testing.T) {
	s, _ := session.Create(t.TempDir(), "/w")
	q1 := s.AppendMessage(*msg(llm.RoleUser, "q1"))
	s.AppendMessage(*msg(llm.RoleAssistant, "a1"))
	s.Branch(q1)
	s.Close()
	r, _ := session.Open(s.Path())
	if got := texts(r.Messages(r.Leaf())); got != "q1" {
		t.Errorf("after reopening, the conversation must continue from the branch point; got %q", got)
	}
}

func TestEntriesOfOtherTypesAreNotMessages(t *testing.T) {
	s, _ := session.Create(t.TempDir(), "/w")
	s.AppendMessage(*msg(llm.RoleUser, "q"))
	s.Append(session.Entry{Type: "snapshot", Data: []byte(`{"tree":"abc"}`)})
	s.AppendMessage(*msg(llm.RoleAssistant, "a"))
	if got := texts(s.Messages(s.Leaf())); got != "q,a" {
		t.Errorf("messages = %q", got)
	}
	e, ok := s.Last("snapshot")
	if !ok || string(e.Data) != `{"tree":"abc"}` {
		t.Errorf("Last(snapshot) = %+v %v", e, ok)
	}
}

func TestRepairDanglingToolCalls(t *testing.T) {
	call := llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
		{ToolCall: &llm.ToolCall{ID: "c1", Name: "bash"}},
		{ToolCall: &llm.ToolCall{ID: "c2", Name: "read"}},
	}}
	done := llm.Message{Role: llm.RoleTool, Result: &llm.ToolResult{CallID: "c1", Name: "bash", Content: "ok"}}
	got := session.Repair([]llm.Message{*msg(llm.RoleUser, "go"), call, done})
	if len(got) != 4 || got[3].Result == nil || got[3].Result.CallID != "c2" || !got[3].Result.IsError || !strings.Contains(got[3].Result.Content, "interrupted") {
		t.Errorf("a crash between tool calls must leave a result for every call: %+v", got)
	}
	healthy := []llm.Message{*msg(llm.RoleUser, "x"), *msg(llm.RoleAssistant, "y")}
	if len(session.Repair(healthy)) != 2 {
		t.Error("Repair must not change a valid history")
	}
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	a, _ := session.Create(dir, "/w")
	a.AppendMessage(*msg(llm.RoleUser, "old"))
	a.Close()
	time.Sleep(1100 * time.Millisecond)
	b, _ := session.Create(dir, "/w")
	b.AppendMessage(*msg(llm.RoleUser, "new"))
	b.Close()
	path, err := session.Latest(dir)
	if err != nil || path != b.Path() {
		t.Errorf("Latest = %q %v, want %q", path, err, b.Path())
	}
	list, _ := session.List(dir)
	if len(list) != 2 || list[0].Path != b.Path() || list[0].FirstPrompt != "new" {
		t.Errorf("List = %+v (newest first, with the first prompt)", list)
	}
}

func TestDirForCwd(t *testing.T) {
	d := session.DirFor("/data", "/Users/me/src/my proj")
	if d != filepath.Join("/data", "sessions", "-Users-me-src-my-proj") {
		t.Errorf("DirFor = %q", d)
	}
}
