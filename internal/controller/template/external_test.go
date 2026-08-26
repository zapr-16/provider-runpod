package template

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

func ptrString(s string) *string { return &s }
func ptrInt32(v int32) *int32    { return &v }
func ptrBool(b bool) *bool       { return &b }

func readyTemplateResponse() *runpodclient.TemplateResponse {
	return &runpodclient.TemplateResponse{
		ID:                "template-123",
		Name:              "vllm-base",
		ImageName:         "runpod/worker-v1-vllm:v2.25.2",
		IsServerless:      true,
		Env:               map[string]string{"MODEL_NAME": "Qwen"},
		ContainerDiskInGb: 30,
	}
}

func matchingTemplateSpec() v1alpha1.TemplateParameters {
	name := "vllm-base"
	return v1alpha1.TemplateParameters{
		Name:              &name,
		ImageName:         "runpod/worker-v1-vllm:v2.25.2",
		IsServerless:      ptrBool(true),
		Env:               []v1alpha1.EnvVar{{Name: "MODEL_NAME", Value: "Qwen"}},
		ContainerDiskInGb: ptrInt32(30),
	}
}

func TestObserve(t *testing.T) {
	type want struct {
		exists     bool
		upToDate   bool
		id         string
		name       string
		connection managed.ConnectionDetails
	}

	tests := map[string]struct {
		externalName string
		spec         func() v1alpha1.TemplateParameters
		statusCode   int
		response     *runpodclient.TemplateResponse
		wantCalls    int
		wantErr      bool
		want         want
	}{
		"EmptyExternalName": {
			spec: matchingTemplateSpec,
			want: want{exists: false},
		},
		"NotFoundTreatsTemplateAsMissing": {
			externalName: "template-123",
			spec:         matchingTemplateSpec,
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want:         want{exists: false},
		},
		"ServerErrorReturnsError": {
			externalName: "template-123",
			spec:         matchingTemplateSpec,
			statusCode:   http.StatusInternalServerError,
			wantCalls:    1,
			wantErr:      true,
		},
		"InvalidExternalNameReturnsError": {
			externalName: "template-123/../../v1/pods/victim",
			spec:         matchingTemplateSpec,
			wantCalls:    0,
			wantErr:      true,
		},
		"MatchingSpecIsUpToDateAndAvailable": {
			externalName: "template-123",
			spec:         matchingTemplateSpec,
			statusCode:   http.StatusOK,
			response:     readyTemplateResponse(),
			wantCalls:    1,
			want: want{
				exists:     true,
				upToDate:   true,
				id:         "template-123",
				name:       "vllm-base",
				connection: managed.ConnectionDetails{"templateId": []byte("template-123")},
			},
		},
		"ImageNameDriftMarksNotUpToDate": {
			externalName: "template-123",
			spec: func() v1alpha1.TemplateParameters {
				s := matchingTemplateSpec()
				s.ImageName = "runpod/worker-v1-vllm:v2.30.0"
				return s
			},
			statusCode: http.StatusOK,
			response:   readyTemplateResponse(),
			wantCalls:  1,
			want: want{
				exists:     true,
				upToDate:   false,
				id:         "template-123",
				name:       "vllm-base",
				connection: managed.ConnectionDetails{"templateId": []byte("template-123")},
			},
		},
		"EnvDriftMarksNotUpToDate": {
			externalName: "template-123",
			spec: func() v1alpha1.TemplateParameters {
				s := matchingTemplateSpec()
				s.Env = []v1alpha1.EnvVar{{Name: "MODEL_NAME", Value: "different"}}
				return s
			},
			statusCode: http.StatusOK,
			response:   readyTemplateResponse(),
			wantCalls:  1,
			want: want{
				exists:     true,
				upToDate:   false,
				id:         "template-123",
				name:       "vllm-base",
				connection: managed.ConnectionDetails{"templateId": []byte("template-123")},
			},
		},
		"NameDriftMarksNotUpToDate": {
			externalName: "template-123",
			spec: func() v1alpha1.TemplateParameters {
				s := matchingTemplateSpec()
				s.Name = ptrString("renamed")
				return s
			},
			statusCode: http.StatusOK,
			response:   readyTemplateResponse(), // name "vllm-base" (i.e. "old")
			wantCalls:  1,
			want: want{
				exists:     true,
				upToDate:   false,
				id:         "template-123",
				name:       "vllm-base",
				connection: managed.ConnectionDetails{"templateId": []byte("template-123")},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				if r.URL.Path != "/templates/"+tc.externalName {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if tc.statusCode != 0 {
					w.WriteHeader(tc.statusCode)
				}
				if tc.response != nil {
					if err := json.NewEncoder(w).Encode(tc.response); err != nil {
						t.Fatalf("encode response: %v", err)
					}
				}
			}))
			defer server.Close()

			tmpl := &v1alpha1.Template{
				Spec: v1alpha1.TemplateSpec{ForProvider: tc.spec()},
			}
			if tc.externalName != "" {
				meta.SetExternalName(tmpl, tc.externalName)
			}

			e := &external{client: newTestClient(t, server), log: logr.Discard()}

			got, err := e.Observe(context.Background(), tmpl)
			if tc.wantErr {
				if err == nil {
					t.Fatal("Observe() error = nil, want non-nil")
				}
				if calls != tc.wantCalls {
					t.Fatalf("Observe() HTTP calls = %d, want %d", calls, tc.wantCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Observe() error = %v", err)
			}
			if calls != tc.wantCalls {
				t.Fatalf("Observe() HTTP calls = %d, want %d", calls, tc.wantCalls)
			}
			if got.ResourceExists != tc.want.exists {
				t.Fatalf("Observe() ResourceExists = %v, want %v", got.ResourceExists, tc.want.exists)
			}
			if !tc.want.exists {
				return
			}
			if got.ResourceUpToDate != tc.want.upToDate {
				t.Fatalf("Observe() ResourceUpToDate = %v, want %v", got.ResourceUpToDate, tc.want.upToDate)
			}
			ready := tmpl.GetCondition(xpv2.TypeReady)
			if tc.want.upToDate && ready.Status != "True" {
				t.Fatalf("Observe() Ready status = %v, want True", ready.Status)
			}
			at := tmpl.Status.AtProvider
			if at.TemplateID != tc.want.id {
				t.Fatalf("Observe() TemplateID = %q, want %q", at.TemplateID, tc.want.id)
			}
			if at.Name != tc.want.name {
				t.Fatalf("Observe() Name = %q, want %q", at.Name, tc.want.name)
			}
			if !reflect.DeepEqual(got.ConnectionDetails, tc.want.connection) {
				t.Fatalf("Observe() ConnectionDetails = %#v, want %#v", got.ConnectionDetails, tc.want.connection)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("PostsFullPayloadAndSetsExternalName", func(t *testing.T) {
		var gotBody runpodclient.CreateTemplateRequest
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "template-created"})
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-base"},
			Spec: v1alpha1.TemplateSpec{ForProvider: v1alpha1.TemplateParameters{
				ImageName:         "runpod/worker-v1-vllm:v2.25.2",
				IsServerless:      ptrBool(true),
				Env:               []v1alpha1.EnvVar{{Name: "MODEL_NAME", Value: "Qwen"}},
				ContainerDiskInGb: ptrInt32(30),
			}},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Create(context.Background(), tmpl)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if gotPath != "/templates" {
			t.Fatalf("Create() path = %q, want /templates", gotPath)
		}
		if gotBody.Name != "vllm-base" {
			t.Fatalf("Create() name = %q, want %q (defaulted from metadata.name)", gotBody.Name, "vllm-base")
		}
		if gotBody.ImageName != "runpod/worker-v1-vllm:v2.25.2" {
			t.Fatalf("Create() imageName = %q", gotBody.ImageName)
		}
		if !gotBody.IsServerless {
			t.Fatal("Create() isServerless = false, want true")
		}
		if gotBody.Env["MODEL_NAME"] != "Qwen" {
			t.Fatalf("Create() env = %#v", gotBody.Env)
		}
		if gotBody.ContainerDiskInGb == nil || *gotBody.ContainerDiskInGb != 30 {
			t.Fatalf("Create() containerDiskInGb = %#v", gotBody.ContainerDiskInGb)
		}
		if meta.GetExternalName(tmpl) != "template-created" {
			t.Fatalf("Create() external name = %q, want %q", meta.GetExternalName(tmpl), "template-created")
		}
		wantDetails := managed.ConnectionDetails{"templateId": []byte("template-created")}
		if !reflect.DeepEqual(got.ConnectionDetails, wantDetails) {
			t.Fatalf("Create() connection details = %#v, want %#v", got.ConnectionDetails, wantDetails)
		}
	})

	t.Run("UsesSpecNameWhenSet", func(t *testing.T) {
		var gotBody runpodclient.CreateTemplateRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "template-created"})
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{
			ObjectMeta: metav1.ObjectMeta{Name: "resource-name"},
			Spec: v1alpha1.TemplateSpec{ForProvider: v1alpha1.TemplateParameters{
				Name:      ptrString("custom-name"),
				ImageName: "some/image:latest",
			}},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Create(context.Background(), tmpl); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if gotBody.Name != "custom-name" {
			t.Fatalf("Create() name = %q, want %q", gotBody.Name, "custom-name")
		}
	})

	t.Run("CreateFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Create(context.Background(), &v1alpha1.Template{})
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errCreateTemplate) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errCreateTemplate)
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("PatchesSpecSetFields", func(t *testing.T) {
		var gotBody runpodclient.UpdateTemplateRequest
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "template-123"})
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{
			Spec: v1alpha1.TemplateSpec{ForProvider: matchingTemplateSpec()},
		}
		meta.SetExternalName(tmpl, "template-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), tmpl); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if gotPath != "/templates/template-123" {
			t.Fatalf("Update() path = %q, want /templates/template-123", gotPath)
		}
		if gotBody.ImageName == nil || *gotBody.ImageName != "runpod/worker-v1-vllm:v2.25.2" {
			t.Fatalf("Update() imageName = %#v", gotBody.ImageName)
		}
		if gotBody.Env["MODEL_NAME"] != "Qwen" {
			t.Fatalf("Update() env = %#v", gotBody.Env)
		}
		if gotBody.ContainerDiskInGb == nil || *gotBody.ContainerDiskInGb != 30 {
			t.Fatalf("Update() containerDiskInGb = %#v", gotBody.ContainerDiskInGb)
		}
	})

	t.Run("PatchesNameWhenSet", func(t *testing.T) {
		// Regression: a post-creation name edit must be PATCHable, or
		// Observe's name-drift check makes ResourceUpToDate=false forever.
		var gotBody runpodclient.UpdateTemplateRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "template-123"})
		}))
		defer server.Close()

		spec := matchingTemplateSpec()
		spec.Name = ptrString("renamed")
		tmpl := &v1alpha1.Template{
			Spec: v1alpha1.TemplateSpec{ForProvider: spec},
		}
		meta.SetExternalName(tmpl, "template-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), tmpl); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if gotBody.Name == nil || *gotBody.Name != "renamed" {
			t.Fatalf("Update() name = %#v, want %q", gotBody.Name, "renamed")
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCalls", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), &v1alpha1.Template{Spec: v1alpha1.TemplateSpec{ForProvider: matchingTemplateSpec()}}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Update() HTTP calls = %d, want 0", calls)
		}
	})

	t.Run("PatchFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{Spec: v1alpha1.TemplateSpec{ForProvider: matchingTemplateSpec()}}
		meta.SetExternalName(tmpl, "template-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Update(context.Background(), tmpl)
		if err == nil {
			t.Fatal("Update() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errUpdateTemplate) {
			t.Fatalf("Update() error = %q, want wrapped %q", err.Error(), errUpdateTemplate)
		}
	})
}

func TestDelete(t *testing.T) {
	t.Run("DeletesTemplate", func(t *testing.T) {
		var gotMethod, gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{}
		meta.SetExternalName(tmpl, "template-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), tmpl); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if gotMethod != http.MethodDelete || gotPath != "/templates/template-123" {
			t.Fatalf("Delete() = %s %s, want DELETE /templates/template-123", gotMethod, gotPath)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCalls", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), &v1alpha1.Template{}); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Delete() HTTP calls = %d, want 0", calls)
		}
	})

	t.Run("NotFoundDeleteIsSuccess", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{}
		meta.SetExternalName(tmpl, "template-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), tmpl); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("ServerErrorReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		tmpl := &v1alpha1.Template{}
		meta.SetExternalName(tmpl, "template-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Delete(context.Background(), tmpl)
		if err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errDeleteTemplate) {
			t.Fatalf("Delete() error = %q, want wrapped %q", err.Error(), errDeleteTemplate)
		}
	})
}

func newTestClient(t *testing.T, server *httptest.Server) *runpodclient.Client {
	t.Helper()

	return runpodclient.NewClient("test-key",
		runpodclient.WithBaseURL(server.URL),
		runpodclient.WithHTTPClient(server.Client()),
	)
}
