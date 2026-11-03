package task

import "testing"

func TestHiddenPriority(t *testing.T) {
	var s Store
	s.Add("low", false, 1)
	s.Add("high", false, 5)
	s.Add("high2", true, 5)
	s.Add("mid", false, 3)
	var got []string
	for _, td := range s.List() {
		got = append(got, td.Title)
		if td.Title == "high" && td.Priority != 5 {
			t.Error("Priority not stored")
		}
	}
	want := []string{"high", "high2", "mid", "low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List order = %v, want %v", got, want)
		}
	}
	var seeded Store
	Seed(&seeded)
	if len(seeded.List()) != 3 {
		t.Error("Seed broken")
	}
}
