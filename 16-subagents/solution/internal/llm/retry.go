package llm

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

type retryPolicy struct {
	max          int
	initial, cap time.Duration
}

// Defaults follow opencode: 2s initial, doubling, 25% jitter, 30s cap, 5 retries.
var defaultRetry = retryPolicy{max: 5, initial: 2 * time.Second, cap: 30 * time.Second}

func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

// backoff returns the wait before retry number attempt (0-based). A server
// Retry-After header wins over the computed delay.
func (p retryPolicy) backoff(attempt int, h http.Header) time.Duration {
	if h != nil {
		if ms, err := strconv.Atoi(h.Get("Retry-After-Ms")); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
		if v := h.Get("Retry-After"); v != "" {
			if s, err := strconv.Atoi(v); err == nil && s >= 0 {
				return time.Duration(s) * time.Second
			}
			if t, err := http.ParseTime(v); err == nil {
				if d := time.Until(t); d > 0 {
					return d
				}
				return 0
			}
		}
	}
	d := p.initial << attempt
	if d > p.cap || d <= 0 {
		d = p.cap
	}
	jitter := 1 + (rand.Float64()*2-1)*0.25
	return min(time.Duration(float64(d)*jitter), p.cap)
}
