package session_test

import (
	"testing"

	"harness/internal/llm"
	"harness/internal/session"
)

func TestMessagesRestartFromLatestCompaction(t *testing.T) {
	s, _ := session.Create(t.TempDir(), "/w")
	for _, m := range []string{"q1", "a1", "q2", "a2"} {
		role := llm.RoleUser
		if m[0] == 'a' {
			role = llm.RoleAssistant
		}
		s.AppendMessage(*msg(role, m))
	}
	s.AppendCompaction([]llm.Message{*msg(llm.RoleUser, "summary of q1/a1"), *msg(llm.RoleUser, "q2"), *msg(llm.RoleAssistant, "a2")})
	s.AppendMessage(*msg(llm.RoleUser, "q3"))
	s.Close()

	r, _ := session.Open(s.Path())
	if got := texts(r.Messages(r.Leaf())); got != "summary of q1/a1,q2,a2,q3" {
		t.Errorf("messages = %q; resume must start from the compacted history", got)
	}
	var count int
	for _, e := range r.Entries() {
		if e.Type == "message" {
			count++
		}
	}
	if count != 5 {
		t.Errorf("the original messages must stay in the file; message entries = %d", count)
	}
}
