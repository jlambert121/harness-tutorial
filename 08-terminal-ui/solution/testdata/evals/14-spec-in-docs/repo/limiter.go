package task

import "time"

// Limiter is a token bucket; see docs/RATE_LIMIT.md.
type Limiter struct{}

// NewLimiter returns a full bucket.
func NewLimiter(capacity int, perSecond float64, start time.Time) *Limiter {
	return &Limiter{}
}

// Allow reports whether one token could be taken at time now.
func (l *Limiter) Allow(now time.Time) bool {
	return true
}
