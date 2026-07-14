package endpoint

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

func readyResponse() *runpodclient.EndpointResponse {
	return &runpodclient.EndpointResponse{
		ID:          "ep-123",
		Name:        "vllm-small",
		TemplateID:  "tpl-xyz",
		GPUTypeIDs:  []string{"NVIDIA GeForce RTX 3090"},
		GPUCount:    1,
		WorkersMin:  0,
		WorkersMax:  2,
		IdleTimeout: 60,
		FlashBoot:   true,
		ScalerType:  "QUEUE_DELAY",
		ScalerValue: 4,
		Workers: []runpodclient.EndpointWorker{
			{ID: "w1", DesiredStatus: "RUNNING"},
			{ID: "w2", DesiredStatus: "IDLE"},
		},
	}
}

func templateResponse() *runpodclient.TemplateResponse {
	return &runpodclient.TemplateResponse{
		ID:                "tpl-xyz",
		ImageName:         "runpod/worker-v1-vllm:stable",
		IsServerless:      true,
		Env:               map[string]string{"MODEL_NAME": "Qwen/Qwen2.5-Coder-7B-Instruct"},
		ContainerDiskInGb: 30,
	}
}

func matchingSpec() v1alpha1.EndpointParameters {
	scaler := v1alpha1.ScalerTypeQueueDelay
	return v1alpha1.EndpointParameters{
		ImageName:         "runpod/worker-v1-vllm:stable",
		Env:               []v1alpha1.EnvVar{{Name: "MODEL_NAME", Value: "Qwen/Qwen2.5-Coder-7B-Instruct"}},
		ContainerDiskInGb: ptrInt32(30),
		GPUTypeIDs:        []string{"NVIDIA GeForce RTX 3090"},
		GPUCount:          ptrInt32(1),
		WorkersMin:        ptrInt32(0),
		WorkersMax:        ptrInt32(2),
		IdleTimeout:       ptrInt32(60),
		FlashBoot:         ptrBool(true),
		ScalerType:        &scaler,
		ScalerValue:       ptrInt32(4),
	}
}

func wantConnection() managed.ConnectionDetails {
	return managed.ConnectionDetails{
		"endpointId": []byte("ep-123"),
		"endpoint":   []byte("https://api.runpod.ai/v2/ep-123"),
		"openaiUrl":  []byte("https://api.runpod.ai/v2/ep-123/openai/v1"),
	}
}

func TestObserve(t *testing.T) {
	type want struct {
		exists       bool
		upToDate     bool
		readyStatus  corev1.ConditionStatus
		endpointID   string
		templateID   string
		runtime      string
		openAI       string
		workersReady int32
		workersTotal int32
		connection   managed.ConnectionDetails
	}

	tests := map[string]struct {
		externalName string
		spec         func() v1alpha1.EndpointParameters
		statusCode   int
		response     *runpodclient.EndpointResponse
		wantCalls    int
		want         want
	}{
		"EmptyExternalName": {
			spec: matchingSpec,
			want: want{exists: false},
		},
		"Non2xxTreatsEndpointAsMissing": {
			externalName: "ep-123",
			spec:         matchingSpec,
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want:         want{exists: false},
		},
		"MatchingSpecIsUpToDateAndAvailable": {
			// Endpoint matches → template drift check adds a GET /templates call.
			externalName: "ep-123",
			spec:         matchingSpec,
			statusCode:   http.StatusOK,
			response:     readyResponse(),
			wantCalls:    2,
			want: want{
				exists:       true,
				upToDate:     true,
				readyStatus:  corev1.ConditionTrue,
				endpointID:   "ep-123",
				templateID:   "tpl-xyz",
				runtime:      "https://api.runpod.ai/v2/ep-123",
				openAI:       "https://api.runpod.ai/v2/ep-123/openai/v1",
				workersReady: 1,
				workersTotal: 2,
				connection:   wantConnection(),
			},
		},
		"ZeroWorkersIsStillAvailable": {
			// Scale-to-zero: no workers is the normal idle state.
			externalName: "ep-123",
			spec:         matchingSpec,
			statusCode:   http.StatusOK,
			response: func() *runpodclient.EndpointResponse {
				r := readyResponse()
				r.Workers = nil
				return r
			}(),
			wantCalls: 2,
			want: want{
				exists:       true,
				upToDate:     true,
				readyStatus:  corev1.ConditionTrue,
				endpointID:   "ep-123",
				templateID:   "tpl-xyz",
				runtime:      "https://api.runpod.ai/v2/ep-123",
				openAI:       "https://api.runpod.ai/v2/ep-123/openai/v1",
				workersReady: 0,
				workersTotal: 0,
				connection:   wantConnection(),
			},
		},
		"WorkersMaxDriftMarksNotUpToDate": {
			// Endpoint-level drift short-circuits the template GET.
			externalName: "ep-123",
			spec: func() v1alpha1.EndpointParameters {
				s := matchingSpec()
				s.WorkersMax = ptrInt32(5)
				return s
			},
			statusCode: http.StatusOK,
			response:   readyResponse(),
			wantCalls:  1,
			want: want{
				exists:       true,
				upToDate:     false,
				readyStatus:  corev1.ConditionTrue,
				endpointID:   "ep-123",
				templateID:   "tpl-xyz",
				runtime:      "https://api.runpod.ai/v2/ep-123",
				openAI:       "https://api.runpod.ai/v2/ep-123/openai/v1",
				workersReady: 1,
				workersTotal: 2,
				connection:   wantConnection(),
			},
		},
		"TemplateImageDriftMarksNotUpToDate": {
			externalName: "ep-123",
			spec: func() v1alpha1.EndpointParameters {
				s := matchingSpec()
				s.ImageName = "runpod/worker-v1-vllm:dev"
				return s
			},
			statusCode: http.StatusOK,
			response:   readyResponse(),
			wantCalls:  2,
			want: want{
				exists:       true,
				upToDate:     false,
				readyStatus:  corev1.ConditionTrue,
				endpointID:   "ep-123",
				templateID:   "tpl-xyz",
				runtime:      "https://api.runpod.ai/v2/ep-123",
				openAI:       "https://api.runpod.ai/v2/ep-123/openai/v1",
				workersReady: 1,
				workersTotal: 2,
				connection:   wantConnection(),
			},
		},
		"NilOptionalFieldsDoNotDrift": {
			externalName: "ep-123",
			spec: func() v1alpha1.EndpointParameters {
				return v1alpha1.EndpointParameters{ImageName: "runpod/worker-v1-vllm:stable"}
			},
			statusCode: http.StatusOK,
			response:   readyResponse(),
			wantCalls:  2,
			want: want{
				exists:       true,
				upToDate:     true,
				readyStatus:  corev1.ConditionTrue,
				endpointID:   "ep-123",
				templateID:   "tpl-xyz",
				runtime:      "https://api.runpod.ai/v2/ep-123",
				openAI:       "https://api.runpod.ai/v2/ep-123/openai/v1",
				workersReady: 1,
				workersTotal: 2,
				connection:   wantConnection(),
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
				switch r.URL.Path {
				case "/endpoints/" + tc.externalName:
					if got := r.URL.Query().Get("includeWorkers"); got != "true" {
						t.Fatalf("unexpected includeWorkers query: %q", got)
					}
					if tc.statusCode != 0 {
						w.WriteHeader(tc.statusCode)
					}
					if tc.response != nil {
						if err := json.NewEncoder(w).Encode(tc.response); err != nil {
							t.Fatalf("encode response: %v", err)
						}
					}
				case "/templates/tpl-xyz":
					if err := json.NewEncoder(w).Encode(templateResponse()); err != nil {
						t.Fatalf("encode template response: %v", err)
					}
				default:
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			ep := &v1alpha1.Endpoint{
				Spec: v1alpha1.EndpointSpec{ForProvider: tc.spec()},
			}
			if tc.externalName != "" {
				meta.SetExternalName(ep, tc.externalName)
			}

			e := &external{client: newTestClient(t, server), log: logr.Discard()}

			got, err := e.Observe(context.Background(), ep)
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
			ready := ep.GetCondition(xpv1.TypeReady)
			if ready.Status != tc.want.readyStatus {
				t.Fatalf("Observe() Ready status = %v, want %v", ready.Status, tc.want.readyStatus)
			}
			at := ep.Status.AtProvider
			if at.EndpointID != tc.want.endpointID {
				t.Fatalf("Observe() EndpointID = %q, want %q", at.EndpointID, tc.want.endpointID)
			}
			if at.TemplateID != tc.want.templateID {
				t.Fatalf("Observe() TemplateID = %q, want %q", at.TemplateID, tc.want.templateID)
			}
			if at.RuntimeEndpoint != tc.want.runtime {
				t.Fatalf("Observe() RuntimeEndpoint = %q, want %q", at.RuntimeEndpoint, tc.want.runtime)
			}
			if at.OpenAIBaseURL != tc.want.openAI {
				t.Fatalf("Observe() OpenAIBaseURL = %q, want %q", at.OpenAIBaseURL, tc.want.openAI)
			}
			if at.WorkersReady != tc.want.workersReady {
				t.Fatalf("Observe() WorkersReady = %d, want %d", at.WorkersReady, tc.want.workersReady)
			}
			if at.WorkersTotal != tc.want.workersTotal {
				t.Fatalf("Observe() WorkersTotal = %d, want %d", at.WorkersTotal, tc.want.workersTotal)
			}
			if !reflect.DeepEqual(got.ConnectionDetails, tc.want.connection) {
				t.Fatalf("Observe() ConnectionDetails = %#v, want %#v", got.ConnectionDetails, tc.want.connection)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("HappyPathCreatesTemplateThenEndpoint", func(t *testing.T) {
		var gotTemplate runpodclient.CreateTemplateRequest
		var gotEndpoint runpodclient.CreateEndpointRequest
		var order []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("unexpected authorization header: %q", got)
			}
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/templates":
				order = append(order, "template")
				if err := json.NewDecoder(r.Body).Decode(&gotTemplate); err != nil {
					t.Fatalf("decode template request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-created"})
			case r.Method == http.MethodPost && r.URL.Path == "/endpoints":
				order = append(order, "endpoint")
				if err := json.NewDecoder(r.Body).Decode(&gotEndpoint); err != nil {
					t.Fatalf("decode endpoint request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-created"})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: matchingSpec()},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Create(context.Background(), ep)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		if !reflect.DeepEqual(order, []string{"template", "endpoint"}) {
			t.Fatalf("Create() call order = %v", order)
		}
		if meta.GetExternalName(ep) != "ep-created" {
			t.Fatalf("Create() external name = %q, want %q", meta.GetExternalName(ep), "ep-created")
		}
		if gotTemplate.Name != "vllm-small" || gotTemplate.ImageName != "runpod/worker-v1-vllm:stable" {
			t.Fatalf("Create() template request = %#v", gotTemplate)
		}
		if !gotTemplate.IsServerless {
			t.Fatal("Create() template request isServerless = false, want true")
		}
		if !reflect.DeepEqual(gotTemplate.Env, map[string]string{"MODEL_NAME": "Qwen/Qwen2.5-Coder-7B-Instruct"}) {
			t.Fatalf("Create() template env = %#v", gotTemplate.Env)
		}
		if gotEndpoint.TemplateID != "tpl-created" {
			t.Fatalf("Create() endpoint templateId = %q, want %q", gotEndpoint.TemplateID, "tpl-created")
		}
		if gotEndpoint.Name == nil || *gotEndpoint.Name != "vllm-small" {
			t.Fatalf("Create() endpoint name = %#v, want %q", gotEndpoint.Name, "vllm-small")
		}
		if !reflect.DeepEqual(gotEndpoint.GPUTypeIDs, []string{"NVIDIA GeForce RTX 3090"}) {
			t.Fatalf("Create() endpoint gpuTypeIds = %#v", gotEndpoint.GPUTypeIDs)
		}
		if gotEndpoint.WorkersMin == nil || *gotEndpoint.WorkersMin != 0 {
			t.Fatalf("Create() endpoint workersMin = %#v, want 0", gotEndpoint.WorkersMin)
		}
		if gotEndpoint.WorkersMax == nil || *gotEndpoint.WorkersMax != 2 {
			t.Fatalf("Create() endpoint workersMax = %#v, want 2", gotEndpoint.WorkersMax)
		}
		if gotEndpoint.ScalerType == nil || *gotEndpoint.ScalerType != "QUEUE_DELAY" {
			t.Fatalf("Create() endpoint scalerType = %#v", gotEndpoint.ScalerType)
		}
		wantDetails := managed.ConnectionDetails{
			"endpointId": []byte("ep-created"),
			"endpoint":   []byte("https://api.runpod.ai/v2/ep-created"),
			"openaiUrl":  []byte("https://api.runpod.ai/v2/ep-created/openai/v1"),
		}
		if !reflect.DeepEqual(got.ConnectionDetails, wantDetails) {
			t.Fatalf("Create() connection details = %#v, want %#v", got.ConnectionDetails, wantDetails)
		}
	})

	t.Run("TemplateCreateFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Create(context.Background(), &v1alpha1.Endpoint{})
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errCreateTemplate) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errCreateTemplate)
		}
	})

	t.Run("EndpointCreateFailureCleansUpTemplate", func(t *testing.T) {
		var templateDeleted bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/templates":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-orphan"})
			case r.Method == http.MethodPost && r.URL.Path == "/endpoints":
				w.WriteHeader(http.StatusBadRequest)
			case r.Method == http.MethodDelete && r.URL.Path == "/templates/tpl-orphan":
				templateDeleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Create(context.Background(), &v1alpha1.Endpoint{ObjectMeta: metav1.ObjectMeta{Name: "x"}})
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errCreateEndpoint) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errCreateEndpoint)
		}
		if !templateDeleted {
			t.Fatal("Create() did not clean up template after endpoint create failure")
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("PatchesEndpointAndTemplate", func(t *testing.T) {
		var gotEndpoint runpodclient.UpdateEndpointRequest
		var gotTemplate runpodclient.UpdateTemplateRequest
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("unexpected method: %s %s", r.Method, r.URL.Path)
			}
			paths = append(paths, r.URL.Path)
			switch r.URL.Path {
			case "/endpoints/ep-123":
				if err := json.NewDecoder(r.Body).Decode(&gotEndpoint); err != nil {
					t.Fatalf("decode endpoint patch: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case "/templates/tpl-xyz":
				if err := json.NewDecoder(r.Body).Decode(&gotTemplate); err != nil {
					t.Fatalf("decode template patch: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: matchingSpec()},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		if !reflect.DeepEqual(paths, []string{"/endpoints/ep-123", "/templates/tpl-xyz"}) {
			t.Fatalf("Update() patched paths = %v", paths)
		}
		if gotEndpoint.WorkersMax == nil || *gotEndpoint.WorkersMax != 2 {
			t.Fatalf("Update() endpoint workersMax = %#v, want 2", gotEndpoint.WorkersMax)
		}
		if gotEndpoint.IdleTimeout == nil || *gotEndpoint.IdleTimeout != 60 {
			t.Fatalf("Update() endpoint idleTimeout = %#v, want 60", gotEndpoint.IdleTimeout)
		}
		if gotTemplate.ImageName == nil || *gotTemplate.ImageName != "runpod/worker-v1-vllm:stable" {
			t.Fatalf("Update() template imageName = %#v", gotTemplate.ImageName)
		}
	})

	t.Run("MissingTemplateIDFallsBackToGet", func(t *testing.T) {
		var templatePatched bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/endpoints/ep-123":
				_ = json.NewEncoder(w).Encode(readyResponse())
			case r.Method == http.MethodPatch && r.URL.Path == "/templates/tpl-xyz":
				templatePatched = true
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: matchingSpec()},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if !templatePatched {
			t.Fatal("Update() did not patch template after GET fallback")
		}
	})

	t.Run("EndpointPatchFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{Spec: v1alpha1.EndpointSpec{ForProvider: matchingSpec()}}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Update(context.Background(), ep)
		if err == nil {
			t.Fatal("Update() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errUpdateEndpoint) {
			t.Fatalf("Update() error = %q, want wrapped %q", err.Error(), errUpdateEndpoint)
		}
	})
}

func TestDelete(t *testing.T) {
	t.Run("DeletesEndpointThenTemplate", func(t *testing.T) {
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected method: %s %s", r.Method, r.URL.Path)
			}
			paths = append(paths, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), ep); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if !reflect.DeepEqual(paths, []string{"/endpoints/ep-123", "/templates/tpl-xyz"}) {
			t.Fatalf("Delete() paths = %v", paths)
		}
	})

	t.Run("ResolvesTemplateIDViaGetWhenStatusEmpty", func(t *testing.T) {
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.Method+" "+r.URL.Path)
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(readyResponse())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), ep); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		want := []string{"GET /endpoints/ep-123", "DELETE /endpoints/ep-123", "DELETE /templates/tpl-xyz"}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("Delete() calls = %v, want %v", paths, want)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCalls", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), &v1alpha1.Endpoint{}); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Delete() HTTP calls = %d, want 0", calls)
		}
	})
}

func TestHasEndpointDrift(t *testing.T) {
	tests := map[string]struct {
		mutate func(*v1alpha1.EndpointParameters)
		want   bool
	}{
		"MatchingSpecDoesNotDrift": {
			mutate: func(_ *v1alpha1.EndpointParameters) {},
			want:   false,
		},
		"WorkersMinDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.WorkersMin = ptrInt32(1) },
			want:   true,
		},
		"IdleTimeoutDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.IdleTimeout = ptrInt32(120) },
			want:   true,
		},
		"ScalerTypeDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) {
				st := v1alpha1.ScalerTypeRequestCount
				s.ScalerType = &st
			},
			want: true,
		},
		"GPUTypeOrderDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) {
				s.GPUTypeIDs = []string{"NVIDIA L4", "NVIDIA GeForce RTX 3090"}
			},
			want: true,
		},
		"NilFieldsDoNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) {
				*s = v1alpha1.EndpointParameters{ImageName: s.ImageName}
			},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			spec := matchingSpec()
			tc.mutate(&spec)
			if got := hasEndpointDrift(spec, readyResponse()); got != tc.want {
				t.Fatalf("hasEndpointDrift() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasTemplateDrift(t *testing.T) {
	tests := map[string]struct {
		mutate func(*v1alpha1.EndpointParameters)
		want   bool
	}{
		"MatchingTemplateDoesNotDrift": {
			mutate: func(_ *v1alpha1.EndpointParameters) {},
			want:   false,
		},
		"ImageDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.ImageName = "other:latest" },
			want:   true,
		},
		"EnvDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) {
				s.Env = []v1alpha1.EnvVar{{Name: "MODEL_NAME", Value: "other/model"}}
			},
			want: true,
		},
		"ContainerDiskDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.ContainerDiskInGb = ptrInt32(50) },
			want:   true,
		},
		"NilEnvAndDiskDoNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) {
				s.Env = nil
				s.ContainerDiskInGb = nil
			},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			spec := matchingSpec()
			tc.mutate(&spec)
			if got := hasTemplateDrift(spec, *templateResponse()); got != tc.want {
				t.Fatalf("hasTemplateDrift() = %v, want %v", got, tc.want)
			}
		})
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *runpodclient.Client {
	t.Helper()

	c := runpodclient.NewClient("test-key")
	setUnexportedField(t, c, "baseURL", server.URL)
	setUnexportedField(t, c, "httpClient", server.Client())
	return c
}

func ptrInt32(v int32) *int32 { return &v }

func ptrBool(b bool) *bool { return &b }

func setUnexportedField(t *testing.T, target any, name string, value any) {
	t.Helper()

	v := reflect.ValueOf(target).Elem().FieldByName(name)
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}
