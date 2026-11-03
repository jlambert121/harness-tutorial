package main

import (
	"bytes"
	"testing"
)

func TestHiddenGreet(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "hello, world\n"},
		{[]string{"--name", "ada"}, "hello, ada\n"},
		{[]string{"--upper", "--name", "ada"}, "HELLO, ADA\n"},
		{[]string{"--upper"}, "HELLO, WORLD\n"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := run(c.args, &b); err != nil || b.String() != c.want {
			t.Errorf("run(%v) = %q, %v; want %q", c.args, b.String(), err, c.want)
		}
	}
}
