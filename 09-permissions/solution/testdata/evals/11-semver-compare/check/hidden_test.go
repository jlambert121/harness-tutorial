package task

import "testing"

func TestHiddenCompareOrder(t *testing.T) {
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "1.10.0", "2.0.0"}
	for i := range order {
		for j := range order {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			got, err := Compare(order[i], order[j])
			if err != nil || got != want {
				t.Errorf("Compare(%s, %s) = %d, %v; want %d", order[i], order[j], got, err, want)
			}
		}
	}
}

func TestHiddenCompareBuildMetadata(t *testing.T) {
	if c, err := Compare("1.0.0+build.1", "1.0.0+build.2"); err != nil || c != 0 {
		t.Errorf("build metadata must be ignored: %d %v", c, err)
	}
	if c, _ := Compare("1.0.0-alpha+001", "1.0.0-alpha"); c != 0 {
		t.Error("build metadata after pre-release")
	}
}

func TestHiddenCompareInvalid(t *testing.T) {
	for _, bad := range []string{"", "1", "1.0", "1.0.0.0", "01.0.0", "1.0.0-", "1.0.0-01", "v1.0.0", "1.0.0-alpha..1", "a.b.c"} {
		if _, err := Compare(bad, "1.0.0"); err == nil {
			t.Errorf("Compare(%q) should fail", bad)
		}
	}
}
