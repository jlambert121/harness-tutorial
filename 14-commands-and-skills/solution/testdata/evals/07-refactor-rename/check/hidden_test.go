package task

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestHiddenRenamed(t *testing.T) {
	var u User = User{ID: 2, Name: "bo"}
	s := NewStore()
	s.Save(u)
	if got, _ := s.Find(2); got.Greeting() != "hi bo" {
		t.Error("behavior changed")
	}
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`\bUsr\b`)
	for _, f := range files {
		if f == "hidden_test.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		if re.Match(b) {
			t.Errorf("%s still mentions Usr", f)
		}
	}
}
