package task

import "testing"

func TestStore(t *testing.T) {
	s := NewStore()
	s.Save(Usr{ID: 1, Name: "ada"})
	if u, ok := s.Find(1); !ok || u.Greeting() != "hi ada" {
		t.Error("Find")
	}
}
