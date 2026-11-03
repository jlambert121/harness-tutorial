package textkit

import "testing"

func TestTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello world", "Hello World"},
		{"élan vital", "Élan Vital"},
	}
	for _, tt := range tests {
		if got := Title(tt.in); got != tt.want {
			t.Errorf("Title(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWordCount(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"one two  three", 3},
	}
	for _, tt := range tests {
		if got := WordCount(tt.in); got != tt.want {
			t.Errorf("WordCount(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestReverse(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"abc", "cba"},
		{"héllo", "olléh"},
		{"日本語", "語本日"},
	}
	for _, tt := range tests {
		if got := Reverse(tt.in); got != tt.want {
			t.Errorf("Reverse(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
