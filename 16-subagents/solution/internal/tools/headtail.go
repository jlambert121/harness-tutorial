package tools

import (
	"fmt"
	"sync"
	"unicode/utf8"
)

// HeadTail is an io.Writer that keeps the first and last limit/2 bytes of
// everything written to it, so memory stays bounded however much a command prints.
type HeadTail struct {
	mu      sync.Mutex
	headCap int
	head    []byte
	tail    []byte // ring buffer
	tailCap int
	pos     int // next write position in tail once full
	full    bool
	dropped int64
}

func NewHeadTail(limit int) *HeadTail {
	h := limit / 2
	return &HeadTail{headCap: h, tailCap: limit - h}
}

func (b *HeadTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if room := b.headCap - len(b.head); room > 0 {
		k := min(room, len(p))
		b.head = append(b.head, p[:k]...)
		p = p[k:]
	}
	for len(p) > 0 && b.tailCap > 0 {
		if !b.full {
			k := min(b.tailCap-len(b.tail), len(p))
			b.tail = append(b.tail, p[:k]...)
			p = p[k:]
			b.full = len(b.tail) == b.tailCap
			continue
		}
		k := min(b.tailCap-b.pos, len(p))
		copy(b.tail[b.pos:], p[:k])
		b.pos = (b.pos + k) % b.tailCap
		b.dropped += int64(k)
		p = p[k:]
	}
	return n, nil
}

func (b *HeadTail) Dropped() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

func (b *HeadTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	tail := b.tail
	if b.full {
		tail = append(append([]byte{}, b.tail[b.pos:]...), b.tail[:b.pos]...)
	}
	if b.dropped == 0 {
		return string(b.head) + string(tail)
	}
	head := b.head
	if i := lastRuneStart(head); len(head) > 0 && !utf8.FullRune(head[i:]) {
		head = head[:i]
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return fmt.Sprintf("%s\n… %d bytes omitted …\n%s", head, b.dropped, tail)
}

func lastRuneStart(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if utf8.RuneStart(b[i]) {
			return i
		}
	}
	return 0
}
