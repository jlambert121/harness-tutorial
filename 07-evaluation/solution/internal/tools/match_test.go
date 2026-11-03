package tools_test

import (
	"errors"
	"testing"

	"harness/internal/tools"
)

const src = "func main() {\n\tx := 1\n\tif x > 0 {\n\t\tfmt.Println(\"positive\")\n\t}\n}\n"

func TestFindMatchExact(t *testing.T) {
	s, e, used, err := tools.FindMatch(src, "\tx := 1\n", tools.AllStrategies)
	if err != nil || used != tools.StrategyExact || src[s:e] != "\tx := 1\n" {
		t.Errorf("s=%d e=%d used=%s err=%v", s, e, used, err)
	}
}

func TestFindMatchTrailingWhitespace(t *testing.T) {
	s, e, used, err := tools.FindMatch(src, "\tx := 1   \n\tif x > 0 {  ", tools.AllStrategies)
	if err != nil || used != tools.StrategyTrailingWS {
		t.Fatalf("used=%s err=%v", used, err)
	}
	if src[s:e] != "\tx := 1\n\tif x > 0 {" {
		t.Errorf("matched %q", src[s:e])
	}
}

func TestFindMatchIndentationDrift(t *testing.T) {
	s, e, used, err := tools.FindMatch(src, "    if x > 0 {\n        fmt.Println(\"positive\")", tools.AllStrategies)
	if err != nil || used != tools.StrategyTrimmed || src[s:e] != "\tif x > 0 {\n\t\tfmt.Println(\"positive\")" {
		t.Errorf("used=%s err=%v matched=%q", used, err, src[s:max(s, e)])
	}
}

func TestFindMatchUnicodeQuotes(t *testing.T) {
	_, _, used, err := tools.FindMatch(src, "fmt.Println(“positive”)", tools.AllStrategies)
	if err != nil || used != tools.StrategyUnicode {
		t.Errorf("used=%s err=%v", used, err)
	}
}

func TestFindMatchRespectsStrategyList(t *testing.T) {
	_, _, _, err := tools.FindMatch(src, "\tx := 1   ", []tools.Strategy{tools.StrategyExact})
	if !errors.Is(err, tools.ErrNoMatch) {
		t.Errorf("exact-only must not fall back; err=%v", err)
	}
}

func TestFindMatchAmbiguous(t *testing.T) {
	_, _, _, err := tools.FindMatch("a\nb\na\n", "a", tools.AllStrategies)
	var amb *tools.AmbiguousError
	if !errors.As(err, &amb) || amb.Count != 2 {
		t.Errorf("err=%v", err)
	}
}

func TestFuzzyAmbiguityIsNotResolvedByGuessing(t *testing.T) {
	content := "\tfoo()\n\tbar()\n  foo()\n"
	_, _, _, err := tools.FindMatch(content, "foo()", tools.AllStrategies)
	var amb *tools.AmbiguousError
	if !errors.As(err, &amb) {
		t.Errorf("two trimmed matches must be reported as ambiguous, got %v", err)
	}
}

func TestNearMissDoesNotMatch(t *testing.T) {
	_, _, _, err := tools.FindMatch(src, "\ty := 1\n\tif y > 0 {", tools.AllStrategies)
	if !errors.Is(err, tools.ErrNoMatch) {
		t.Errorf("different identifiers must not match under any strategy; err=%v", err)
	}
}
