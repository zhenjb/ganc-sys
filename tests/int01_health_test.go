package tests

import (
	"net/http"
	"testing"
)

func TestINT01HealthEndpoint(t *testing.T) {
	server := newTestServer()

	rec := performRequest(t, server, http.MethodGet, "/api/health", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["status"] != "ok" {
		t.Fatalf("expected status=ok, got %q", body["status"])
	}

	if body["service"] == "" {
		t.Fatalf("expected service field to be non-empty")
	}
}
