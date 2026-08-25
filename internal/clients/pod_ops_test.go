package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdatePod(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"pod-1"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	image := "new/image:tag"
	err := c.UpdatePod(context.Background(), "pod-1", UpdatePodRequest{
		ImageName: &image,
		Env:       map[string]string{"A": "1"},
	})
	if err != nil {
		t.Fatalf("UpdatePod: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/pods/pod-1" {
		t.Fatalf("got %s %s, want PATCH /pods/pod-1", gotMethod, gotPath)
	}
	if gotBody["imageName"] != "new/image:tag" {
		t.Fatalf("imageName not sent: %v", gotBody)
	}
}

func TestUpdatePodRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.UpdatePod(context.Background(), "../evil", UpdatePodRequest{}); err == nil {
		t.Fatal("want error for invalid pod ID")
	}
}

func TestUpdatePodErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	defer srv.Close()
	c := NewClient("key", WithBaseURL(srv.URL))
	if err := c.UpdatePod(context.Background(), "pod-1", UpdatePodRequest{}); err == nil {
		t.Fatal("want error on 400")
	}
}

func TestStartStopPod(t *testing.T) {
	cases := []struct {
		name string
		call func(*Client) error
		path string
	}{
		{"start", func(c *Client) error { return c.StartPod(context.Background(), "pod-1") }, "/pods/pod-1/start"},
		{"stop", func(c *Client) error { return c.StopPod(context.Background(), "pod-1") }, "/pods/pod-1/stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"pod-1"}`))
			}))
			defer srv.Close()
			c := NewClient("key", WithBaseURL(srv.URL))
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != http.MethodPost || gotPath != tc.path {
				t.Fatalf("got %s %s, want POST %s", gotMethod, gotPath, tc.path)
			}
		})
	}
}

func TestStartPodRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.StartPod(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid pod ID")
	}
	if err := c.StopPod(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid pod ID")
	}
}
