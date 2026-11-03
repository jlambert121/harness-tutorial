package diff

import "testing"

func TestUnified(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n"
	b := "1\n2\n3\nfour\n5\n6\n7\n8\n9\nten\n"
	got := Unified(a, b, "f", 1)
	want := "--- f\n+++ f\n@@ -3,3 +3,3 @@\n 3\n-4\n+four\n 5\n@@ -9,1 +9,2 @@\n 9\n+ten\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if Unified(a, a, "f", 3) != "" {
		t.Error("equal inputs should produce no diff")
	}
}
