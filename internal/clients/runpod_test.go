package clients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPingValidCredentialsReturnsNil covers the happy path of the cheap
// authenticated call used to detect revoked/invalid credentials: any 2xx
// response from GET /pods means the key still works.
func TestPingValidCredentialsReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/pods" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization header = %q, want Bearer test-key", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient("test-key", WithBaseURL(srv.URL))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v, want nil", err)
	}
}

// TestPingUnauthorizedReturnsError covers a revoked/invalid key: the
// current validateCredentials only checks the secret is non-empty, so a
// revoked key would still be reported Available without this check.
func TestPingUnauthorizedReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	c := NewClient("revoked-key", WithBaseURL(srv.URL))
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("Ping() error = %q, want to mention status 401", err.Error())
	}
}

// TestPingForbiddenReturnsError covers the other credential-rejection
// status the RunPod API can return.
func TestPingForbiddenReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient("forbidden-key", WithBaseURL(srv.URL))
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping() error = nil, want non-nil")
	}
}

// TestPingServerErrorReturnsError covers a transient failure (5xx): it must
// still be surfaced as an error (so the ProviderConfig is not marked
// Available on a broken connection), even though it is not necessarily an
// invalid-credentials condition.
func TestPingServerErrorReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient("some-key", WithBaseURL(srv.URL))
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping() error = nil, want non-nil")
	}
}
