package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateContainerRegistryAuth(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cra-1","name":"ghcr"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	id, err := c.CreateContainerRegistryAuth(context.Background(), CreateContainerRegistryAuthRequest{
		Name:     "ghcr",
		Username: "my-user",
		Password: "my-pass",
	})
	if err != nil {
		t.Fatalf("CreateContainerRegistryAuth: %v", err)
	}
	if id != "cra-1" {
		t.Fatalf("CreateContainerRegistryAuth id = %q, want %q", id, "cra-1")
	}
	if gotMethod != http.MethodPost || gotPath != "/containerregistryauth" {
		t.Fatalf("got %s %s, want POST /containerregistryauth", gotMethod, gotPath)
	}
	if gotBody["name"] != "ghcr" || gotBody["username"] != "my-user" || gotBody["password"] != "my-pass" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}
}

func TestGetContainerRegistryAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containerregistryauth/cra-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cra-1", "name": "ghcr",
		})
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	out, found, err := c.GetContainerRegistryAuth(context.Background(), "cra-1")
	if err != nil {
		t.Fatalf("GetContainerRegistryAuth: %v", err)
	}
	if !found {
		t.Fatal("GetContainerRegistryAuth found = false, want true")
	}
	if out.ID != "cra-1" || out.Name != "ghcr" {
		t.Fatalf("GetContainerRegistryAuth response = %#v", out)
	}
}

func TestGetContainerRegistryAuthNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	out, found, err := c.GetContainerRegistryAuth(context.Background(), "cra-1")
	if err != nil {
		t.Fatalf("GetContainerRegistryAuth: %v", err)
	}
	if found || out != nil {
		t.Fatalf("GetContainerRegistryAuth = %#v, %v, want not found", out, found)
	}
}

func TestGetContainerRegistryAuthServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	if _, _, err := c.GetContainerRegistryAuth(context.Background(), "cra-1"); err == nil {
		t.Fatal("GetContainerRegistryAuth want error on 500")
	}
}

func TestGetContainerRegistryAuthRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if _, _, err := c.GetContainerRegistryAuth(context.Background(), "../evil"); err == nil {
		t.Fatal("want error for invalid container registry auth ID")
	}
}

func TestDeleteContainerRegistryAuth(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{"NoContent", http.StatusNoContent, false},
		{"NotFoundIsSuccess", http.StatusNotFound, false},
		{"GoneIsSuccess", http.StatusGone, false},
		{"ServerErrorIsError", http.StatusInternalServerError, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			c := NewClient("key", WithBaseURL(srv.URL))
			err := c.DeleteContainerRegistryAuth(context.Background(), "cra-1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("DeleteContainerRegistryAuth want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("DeleteContainerRegistryAuth: %v", err)
			}
			if gotMethod != http.MethodDelete || gotPath != "/containerregistryauth/cra-1" {
				t.Fatalf("got %s %s, want DELETE /containerregistryauth/cra-1", gotMethod, gotPath)
			}
		})
	}
}

func TestDeleteContainerRegistryAuthRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.DeleteContainerRegistryAuth(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid container registry auth ID")
	}
}
