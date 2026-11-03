package llm

import (
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	stream := ": keep-alive comment\n" +
		"data: {\"a\":1}\n\n" +
		"event: message\r\n" +
		"data: line1\r\n" +
		"data: line2\r\n\r\n" +
		"data: [DONE]\n\n" +
		"data: after-done\n\n"
	var got []string
	err := ReadSSE(strings.NewReader(stream), func(data []byte) error {
		got = append(got, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"a":1}`, "line1\nline2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadSSELastEventWithoutBlankLine(t *testing.T) {
	var got []string
	ReadSSE(strings.NewReader("data: x"), func(d []byte) error { got = append(got, string(d)); return nil })
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSSELargeEvent(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	var n int
	if err := ReadSSE(strings.NewReader("data: "+big+"\n\n"), func(d []byte) error { n = len(d); return nil }); err != nil {
		t.Fatal(err)
	}
	if n != len(big) {
		t.Fatalf("got %d bytes, want %d", n, len(big))
	}
}
