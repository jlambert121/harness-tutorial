package tools_test

import (
	"strings"
	"testing"
	"time"

	"harness/internal/tools"
)

func TestReadTrackerRecent(t *testing.T) {
	r := tools.NewReadTracker()
	r.MarkRead("/a", time.Now())
	r.MarkRead("/b", time.Now())
	r.MarkRead("/c", time.Now())
	r.MarkRead("/a", time.Now())
	if got := strings.Join(r.Recent(2), ","); got != "/a,/c" {
		t.Errorf("Recent(2) = %s; most recently read or written first, no duplicates", got)
	}
	var nilTracker *tools.ReadTracker
	if nilTracker.Recent(3) != nil {
		t.Error("nil tracker")
	}
}
