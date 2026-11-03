# Rate limiter

`Limiter` is a token bucket.

- `NewLimiter(capacity int, perSecond float64, start time.Time)` returns a bucket that is **full** at `start`.
- Tokens refill continuously at `perSecond` tokens per second, including fractions (after 250ms at 2/s, half a token has accrued). The bucket never holds more than `capacity` tokens.
- `Allow(now time.Time) bool` refills based on the time elapsed since the previous call (or since `start`), then takes one whole token if at least one is available and returns true; otherwise it takes nothing and returns false.
- If `now` is earlier than the previous call's time (clock went backwards), no tokens are added and the previous time is kept.
- The limiter does not read the system clock; callers always pass the time.
