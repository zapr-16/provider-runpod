package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdateEndpointRequestMarshalsZeroWorkersMax(t *testing.T) {
	// A pointer to 0 must survive omitempty (only a nil pointer is omitted),
	// so the workersMax:0 PATCH used to cycle workers during a template
	// change actually reaches the RunPod API.
	zero := int32(0)
	body, err := json.Marshal(UpdateEndpointRequest{WorkersMax: &zero})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := string(body); !containsWorkersMaxZero(got) {
		t.Fatalf("Marshal() = %s, want it to contain \"workersMax\":0", got)
	}
}

func containsWorkersMaxZero(body string) bool {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return false
	}
	v, ok := decoded["workersMax"]
	if !ok {
		return false
	}
	f, ok := v.(float64)
	return ok && f == 0
}

func TestCreateTemplateFullPayload(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"template-1"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	authID := "auth-1"
	volumeInGb := int32(20)
	volumeMountPath := "/workspace"
	id, err := c.CreateTemplate(context.Background(), CreateTemplateRequest{
		Name:                    "vllm-base",
		ImageName:               "runpod/worker-v1-vllm:v2.25.2",
		IsServerless:            true,
		Env:                     map[string]string{"MODEL_NAME": "Qwen"},
		ContainerDiskInGb:       ptrInt32(30),
		DockerStartCmd:          []string{"python3", "handler.py"},
		DockerEntrypoint:        []string{"/bin/sh", "-c"},
		ContainerRegistryAuthID: &authID,
		Ports:                   []string{"8080/http"},
		VolumeInGb:              &volumeInGb,
		VolumeMountPath:         &volumeMountPath,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if id != "template-1" {
		t.Fatalf("CreateTemplate id = %q, want %q", id, "template-1")
	}
	if gotMethod != http.MethodPost || gotPath != "/templates" {
		t.Fatalf("got %s %s, want POST /templates", gotMethod, gotPath)
	}

	if got, _ := gotBody["dockerStartCmd"].([]any); len(got) != 2 || got[0] != "python3" || got[1] != "handler.py" {
		t.Errorf("dockerStartCmd not sent: %v", gotBody["dockerStartCmd"])
	}
	if got, _ := gotBody["dockerEntrypoint"].([]any); len(got) != 2 || got[0] != "/bin/sh" || got[1] != "-c" {
		t.Errorf("dockerEntrypoint not sent: %v", gotBody["dockerEntrypoint"])
	}
	if gotBody["containerRegistryAuthId"] != "auth-1" {
		t.Errorf("containerRegistryAuthId not sent: %v", gotBody["containerRegistryAuthId"])
	}
	if got, _ := gotBody["ports"].([]any); len(got) != 1 || got[0] != "8080/http" {
		t.Errorf("ports not sent: %v", gotBody["ports"])
	}
	if gotBody["volumeInGb"] != float64(20) {
		t.Errorf("volumeInGb not sent: %v", gotBody["volumeInGb"])
	}
	if gotBody["volumeMountPath"] != "/workspace" {
		t.Errorf("volumeMountPath not sent: %v", gotBody["volumeMountPath"])
	}
}

func TestUpdateTemplateFullPayload(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"template-1"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	authID := "auth-1"
	volumeInGb := int32(20)
	volumeMountPath := "/workspace"
	newName := "renamed"
	err := c.UpdateTemplate(context.Background(), "template-1", UpdateTemplateRequest{
		Name:                    &newName,
		DockerStartCmd:          []string{"python3", "handler.py"},
		DockerEntrypoint:        []string{"/bin/sh", "-c"},
		ContainerRegistryAuthID: &authID,
		Ports:                   []string{"8080/http"},
		VolumeInGb:              &volumeInGb,
		VolumeMountPath:         &volumeMountPath,
	})
	if err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}

	if gotBody["name"] != "renamed" {
		t.Errorf("name not sent: %v", gotBody["name"])
	}
	if got, _ := gotBody["dockerStartCmd"].([]any); len(got) != 2 || got[0] != "python3" {
		t.Errorf("dockerStartCmd not sent: %v", gotBody["dockerStartCmd"])
	}
	if got, _ := gotBody["dockerEntrypoint"].([]any); len(got) != 2 || got[0] != "/bin/sh" {
		t.Errorf("dockerEntrypoint not sent: %v", gotBody["dockerEntrypoint"])
	}
	if gotBody["containerRegistryAuthId"] != "auth-1" {
		t.Errorf("containerRegistryAuthId not sent: %v", gotBody["containerRegistryAuthId"])
	}
	if got, _ := gotBody["ports"].([]any); len(got) != 1 || got[0] != "8080/http" {
		t.Errorf("ports not sent: %v", gotBody["ports"])
	}
	if gotBody["volumeInGb"] != float64(20) {
		t.Errorf("volumeInGb not sent: %v", gotBody["volumeInGb"])
	}
	if gotBody["volumeMountPath"] != "/workspace" {
		t.Errorf("volumeMountPath not sent: %v", gotBody["volumeMountPath"])
	}
}

func TestGetTemplateFullResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/templates/template-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                      "template-1",
			"name":                    "vllm-base",
			"imageName":               "runpod/worker-v1-vllm:v2.25.2",
			"isServerless":            true,
			"env":                     map[string]string{"MODEL_NAME": "Qwen"},
			"containerDiskInGb":       30,
			"dockerStartCmd":          []string{"python3", "handler.py"},
			"dockerEntrypoint":        []string{"/bin/sh", "-c"},
			"containerRegistryAuthId": "auth-1",
			"ports":                   []string{"8080/http"},
			"volumeInGb":              20,
			"volumeMountPath":         "/workspace",
		})
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	out, found, err := c.GetTemplate(context.Background(), "template-1")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if !found {
		t.Fatal("GetTemplate found = false, want true")
	}
	if len(out.DockerStartCmd) != 2 || out.DockerStartCmd[0] != "python3" {
		t.Errorf("DockerStartCmd = %#v", out.DockerStartCmd)
	}
	if len(out.DockerEntrypoint) != 2 || out.DockerEntrypoint[0] != "/bin/sh" {
		t.Errorf("DockerEntrypoint = %#v", out.DockerEntrypoint)
	}
	if out.ContainerRegistryAuthID != "auth-1" {
		t.Errorf("ContainerRegistryAuthID = %q, want %q", out.ContainerRegistryAuthID, "auth-1")
	}
	if len(out.Ports) != 1 || out.Ports[0] != "8080/http" {
		t.Errorf("Ports = %#v", out.Ports)
	}
	if out.VolumeInGb != 20 {
		t.Errorf("VolumeInGb = %d, want 20", out.VolumeInGb)
	}
	if out.VolumeMountPath != "/workspace" {
		t.Errorf("VolumeMountPath = %q, want %q", out.VolumeMountPath, "/workspace")
	}
}

func TestGetTemplateNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	out, found, err := c.GetTemplate(context.Background(), "template-1")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if found || out != nil {
		t.Fatalf("GetTemplate = %#v, %v, want not found", out, found)
	}
}

func TestGetTemplateRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if _, _, err := c.GetTemplate(context.Background(), "../evil"); err == nil {
		t.Fatal("want error for invalid template ID")
	}
}

func TestUpdateTemplateRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.UpdateTemplate(context.Background(), "../evil", UpdateTemplateRequest{}); err == nil {
		t.Fatal("want error for invalid template ID")
	}
}

func TestDeleteTemplate(t *testing.T) {
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
			err := c.DeleteTemplate(context.Background(), "template-1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("DeleteTemplate want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("DeleteTemplate: %v", err)
			}
			if gotMethod != http.MethodDelete || gotPath != "/templates/template-1" {
				t.Fatalf("got %s %s, want DELETE /templates/template-1", gotMethod, gotPath)
			}
		})
	}
}

func TestDeleteTemplateRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.DeleteTemplate(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid template ID")
	}
}

func ptrInt32(v int32) *int32 { return &v }
