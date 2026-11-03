package task

import "time"

// Limiter is a token bucket; see docs/RATE_LIMIT.md.
type Limiter struct {
	capacity  float64
	perSecond float64
	tokens    float64
	last      time.Time
}

// NewLimiter returns a full bucket.
func NewLimiter(capacity int, perSecond float64, start time.Time) *Limiter {
	return &Limiter{capacity: float64(capacity), perSecond: perSecond, tokens: float64(capacity), last: start}
}

// Allow reports whether one token could be taken at time now.
func (l *Limiter) Allow(now time.Time) bool {
	if now.After(l.last) {
		l.tokens = min(l.capacity, l.tokens+now.Sub(l.last).Seconds()*l.perSecond)
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}
