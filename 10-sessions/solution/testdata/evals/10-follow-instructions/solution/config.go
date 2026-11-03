package task

import (
	"errors"
	"fmt"
	"strings"
)

// ErrEmpty is returned for empty input.
var ErrEmpty = errors.New("empty input")

// Config is a parsed key=value file.
type Config map[string]string

// ParseConfig parses lines of key=value.
func ParseConfig(s string) (Config, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("config: %w", ErrEmpty)
	}
	c := Config{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("config: line %d: missing '='", i+1)
		}
		c[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return c, nil
}
