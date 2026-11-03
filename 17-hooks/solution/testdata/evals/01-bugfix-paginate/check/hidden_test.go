package task

import (
	"slices"
	"testing"
)

func TestHiddenPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7}
	cases := []struct {
		page, size int
		want       []int
	}{
		{1, 3, []int{1, 2, 3}}, {2, 3, []int{4, 5, 6}}, {3, 3, []int{7}}, {4, 3, nil}, {0, 3, nil}, {1, 10, items},
	}
	for _, c := range cases {
		if got := Paginate(items, c.page, c.size); !slices.Equal(got, c.want) {
			t.Errorf("Paginate(page=%d,size=%d) = %v, want %v", c.page, c.size, got, c.want)
		}
	}
}
