// Package session persists conversations as append-only JSONL trees and
// snapshots the workspace so changes can be undone.
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"harness/internal/llm"
)

// Entry is one line of a session file. Entries form a tree through ParentID;
// the conversation is the path from the root to a leaf.
type Entry struct {
	ID       string          `json:"id"`
	ParentID string          `json:"parent_id,omitempty"`
	Time     time.Time       `json:"time"`
	Type     string          `json:"type"` // header, message, snapshot, model_change, compaction, label, ...
	Message  *llm.Message    `json:"message,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type header struct {
	Version int    `json:"version"`
	Cwd     string `json:"cwd"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	entries []Entry
	byID    map[string]int
	leaf    string
	cwd     string
	err     error
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// DirFor is where sessions for a working directory live.
func DirFor(dataDir, cwd string) string {
	slug := strings.NewReplacer("/", "-", " ", "-", "\\", "-", ":", "-").Replace(cwd)
	return filepath.Join(dataDir, "sessions", slug)
}

// Create starts a new session file in dir.
func Create(dir, cwd string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	id := newID()
	path := filepath.Join(dir, time.Now().Format("2006-01-02T15-04-05")+"_"+id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, f: f, byID: map[string]int{}, cwd: cwd}
	h, _ := json.Marshal(header{Version: 1, Cwd: cwd})
	s.Append(Entry{ID: id, Type: "header", Data: h})
	return s, s.err
}

// Open loads a session file. A torn final line (crash mid-write) is dropped
// and truncated away so later appends produce a valid file.
func Open(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, byID: map[string]int{}}
	good := 0
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var e Entry
		if len(strings.TrimSpace(string(line))) == 0 {
			good += len(line) + 1
			continue
		}
		if err := json.Unmarshal(line, &e); err != nil {
			break
		}
		good += len(line) + 1
		s.add(e)
	}
	if len(s.entries) == 0 || s.entries[0].Type != "header" {
		return nil, fmt.Errorf("%s: not a session file", path)
	}
	var h header
	json.Unmarshal(s.entries[0].Data, &h)
	s.cwd = h.Cwd
	if good > len(data) {
		good = len(data)
	}
	if good < len(data) {
		if err := os.Truncate(path, int64(good)); err != nil {
			return nil, err
		}
	}
	if s.f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) add(e Entry) {
	s.byID[e.ID] = len(s.entries)
	s.entries = append(s.entries, e)
	s.leaf = e.ID
}

// Append writes e as a child of the current leaf (unless ParentID is set),
// fsyncs, and makes it the new leaf. It returns the entry's ID.
func (s *Store) Append(e Entry) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = newID()
	}
	if e.ParentID == "" && e.Type != "header" {
		e.ParentID = s.leaf
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b, err := json.Marshal(e)
	if err == nil {
		_, err = s.f.Write(append(b, '\n'))
	}
	if err == nil {
		err = s.f.Sync()
	}
	if err != nil {
		s.err = err
	}
	s.add(e)
	return e.ID
}

func (s *Store) AppendMessage(m llm.Message) string {
	return s.Append(Entry{Type: "message", Message: &m})
}

// AppendCompaction records a replacement history (Codex's replacement_history).
// Earlier entries stay in the file; Messages starts over from here.
func (s *Store) AppendCompaction(history []llm.Message) string {
	data, _ := json.Marshal(map[string]any{"history": history})
	return s.Append(Entry{Type: "compaction", Data: data})
}

// Branch moves the conversation back to entry id; the next Append starts a
// new branch from there. Nothing is deleted. The move is itself recorded (a
// "branch" entry whose parent is id), so it survives reopening the file.
func (s *Store) Branch(id string) {
	s.mu.Lock()
	_, ok := s.byID[id]
	s.mu.Unlock()
	if ok {
		s.Append(Entry{Type: "branch", ParentID: id})
	}
}

// Messages returns the conversation on the path from the root to leaf.
func (s *Store) Messages(leaf string) []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []llm.Message
	for _, e := range s.chain(leaf) {
		switch {
		case e.Type == "message" && e.Message != nil:
			out = append(out, *e.Message)
		case e.Type == "compaction":
			var c struct{ History []llm.Message }
			if json.Unmarshal(e.Data, &c) == nil {
				out = c.History
			}
		}
	}
	return out
}

// PathTo returns the entries from the root to leaf.
func (s *Store) PathTo(leaf string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chain(leaf)
}

func (s *Store) chain(leaf string) []Entry {
	var rev []Entry
	for id := leaf; id != ""; {
		i, ok := s.byID[id]
		if !ok {
			break
		}
		rev = append(rev, s.entries[i])
		id = s.entries[i].ParentID
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Last returns the most recent entry of the given type on the current branch.
func (s *Store) Last(typ string) (Entry, bool) {
	p := s.PathTo(s.Leaf())
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].Type == typ {
			return p[i], true
		}
	}
	return Entry{}, false
}

// Leaves returns entries with no children, oldest first.
func (s *Store) Leaves() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	hasChild := map[string]bool{}
	for _, e := range s.entries {
		hasChild[e.ParentID] = true
	}
	var out []string
	for _, e := range s.entries {
		if !hasChild[e.ID] {
			out = append(out, e.ID)
		}
	}
	return out
}

func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.entries...)
}

func (s *Store) Leaf() string { s.mu.Lock(); defer s.mu.Unlock(); return s.leaf }
func (s *Store) Path() string { return s.path }
func (s *Store) Cwd() string  { return s.cwd }
func (s *Store) ID() string   { return s.entries[0].ID }
func (s *Store) Err() error   { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

// Repair makes a history valid to send: every tool call gets a result. A
// crash or kill between a tool call and its result would otherwise leave an
// orphan that providers reject.
func Repair(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for i := 0; i < len(msgs); i++ {
		out = append(out, msgs[i])
		calls := msgs[i].ToolCalls()
		if len(calls) == 0 {
			continue
		}
		got := map[string]bool{}
		j := i + 1
		for ; j < len(msgs) && msgs[j].Role == llm.RoleTool; j++ {
			out = append(out, msgs[j])
			if msgs[j].Result != nil {
				got[msgs[j].Result.CallID] = true
			}
		}
		for _, c := range calls {
			if !got[c.ID] {
				r := llm.ToolResult{CallID: c.ID, Name: c.Name, IsError: true,
					Content: "Tool execution was interrupted (the harness stopped before it finished). Check the current state before retrying."}
				out = append(out, llm.Message{Role: llm.RoleTool, Result: &r})
			}
		}
		i = j - 1
	}
	return out
}

type Info struct {
	Path, ID    string
	Modified    time.Time
	FirstPrompt string
	Entries     int
}

// List returns sessions in dir, newest first.
func List(dir string) ([]Info, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, f := range files {
		s, err := Open(f)
		if err != nil {
			continue
		}
		fi, _ := os.Stat(f)
		info := Info{Path: f, ID: s.ID(), Modified: fi.ModTime(), Entries: len(s.entries)}
		for _, m := range s.Messages(s.Leaf()) {
			if m.Role == llm.RoleUser {
				info.FirstPrompt = m.Blocks[len(m.Blocks)-1].Text
				break
			}
		}
		s.Close()
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path > out[j].Path })
	return out, nil
}

// Latest returns the newest session file in dir.
func Latest(dir string) (string, error) {
	list, err := List(dir)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", fmt.Errorf("no sessions in %s", dir)
	}
	return list[0].Path, nil
}
