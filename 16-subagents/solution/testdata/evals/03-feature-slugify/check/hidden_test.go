package task

import "testing"

func TestHiddenSlugify(t *testing.T) {
	cases := map[string]string{
		"  Hello, World! 2026 ": "hello-world-2026",
		"Go--is__fun":           "go-is-fun",
		"Crème brûlée":          "cr-me-br-l-e",
		"---":                   "",
		"":                      "",
		"ABC123":                "abc123",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
