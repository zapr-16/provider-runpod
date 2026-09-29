package clients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
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

// TestPingNon2xxReturnsError covers a revoked/invalid key (401/403) and a
// transient failure (5xx): each must surface as an error naming the status,
// so the ProviderConfig is never marked Available on a rejected or broken
// connection.
func TestPingNon2xxReturnsError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			err := NewClient("some-key", WithBaseURL(srv.URL)).Ping(context.Background())
			if err == nil {
				t.Fatal("Ping() error = nil, want non-nil")
			}
			if want := strconv.Itoa(status); !strings.Contains(err.Error(), want) {
				t.Fatalf("Ping() error = %q, want to mention status %s", err.Error(), want)
			}
		})
	}
}

// TestListPods covers ListPods, used only by the pod controller's
// ambiguous-create recovery path (adoptIncompleteCreate) to find a pod
// created under a deterministic name whose result was never confirmed.
func TestListPods(t *testing.T) {
	tests := map[string]struct {
		statusCode int
		body       string
		want       []PodResponse
		wantErr    bool
	}{
		"HappyPathReturnsAllPods": {
			statusCode: http.StatusOK,
			body:       `[{"id":"pod-1","name":"vllm-small-aabbccdd"},{"id":"pod-2","name":"other"}]`,
			want: []PodResponse{
				{ID: "pod-1", Name: "vllm-small-aabbccdd"},
				{ID: "pod-2", Name: "other"},
			},
		},
		"EmptyListReturnsEmptyNoError": {
			statusCode: http.StatusOK,
			body:       `[]`,
			want:       []PodResponse{},
		},
		"NotFoundReturnsEmptyNoError": {
			// A list endpoint has no natural "not found" case; treat 404 the
			// same as an empty list rather than surfacing it as an error.
			statusCode: http.StatusNotFound,
			want:       nil,
		},
		"ServerErrorReturnsError": {
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.WriteHeader(tc.statusCode)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()

			c := NewClient("key", WithBaseURL(srv.URL))
			got, err := c.ListPods(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatal("ListPods() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListPods() error = %v", err)
			}
			if gotMethod != http.MethodGet || gotPath != "/pods" {
				t.Fatalf("got %s %s, want GET /pods", gotMethod, gotPath)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ListPods() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
