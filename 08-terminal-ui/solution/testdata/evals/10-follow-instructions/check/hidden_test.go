package task

import (
	"errors"
	"strings"
	"testing"
)

func TestHiddenEmpty(t *testing.T) {
	for _, in := range []string{"", "  \n\t "} {
		_, err := ParseConfig(in)
		if !errors.Is(err, ErrEmpty) {
			t.Errorf("ParseConfig(%q) err = %v, want errors.Is(err, ErrEmpty)", in, err)
		}
		if err == nil || !strings.HasPrefix(err.Error(), "config: ") {
			t.Errorf("error %v must start with \"config: \"", err)
		}
	}
	if c, err := ParseConfig("a=1"); err != nil || c["a"] != "1" {
		t.Error("normal parsing broke")
	}
}
