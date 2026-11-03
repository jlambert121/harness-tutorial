package llm

// CostFromUsage prices a response from catalog rates. Usage.Input includes
// cached and cache-written tokens, so those are subtracted before applying the
// normal input rate. Cache writes without a listed price bill at the input rate.
func CostFromUsage(u Usage, p Pricing) float64 {
	uncached := u.Input - u.CacheRead - u.CacheWrite
	if uncached < 0 {
		uncached = 0
	}
	write := p.CacheWrite
	if write == 0 {
		write = p.Input
	}
	read := p.CacheRead
	if read == 0 {
		read = p.Input
	}
	return float64(uncached)*p.Input + float64(u.CacheRead)*read + float64(u.CacheWrite)*write + float64(u.Output)*p.Output
}
