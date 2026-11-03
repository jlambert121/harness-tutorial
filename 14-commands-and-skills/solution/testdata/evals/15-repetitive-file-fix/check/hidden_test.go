package task

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestHiddenTextMatchesNetHTTP(t *testing.T) {
	for c := 100; c < 600; c++ {
		if want := http.StatusText(c); want != "" && Text(c) != want {
			t.Errorf("Text(%d) = %q, want %q", c, Text(c), want)
		}
	}
	b, _ := os.ReadFile("status.go")
	if strings.Contains(string(b), "net/http") {
		t.Error("status.go must not import net/http")
	}
}
