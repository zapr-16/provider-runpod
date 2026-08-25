package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateNetworkVolume(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"nv-1","name":"model-cache","size":50,"dataCenterId":"EU-RO-1"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	id, err := c.CreateNetworkVolume(context.Background(), CreateNetworkVolumeRequest{
		Name:         "model-cache",
		Size:         50,
		DataCenterID: "EU-RO-1",
	})
	if err != nil {
		t.Fatalf("CreateNetworkVolume: %v", err)
	}
	if id != "nv-1" {
		t.Fatalf("CreateNetworkVolume id = %q, want %q", id, "nv-1")
	}
	if gotMethod != http.MethodPost || gotPath != "/networkvolumes" {
		t.Fatalf("got %s %s, want POST /networkvolumes", gotMethod, gotPath)
	}
	if gotBody["name"] != "model-cache" || gotBody["dataCenterId"] != "EU-RO-1" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}
}

func TestGetNetworkVolume(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/networkvolumes/nv-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "nv-1", "name": "model-cache", "size": 50, "dataCenterId": "EU-RO-1",
		})
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	out, found, err := c.GetNetworkVolume(context.Background(), "nv-1")
	if err != nil {
		t.Fatalf("GetNetworkVolume: %v", err)
	}
	if !found {
		t.Fatal("GetNetworkVolume found = false, want true")
	}
	if out.ID != "nv-1" || out.Name != "model-cache" || out.Size != 50 || out.DataCenterID != "EU-RO-1" {
		t.Fatalf("GetNetworkVolume response = %#v", out)
	}
}

func TestGetNetworkVolumeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	out, found, err := c.GetNetworkVolume(context.Background(), "nv-1")
	if err != nil {
		t.Fatalf("GetNetworkVolume: %v", err)
	}
	if found || out != nil {
		t.Fatalf("GetNetworkVolume = %#v, %v, want not found", out, found)
	}
}

func TestGetNetworkVolumeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	if _, _, err := c.GetNetworkVolume(context.Background(), "nv-1"); err == nil {
		t.Fatal("GetNetworkVolume want error on 500")
	}
}

func TestGetNetworkVolumeRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if _, _, err := c.GetNetworkVolume(context.Background(), "../evil"); err == nil {
		t.Fatal("want error for invalid network volume ID")
	}
}

func TestUpdateNetworkVolume(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"nv-1"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	size := int32(100)
	err := c.UpdateNetworkVolume(context.Background(), "nv-1", UpdateNetworkVolumeRequest{Size: &size})
	if err != nil {
		t.Fatalf("UpdateNetworkVolume: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/networkvolumes/nv-1" {
		t.Fatalf("got %s %s, want PATCH /networkvolumes/nv-1", gotMethod, gotPath)
	}
	if gotBody["size"] != float64(100) {
		t.Fatalf("size not sent: %v", gotBody)
	}
}

func TestUpdateNetworkVolumeRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.UpdateNetworkVolume(context.Background(), "../evil", UpdateNetworkVolumeRequest{}); err == nil {
		t.Fatal("want error for invalid network volume ID")
	}
}

func TestUpdateNetworkVolumeErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	defer srv.Close()
	c := NewClient("key", WithBaseURL(srv.URL))
	if err := c.UpdateNetworkVolume(context.Background(), "nv-1", UpdateNetworkVolumeRequest{}); err == nil {
		t.Fatal("want error on 400")
	}
}

func TestDeleteNetworkVolume(t *testing.T) {
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
			err := c.DeleteNetworkVolume(context.Background(), "nv-1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("DeleteNetworkVolume want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("DeleteNetworkVolume: %v", err)
			}
			if gotMethod != http.MethodDelete || gotPath != "/networkvolumes/nv-1" {
				t.Fatalf("got %s %s, want DELETE /networkvolumes/nv-1", gotMethod, gotPath)
			}
		})
	}
}

func TestDeleteNetworkVolumeRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.DeleteNetworkVolume(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid network volume ID")
	}
}
