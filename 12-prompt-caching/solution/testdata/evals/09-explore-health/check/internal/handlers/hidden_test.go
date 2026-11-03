package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHiddenHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	Health(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("Health = %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	Version(rec, httptest.NewRequest("GET", "/version", nil))
	if rec.Code != http.StatusOK {
		t.Error("Version broke")
	}
}
