package task

import (
	"fmt"
	"strconv"
	"strings"
)

type version struct {
	core [3]uint64
	pre  []string
}

func parse(s string) (version, error) {
	var v version
	bad := fmt.Errorf("invalid semantic version %q", s)
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdents(s[i+1:], false) {
			return v, bad
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if !validIdents(s[i+1:], true) {
			return v, bad
		}
		v.pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, bad
	}
	for i, p := range parts {
		if !numeric(p) || (len(p) > 1 && p[0] == '0') {
			return v, bad
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return v, bad
		}
		v.core[i] = n
	}
	return v, nil
}

func validIdents(s string, pre bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for _, r := range id {
			if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
		if pre && numeric(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Compare returns -1 if a < b, 0 if they have equal precedence, and 1 if a > b.
func Compare(a, b string) (int, error) {
	va, err := parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range va.core {
		if va.core[i] != vb.core[i] {
			return cmp(va.core[i] < vb.core[i]), nil
		}
	}
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0, nil
	case len(va.pre) == 0:
		return 1, nil
	case len(vb.pre) == 0:
		return -1, nil
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		x, y := va.pre[i], vb.pre[i]
		if x == y {
			continue
		}
		xn, yn := numeric(x), numeric(y)
		switch {
		case xn && yn:
			p, _ := strconv.ParseUint(x, 10, 64)
			q, _ := strconv.ParseUint(y, 10, 64)
			return cmp(p < q), nil
		case xn:
			return -1, nil
		case yn:
			return 1, nil
		default:
			return cmp(x < y), nil
		}
	}
	switch {
	case len(va.pre) < len(vb.pre):
		return -1, nil
	case len(va.pre) > len(vb.pre):
		return 1, nil
	}
	return 0, nil
}

func cmp(less bool) int {
	if less {
		return -1
	}
	return 1
}
