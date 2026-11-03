package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func loadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCatalog(t *testing.T) {
	models, err := ParseCatalog(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
	m := models[0]
	if m.ID != "anthropic/claude-sonnet-5.5" {
		t.Errorf("ID = %q", m.ID)
	}
	if m.ContextWindow != 1000000 || m.MaxTokens != 128000 {
		t.Errorf("ContextWindow=%d MaxTokens=%d", m.ContextWindow, m.MaxTokens)
	}
	if m.Pricing.Input != 0.000002 || m.Pricing.Output != 0.00001 {
		t.Errorf("Pricing = %+v", m.Pricing)
	}
	if m.Pricing.CacheRead != 0.0000001 || m.Pricing.CacheWrite != 0.0000025 {
		t.Errorf("cache pricing = %+v", m.Pricing)
	}
	if !m.Has("tool-use") || m.Has("open-weights") {
		t.Errorf("Has() wrong for tags %v", m.Tags)
	}
	if m.Eligibility != "eligible" {
		t.Errorf("Eligibility = %q, want eligible", m.Eligibility)
	}
	if models[2].Eligibility != "ineligible" {
		t.Errorf("third model Eligibility = %q, want ineligible", models[2].Eligibility)
	}
}

func TestParseCatalogMissingPricing(t *testing.T) {
	models, err := ParseCatalog([]byte(`{"data":[{"id":"x/y","type":"language","pricing":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if models[0].Pricing.Input != 0 || models[0].Eligibility != "" {
		t.Errorf("got %+v", models[0])
	}
}

func TestParseCatalogBadPrice(t *testing.T) {
	_, err := ParseCatalog([]byte(`{"data":[{"id":"x/y","pricing":{"input":"abc"}}]}`))
	if err == nil {
		t.Fatal("want error for non-numeric price")
	}
}

func TestFetchCatalogSendsAuthOnlyWithKey(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Write(loadFixture(t))
	}))
	defer srv.Close()

	models, err := FetchCatalog(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || gotAuth != "" || gotQuery != "" {
		t.Errorf("anonymous: n=%d auth=%q query=%q", len(models), gotAuth, gotQuery)
	}

	if _, err := FetchCatalog(context.Background(), srv.URL, "k123"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k123" || gotQuery != "include_availability=true" {
		t.Errorf("authenticated: auth=%q query=%q", gotAuth, gotQuery)
	}
}

func TestFetchCatalogHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"nope"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := FetchCatalog(context.Background(), srv.URL, "bad"); err == nil {
		t.Fatal("want error on 401")
	}
}
