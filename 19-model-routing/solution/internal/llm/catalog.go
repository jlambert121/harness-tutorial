package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Pricing is USD per token. The gateway sends prices as decimal strings.
type Pricing struct {
	Input, Output, CacheRead, CacheWrite float64
}

type ModelInfo struct {
	ID            string
	Name          string
	Type          string
	ContextWindow int
	MaxTokens     int
	Tags          []string
	Pricing       Pricing
	// Eligibility is "eligible", "ineligible", or "" when the catalog was fetched anonymously.
	Eligibility      string
	IneligibleReason string
}

func (m ModelInfo) Has(tag string) bool { return slices.Contains(m.Tags, tag) }

type wireModel struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Type          string           `json:"type"`
	ContextWindow int              `json:"context_window"`
	MaxTokens     int              `json:"max_tokens"`
	Tags          []string         `json:"tags"`
	Pricing       map[string]any   `json:"pricing"`
	Eligibility   *wireEligibility `json:"model_eligibility"`
}

type wireEligibility struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func ParseCatalog(body []byte) ([]ModelInfo, error) {
	var resp struct {
		Data []wireModel `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	out := make([]ModelInfo, 0, len(resp.Data))
	for _, w := range resp.Data {
		m := ModelInfo{
			ID: w.ID, Name: w.Name, Type: w.Type,
			ContextWindow: w.ContextWindow, MaxTokens: w.MaxTokens, Tags: w.Tags,
		}
		var err error
		for key, dst := range map[string]*float64{
			"input": &m.Pricing.Input, "output": &m.Pricing.Output,
			"input_cache_read": &m.Pricing.CacheRead, "input_cache_write": &m.Pricing.CacheWrite,
		} {
			if *dst, err = price(w.Pricing[key]); err != nil {
				return nil, fmt.Errorf("model %s pricing.%s: %w", w.ID, key, err)
			}
		}
		if w.Eligibility != nil {
			m.Eligibility, m.IneligibleReason = w.Eligibility.Status, w.Eligibility.Reason
		}
		out = append(out, m)
	}
	return out, nil
}

func price(v any) (float64, error) {
	switch p := v.(type) {
	case nil:
		return 0, nil
	case string:
		return strconv.ParseFloat(p, 64)
	case float64:
		return p, nil
	default:
		return 0, fmt.Errorf("unexpected price type %T", v)
	}
}

// FetchCatalog lists gateway models. With an apiKey it also asks for per-team
// availability, which reflects the team's model allowlist.
func FetchCatalog(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, error) {
	url := strings.TrimRight(baseURL, "/") + "/models"
	if apiKey != "" {
		url += "?include_availability=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return ParseCatalog(body)
}
