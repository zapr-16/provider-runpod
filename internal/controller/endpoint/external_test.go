package endpoint

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
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
		ID:                      "tpl-xyz",
		ImageName:               "runpod/worker-v1-vllm:stable",
		IsServerless:            true,
		Env:                     map[string]string{"MODEL_NAME": "Qwen/Qwen2.5-Coder-7B-Instruct"},
		ContainerDiskInGb:       30,
		DockerStartCmd:          []string{"python", "run.py"},
		DockerEntrypoint:        []string{"/bin/sh"},
		ContainerRegistryAuthID: "auth-1",
	}
}

func matchingSpec() v1alpha1.EndpointParameters {
	scaler := v1alpha1.ScalerTypeQueueDelay
	return v1alpha1.EndpointParameters{
		ImageName:               ptrString("runpod/worker-v1-vllm:stable"),
		Env:                     []v1alpha1.EnvVar{{Name: "MODEL_NAME", Value: "Qwen/Qwen2.5-Coder-7B-Instruct"}},
		ContainerDiskInGb:       ptrInt32(30),
		GPUTypeIDs:              []string{"NVIDIA GeForce RTX 3090"},
		GPUCount:                ptrInt32(1),
		WorkersMin:              ptrInt32(0),
		WorkersMax:              ptrInt32(2),
		IdleTimeout:             ptrInt32(60),
		FlashBoot:               ptrBool(true),
		ScalerType:              &scaler,
		ScalerValue:             ptrInt32(4),
		DockerStartCmd:          []string{"python", "run.py"},
		DockerEntrypoint:        []string{"/bin/sh"},
		ContainerRegistryAuthID: ptrString("auth-1"),
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
		externalName       string
		spec               func() v1alpha1.EndpointParameters
		statusCode         int
		templateStatusCode int
		response           *runpodclient.EndpointResponse
		wantCalls          int
		wantErr            bool
		want               want
	}{
		"EmptyExternalName": {
			spec: matchingSpec,
			want: want{exists: false},
		},
		"NotFoundTreatsEndpointAsMissing": {
			externalName: "ep-123",
			spec:         matchingSpec,
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want:         want{exists: false},
		},
		"ServerErrorReturnsError": {
			// A transient 5xx must NOT look like a missing endpoint: the
			// reconciler would create a duplicate endpoint+template and
			// orphan the originals.
			externalName: "ep-123",
			spec:         matchingSpec,
			statusCode:   http.StatusInternalServerError,
			wantCalls:    1,
			wantErr:      true,
		},
		"RateLimitedReturnsError": {
			externalName: "ep-123",
			spec:         matchingSpec,
			statusCode:   http.StatusTooManyRequests,
			wantCalls:    1,
			wantErr:      true,
		},
		"InvalidExternalNameReturnsError": {
			externalName: "ep-123/../../v1/pods/victim",
			spec:         matchingSpec,
			wantCalls:    0,
			wantErr:      true,
		},
		"TemplateServerErrorReturnsError": {
			// Template drift check failures must surface, not silently
			// report the endpoint as up to date.
			externalName:       "ep-123",
			spec:               matchingSpec,
			statusCode:         http.StatusOK,
			templateStatusCode: http.StatusInternalServerError,
			response:           readyResponse(),
			wantCalls:          2,
			wantErr:            true,
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
				s.ImageName = ptrString("runpod/worker-v1-vllm:dev")
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
				return v1alpha1.EndpointParameters{ImageName: ptrString("runpod/worker-v1-vllm:stable")}
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
		// Scenario 2: templateId mode skips the implicit-template drift GET
		// entirely; drift is just observed.templateId vs spec.templateId.
		"TemplateModeMatchingTemplateIDIsUpToDate": {
			externalName: "ep-123",
			spec: func() v1alpha1.EndpointParameters {
				return v1alpha1.EndpointParameters{TemplateID: ptrString("tpl-xyz")}
			},
			statusCode: http.StatusOK,
			response:   readyResponse(),
			wantCalls:  1,
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
		"TemplateModeMismatchedTemplateIDIsNotUpToDate": {
			externalName: "ep-123",
			spec: func() v1alpha1.EndpointParameters {
				return v1alpha1.EndpointParameters{TemplateID: ptrString("tpl-new")}
			},
			statusCode: http.StatusOK,
			response: func() *runpodclient.EndpointResponse {
				r := readyResponse()
				r.TemplateID = "tpl-old"
				return r
			}(),
			wantCalls: 1,
			want: want{
				exists:       true,
				upToDate:     false,
				readyStatus:  corev1.ConditionTrue,
				endpointID:   "ep-123",
				templateID:   "tpl-old",
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
					t.Fatalf("unexpected method: %q", r.Method)
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
					if tc.templateStatusCode != 0 {
						w.WriteHeader(tc.templateStatusCode)
						return
					}
					if err := json.NewEncoder(w).Encode(templateResponse()); err != nil {
						t.Fatalf("encode template response: %v", err)
					}
				default:
					t.Fatalf("unexpected path: %q", r.URL.Path)
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
				t.Errorf("Observe() ResourceUpToDate = %v, want %v", got.ResourceUpToDate, tc.want.upToDate)
			}
			ready := ep.GetCondition(xpv2.TypeReady)
			if ready.Status != tc.want.readyStatus {
				t.Errorf("Observe() Ready status = %v, want %v", ready.Status, tc.want.readyStatus)
			}
			at := ep.Status.AtProvider
			if at.EndpointID != tc.want.endpointID {
				t.Errorf("Observe() EndpointID = %q, want %q", at.EndpointID, tc.want.endpointID)
			}
			if at.TemplateID != tc.want.templateID {
				t.Errorf("Observe() TemplateID = %q, want %q", at.TemplateID, tc.want.templateID)
			}
			if at.RuntimeEndpoint != tc.want.runtime {
				t.Errorf("Observe() RuntimeEndpoint = %q, want %q", at.RuntimeEndpoint, tc.want.runtime)
			}
			if at.OpenAIBaseURL != tc.want.openAI {
				t.Errorf("Observe() OpenAIBaseURL = %q, want %q", at.OpenAIBaseURL, tc.want.openAI)
			}
			if at.WorkersReady != tc.want.workersReady {
				t.Errorf("Observe() WorkersReady = %d, want %d", at.WorkersReady, tc.want.workersReady)
			}
			if at.WorkersTotal != tc.want.workersTotal {
				t.Errorf("Observe() WorkersTotal = %d, want %d", at.WorkersTotal, tc.want.workersTotal)
			}
			if !reflect.DeepEqual(got.ConnectionDetails, tc.want.connection) {
				t.Errorf("Observe() ConnectionDetails = %#v, want %#v", got.ConnectionDetails, tc.want.connection)
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
				t.Fatalf("unexpected request: %q %q", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		spec := matchingSpec()
		spec.ComputeType = ptrString("CPU")
		spec.VCPUCount = ptrInt32(4)
		spec.CPUFlavorIDs = []string{"cpu3c"}
		spec.AllowedCudaVersions = []string{"12.1"}
		spec.MinCudaVersion = ptrString("11.8")
		spec.NetworkVolumeIDs = []string{"nv-1", "nv-2"}
		spec.DockerStartCmd = []string{"python", "run.py"}
		spec.DockerEntrypoint = []string{"/bin/sh"}
		spec.ContainerRegistryAuthID = ptrString("auth-1")

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Create(context.Background(), ep)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		if !reflect.DeepEqual(order, []string{"template", "endpoint"}) {
			t.Errorf("Create() call order = %v", order)
		}
		if meta.GetExternalName(ep) != "ep-created" {
			t.Errorf("Create() external name = %q, want %q", meta.GetExternalName(ep), "ep-created")
		}
		if gotTemplate.Name != "vllm-small" || gotTemplate.ImageName != "runpod/worker-v1-vllm:stable" {
			t.Errorf("Create() template request = %#v", gotTemplate)
		}
		if !gotTemplate.IsServerless {
			t.Error("Create() template request isServerless = false, want true")
		}
		if !reflect.DeepEqual(gotTemplate.Env, map[string]string{"MODEL_NAME": "Qwen/Qwen2.5-Coder-7B-Instruct"}) {
			t.Errorf("Create() template env = %#v", gotTemplate.Env)
		}
		if gotEndpoint.TemplateID != "tpl-created" {
			t.Errorf("Create() endpoint templateId = %q, want %q", gotEndpoint.TemplateID, "tpl-created")
		}
		if gotEndpoint.Name == nil || *gotEndpoint.Name != "vllm-small" {
			t.Errorf("Create() endpoint name = %#v, want %q", gotEndpoint.Name, "vllm-small")
		}
		if !reflect.DeepEqual(gotEndpoint.GPUTypeIDs, []string{"NVIDIA GeForce RTX 3090"}) {
			t.Errorf("Create() endpoint gpuTypeIds = %#v", gotEndpoint.GPUTypeIDs)
		}
		if gotEndpoint.WorkersMin == nil || *gotEndpoint.WorkersMin != 0 {
			t.Errorf("Create() endpoint workersMin = %#v, want 0", gotEndpoint.WorkersMin)
		}
		if gotEndpoint.WorkersMax == nil || *gotEndpoint.WorkersMax != 2 {
			t.Errorf("Create() endpoint workersMax = %#v, want 2", gotEndpoint.WorkersMax)
		}
		if gotEndpoint.ScalerType == nil || *gotEndpoint.ScalerType != "QUEUE_DELAY" {
			t.Errorf("Create() endpoint scalerType = %#v", gotEndpoint.ScalerType)
		}
		if gotEndpoint.ComputeType == nil || *gotEndpoint.ComputeType != "CPU" {
			t.Errorf("Create() endpoint computeType = %#v, want %q", gotEndpoint.ComputeType, "CPU")
		}
		if gotEndpoint.VCPUCount == nil || *gotEndpoint.VCPUCount != 4 {
			t.Errorf("Create() endpoint vcpuCount = %#v, want 4", gotEndpoint.VCPUCount)
		}
		if !reflect.DeepEqual(gotEndpoint.CPUFlavorIDs, []string{"cpu3c"}) {
			t.Errorf("Create() endpoint cpuFlavorIds = %#v", gotEndpoint.CPUFlavorIDs)
		}
		if !reflect.DeepEqual(gotEndpoint.AllowedCudaVersions, []string{"12.1"}) {
			t.Errorf("Create() endpoint allowedCudaVersions = %#v", gotEndpoint.AllowedCudaVersions)
		}
		if gotEndpoint.MinCudaVersion == nil || *gotEndpoint.MinCudaVersion != "11.8" {
			t.Errorf("Create() endpoint minCudaVersion = %#v", gotEndpoint.MinCudaVersion)
		}
		if !reflect.DeepEqual(gotEndpoint.NetworkVolumeIDs, []string{"nv-1", "nv-2"}) {
			t.Errorf("Create() endpoint networkVolumeIds = %#v", gotEndpoint.NetworkVolumeIDs)
		}
		if !reflect.DeepEqual(gotTemplate.DockerStartCmd, []string{"python", "run.py"}) {
			t.Errorf("Create() template dockerStartCmd = %#v", gotTemplate.DockerStartCmd)
		}
		if !reflect.DeepEqual(gotTemplate.DockerEntrypoint, []string{"/bin/sh"}) {
			t.Errorf("Create() template dockerEntrypoint = %#v", gotTemplate.DockerEntrypoint)
		}
		if gotTemplate.ContainerRegistryAuthID == nil || *gotTemplate.ContainerRegistryAuthID != "auth-1" {
			t.Errorf("Create() template containerRegistryAuthId = %#v", gotTemplate.ContainerRegistryAuthID)
		}
		wantDetails := managed.ConnectionDetails{
			"endpointId": []byte("ep-created"),
			"endpoint":   []byte("https://api.runpod.ai/v2/ep-created"),
			"openaiUrl":  []byte("https://api.runpod.ai/v2/ep-created/openai/v1"),
		}
		if !reflect.DeepEqual(got.ConnectionDetails, wantDetails) {
			t.Errorf("Create() connection details = %#v, want %#v", got.ConnectionDetails, wantDetails)
		}
	})

	t.Run("TemplateCreateFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Create(context.Background(), &v1alpha1.Endpoint{
			Spec: v1alpha1.EndpointSpec{ForProvider: v1alpha1.EndpointParameters{ImageName: ptrString("runpod/worker-v1-vllm:stable")}},
		})
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
				t.Fatalf("unexpected request: %q %q", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Create(context.Background(), &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "x"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: v1alpha1.EndpointParameters{ImageName: ptrString("runpod/worker-v1-vllm:stable")}},
		})
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

	t.Run("TemplateModeSkipsTemplateCreate", func(t *testing.T) {
		// Scenario 1: POST /endpoints carries templateId "tpl-ext" and NO
		// POST /templates happens.
		var gotEndpoint runpodclient.CreateEndpointRequest
		var calls []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			if r.Method == http.MethodPost && r.URL.Path == "/endpoints" {
				if err := json.NewDecoder(r.Body).Decode(&gotEndpoint); err != nil {
					t.Fatalf("decode endpoint request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-created"})
				return
			}
			t.Fatalf("unexpected request: %q %q", r.Method, r.URL.Path)
		}))
		defer server.Close()

		spec := v1alpha1.EndpointParameters{
			TemplateID: ptrString("tpl-ext"),
			WorkersMin: ptrInt32(0),
			WorkersMax: ptrInt32(2),
		}
		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-from-template"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Create(context.Background(), ep)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		want := []string{"POST /endpoints"}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("Create() calls = %v, want %v", calls, want)
		}
		if gotEndpoint.TemplateID != "tpl-ext" {
			t.Fatalf("Create() endpoint templateId = %q, want %q", gotEndpoint.TemplateID, "tpl-ext")
		}
		if meta.GetExternalName(ep) != "ep-created" {
			t.Fatalf("Create() external name = %q, want %q", meta.GetExternalName(ep), "ep-created")
		}
		if got.ConnectionDetails["endpointId"] == nil {
			t.Fatal("Create() connection details missing endpointId")
		}
	})

	t.Run("SendsNameWithUIDSuffixForDeterministicRecovery", func(t *testing.T) {
		var gotEndpoint runpodclient.CreateEndpointRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/endpoints" {
				_ = json.NewDecoder(r.Body).Decode(&gotEndpoint)
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-created"})
				return
			}
			t.Fatalf("unexpected request: %q %q", r.Method, r.URL.Path)
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-from-template", UID: "550e8400-e29b-41d4-a716-446655440000"},
			Spec: v1alpha1.EndpointSpec{ForProvider: v1alpha1.EndpointParameters{
				TemplateID: ptrString("tpl-ext"),
			}},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Create(context.Background(), ep); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		want := "vllm-from-template-550e8400"
		if gotEndpoint.Name == nil || *gotEndpoint.Name != want {
			t.Fatalf("Create() endpoint name = %#v, want %q", gotEndpoint.Name, want)
		}
	})
}

// TestObserveAdoptsIncompleteCreate covers Observe()'s ambiguous-create
// recovery: an empty external-name annotation combined with
// meta.ExternalCreateIncomplete means a prior Create's result was never
// confirmed, so Observe must list endpoints and match on the deterministic
// create name instead of blindly reporting the resource missing (which
// would let the reconciler retry Create and orphan an already-billing
// endpoint).
func TestObserveAdoptsIncompleteCreate(t *testing.T) {
	newEndpoint := func() *v1alpha1.Endpoint {
		return &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small", UID: "550e8400-e29b-41d4-a716-446655440000"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: v1alpha1.EndpointParameters{TemplateID: ptrString("tpl-xyz")}},
		}
	}
	markIncomplete := func(ep *v1alpha1.Endpoint) {
		meta.SetExternalCreatePending(ep, time.Now())
	}
	derivedName := "vllm-small-550e8400"

	t.Run("NoIncompleteCreateSkipsListCall", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Observe(context.Background(), newEndpoint())
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if got.ResourceExists {
			t.Fatal("Observe() ResourceExists = true, want false")
		}
		if calls != 0 {
			t.Fatalf("Observe() made %d HTTP calls, want 0 (happy path never lists)", calls)
		}
	})

	t.Run("ZeroMatchesReportsNotExists", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/endpoints" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`[{"id":"ep-other","name":"unrelated"}]`))
		}))
		defer server.Close()

		ep := newEndpoint()
		markIncomplete(ep)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Observe(context.Background(), ep)
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if got.ResourceExists {
			t.Fatal("Observe() ResourceExists = true, want false")
		}
		if meta.GetExternalName(ep) != "" {
			t.Fatalf("Observe() external-name = %q, want empty", meta.GetExternalName(ep))
		}
	})

	t.Run("SingleMatchAdoptsAndLateInitializes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/endpoints" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode([]runpodclient.EndpointResponse{
				{ID: "ep-recovered", Name: derivedName, TemplateID: "tpl-xyz"},
			})
		}))
		defer server.Close()

		ep := newEndpoint()
		markIncomplete(ep)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Observe(context.Background(), ep)
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if !got.ResourceExists {
			t.Fatal("Observe() ResourceExists = false, want true")
		}
		if !got.ResourceLateInitialized {
			t.Fatal("Observe() ResourceLateInitialized = false, want true (must persist the adopted external-name)")
		}
		if meta.GetExternalName(ep) != "ep-recovered" {
			t.Fatalf("Observe() external-name = %q, want %q", meta.GetExternalName(ep), "ep-recovered")
		}
	})

	t.Run("MultipleMatchesReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]runpodclient.EndpointResponse{
				{ID: "ep-a", Name: derivedName, TemplateID: "tpl-xyz"},
				{ID: "ep-b", Name: derivedName, TemplateID: "tpl-xyz"},
			})
		}))
		defer server.Close()

		ep := newEndpoint()
		markIncomplete(ep)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Observe(context.Background(), ep)
		if err == nil {
			t.Fatal("Observe() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errAmbiguousCreate) {
			t.Fatalf("Observe() error = %q, want it to mention %q", err.Error(), errAmbiguousCreate)
		}
		if meta.GetExternalName(ep) != "" {
			t.Fatalf("Observe() external-name = %q, want empty (must not guess)", meta.GetExternalName(ep))
		}
	})

	t.Run("IdentityMismatchReturnsErrorAndDoesNotAdopt", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]runpodclient.EndpointResponse{
				// Same derived name, but a different template: this must
				// never be silently adopted, even though the name matches
				// exactly.
				{ID: "ep-wrong-template", Name: derivedName, TemplateID: "tpl-other"},
			})
		}))
		defer server.Close()

		ep := newEndpoint()
		markIncomplete(ep)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Observe(context.Background(), ep)
		if err == nil {
			t.Fatal("Observe() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errAmbiguousCreate) {
			t.Fatalf("Observe() error = %q, want it to mention %q", err.Error(), errAmbiguousCreate)
		}
		if meta.GetExternalName(ep) != "" {
			t.Fatalf("Observe() external-name = %q, want empty (must not adopt on identity mismatch)", meta.GetExternalName(ep))
		}
	})
}

// TestHasEndpointDriftIgnoresDerivedNameSuffix confirms that the
// deterministic -uid8 suffix appended to the name sent on create never
// surfaces as drift: hasEndpointDrift never compares against an endpoint's
// name in the first place, so the response's name is free to include the
// suffix (or anything else) without affecting up-to-date evaluation.
func TestHasEndpointDriftIgnoresDerivedNameSuffix(t *testing.T) {
	spec := matchingSpec()
	response := readyResponse()
	response.Name = "vllm-small-550e8400"

	if hasEndpointDrift(spec, response) {
		t.Fatal("hasEndpointDrift() = true, want false: the derived-name suffix must never be reported as drift")
	}
}

func TestUpdate(t *testing.T) {
	t.Run("PatchesEndpointAndTemplateWhenDriftedAndRecyclesWorkers", func(t *testing.T) {
		// Scenario 5: PATCH /endpoints -> GET /templates (drift check) ->
		// PATCH /templates/{id} -> PATCH /endpoints{workersMax:0} ->
		// PATCH /endpoints{workersMax:restore}.
		var gotEndpoint runpodclient.UpdateEndpointRequest
		var gotTemplate runpodclient.UpdateTemplateRequest
		var endpointPatchBodies []runpodclient.UpdateEndpointRequest
		var order []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				order = append(order, "PATCH /endpoints")
				var body runpodclient.UpdateEndpointRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode endpoint patch: %v", err)
				}
				endpointPatchBodies = append(endpointPatchBodies, body)
				if len(endpointPatchBodies) == 1 {
					gotEndpoint = body
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				order = append(order, "GET /templates")
				drifted := templateResponse()
				drifted.ImageName = "runpod/worker-v1-vllm:old"
				_ = json.NewEncoder(w).Encode(drifted)
			case r.Method == http.MethodPatch && r.URL.Path == "/templates/tpl-xyz":
				order = append(order, "PATCH /templates")
				if err := json.NewDecoder(r.Body).Decode(&gotTemplate); err != nil {
					t.Fatalf("decode template patch: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		spec := matchingSpec()
		spec.VCPUCount = ptrInt32(4)
		spec.CPUFlavorIDs = []string{"cpu3c"}
		spec.AllowedCudaVersions = []string{"12.1"}
		spec.MinCudaVersion = ptrString("11.8")
		spec.NetworkVolumeIDs = []string{"nv-1", "nv-2"}
		spec.DockerStartCmd = []string{"python", "run.py"}
		spec.DockerEntrypoint = []string{"/bin/sh"}
		spec.ContainerRegistryAuthID = ptrString("auth-1")

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		wantOrder := []string{"PATCH /endpoints", "GET /templates", "PATCH /templates", "PATCH /endpoints", "PATCH /endpoints"}
		if !reflect.DeepEqual(order, wantOrder) {
			t.Fatalf("Update() call order = %v, want %v", order, wantOrder)
		}
		if len(endpointPatchBodies) != 3 {
			t.Fatalf("Update() endpoint PATCH count = %d, want 3", len(endpointPatchBodies))
		}
		if got := endpointPatchBodies[1].WorkersMax; got == nil || *got != 0 {
			t.Errorf("Update() recycle first PATCH workersMax = %#v, want 0", got)
		}
		if got := endpointPatchBodies[2].WorkersMax; got == nil || *got != 2 {
			t.Errorf("Update() recycle restore PATCH workersMax = %#v, want 2", got)
		}
		if gotEndpoint.WorkersMax == nil || *gotEndpoint.WorkersMax != 2 {
			t.Errorf("Update() endpoint workersMax = %#v, want 2", gotEndpoint.WorkersMax)
		}
		if gotEndpoint.IdleTimeout == nil || *gotEndpoint.IdleTimeout != 60 {
			t.Errorf("Update() endpoint idleTimeout = %#v, want 60", gotEndpoint.IdleTimeout)
		}
		if gotEndpoint.VCPUCount == nil || *gotEndpoint.VCPUCount != 4 {
			t.Errorf("Update() endpoint vcpuCount = %#v, want 4", gotEndpoint.VCPUCount)
		}
		if !reflect.DeepEqual(gotEndpoint.CPUFlavorIDs, []string{"cpu3c"}) {
			t.Errorf("Update() endpoint cpuFlavorIds = %#v", gotEndpoint.CPUFlavorIDs)
		}
		if !reflect.DeepEqual(gotEndpoint.AllowedCudaVersions, []string{"12.1"}) {
			t.Errorf("Update() endpoint allowedCudaVersions = %#v", gotEndpoint.AllowedCudaVersions)
		}
		if gotEndpoint.MinCudaVersion == nil || *gotEndpoint.MinCudaVersion != "11.8" {
			t.Errorf("Update() endpoint minCudaVersion = %#v", gotEndpoint.MinCudaVersion)
		}
		if !reflect.DeepEqual(gotEndpoint.NetworkVolumeIDs, []string{"nv-1", "nv-2"}) {
			t.Errorf("Update() endpoint networkVolumeIds = %#v", gotEndpoint.NetworkVolumeIDs)
		}
		if gotTemplate.ImageName == nil || *gotTemplate.ImageName != "runpod/worker-v1-vllm:stable" {
			t.Errorf("Update() template imageName = %#v", gotTemplate.ImageName)
		}
		if !reflect.DeepEqual(gotTemplate.DockerStartCmd, []string{"python", "run.py"}) {
			t.Errorf("Update() template dockerStartCmd = %#v", gotTemplate.DockerStartCmd)
		}
		if !reflect.DeepEqual(gotTemplate.DockerEntrypoint, []string{"/bin/sh"}) {
			t.Errorf("Update() template dockerEntrypoint = %#v", gotTemplate.DockerEntrypoint)
		}
		if gotTemplate.ContainerRegistryAuthID == nil || *gotTemplate.ContainerRegistryAuthID != "auth-1" {
			t.Errorf("Update() template containerRegistryAuthId = %#v", gotTemplate.ContainerRegistryAuthID)
		}
	})

	t.Run("NilWorkersMaxSkipsRecycleWithoutGetFallback", func(t *testing.T) {
		// Template drifted and recycle is enabled, but spec.workersMax is
		// nil: recycling must be skipped outright, with no GET /endpoints
		// fallback and no workersMax PATCHes of any kind (runpod-final-review
		// item 1 — a GET-fallback could "restore" to whatever workersMax
		// happens to be observed, including 0 if a prior recycle failed
		// mid-cycle).
		var calls []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				drifted := templateResponse()
				drifted.ImageName = "runpod/worker-v1-vllm:old"
				_ = json.NewEncoder(w).Encode(drifted)
			case r.Method == http.MethodPatch && r.URL.Path == "/templates/tpl-xyz":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		spec := matchingSpec()
		spec.WorkersMax = nil

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		want := []string{"PATCH /endpoints/ep-123", "GET /templates/tpl-xyz", "PATCH /templates/tpl-xyz"}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("Update() calls = %v, want %v (no recycle PATCHes, no GET fallback)", calls, want)
		}
	})

	t.Run("RecycleRestoreFailureReturnsError", func(t *testing.T) {
		// The second (restore) workersMax PATCH fails transiently. Update
		// must propagate an error wrapping errRecycleWorkers, and calls must
		// end at the failed restore attempt: the endpoint is left at
		// workersMax:0, but since spec.workersMax is set, the next
		// reconcile's endpoint PATCH will carry it and self-heal.
		var calls []string
		var endpointPatchCount int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				endpointPatchCount++
				if endpointPatchCount == 3 {
					// The restore PATCH (1st = spec patch, 2nd = zero, 3rd = restore).
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				drifted := templateResponse()
				drifted.ImageName = "runpod/worker-v1-vllm:old"
				_ = json.NewEncoder(w).Encode(drifted)
			case r.Method == http.MethodPatch && r.URL.Path == "/templates/tpl-xyz":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		spec := matchingSpec()

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Update(context.Background(), ep)
		if err == nil {
			t.Fatal("Update() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errRecycleWorkers) {
			t.Fatalf("Update() error = %q, want wrapped %q", err.Error(), errRecycleWorkers)
		}

		want := []string{
			"PATCH /endpoints/ep-123",
			"GET /templates/tpl-xyz",
			"PATCH /templates/tpl-xyz",
			"PATCH /endpoints/ep-123",
			"PATCH /endpoints/ep-123",
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("Update() calls = %v, want %v (ending at the failed restore)", calls, want)
		}
	})

	t.Run("RecycleDisabledSkipsWorkersMaxCycling", func(t *testing.T) {
		// Scenario 6: template drifted, but recycleWorkersOnTemplateChange
		// is explicitly false -> no workersMax cycling PATCHes.
		var endpointPatchCount int
		var templatePatched bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				endpointPatchCount++
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				drifted := templateResponse()
				drifted.ImageName = "runpod/worker-v1-vllm:old"
				_ = json.NewEncoder(w).Encode(drifted)
			case r.Method == http.MethodPatch && r.URL.Path == "/templates/tpl-xyz":
				templatePatched = true
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		spec := matchingSpec()
		spec.RecycleWorkersOnTemplateChange = ptrBool(false)

		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-small"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if !templatePatched {
			t.Fatal("Update() did not patch template despite drift")
		}
		if endpointPatchCount != 1 {
			t.Fatalf("Update() endpoint PATCH count = %d, want 1 (no recycle)", endpointPatchCount)
		}
	})

	t.Run("TemplateGetFailureDuringDriftCheckReturnsError", func(t *testing.T) {
		// A transient GET /templates failure during the drift check must
		// propagate as an error (retry semantics), not be silently treated
		// as "no drift" — consistent with Observe's handling of the same
		// call. No template PATCH or recycle PATCHes must fire.
		var calls []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				w.WriteHeader(http.StatusInternalServerError)
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
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
		_, err := e.Update(context.Background(), ep)
		if err == nil {
			t.Fatal("Update() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errGetTemplate) {
			t.Fatalf("Update() error = %q, want wrapped %q", err.Error(), errGetTemplate)
		}

		want := []string{"PATCH /endpoints/ep-123", "GET /templates/tpl-xyz"}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("Update() calls = %v, want %v (no template PATCH or recycle PATCHes)", calls, want)
		}
	})

	t.Run("NoTemplateDriftSkipsTemplatePatchAndRecycle", func(t *testing.T) {
		// Scenario 7: GET /templates shows the template already matches the
		// spec -> no template PATCH, no recycle PATCHes.
		var endpointPatchCount int
		var templatePatched bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123":
				endpointPatchCount++
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				_ = json.NewEncoder(w).Encode(templateResponse())
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
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if templatePatched {
			t.Fatal("Update() patched template despite no drift")
		}
		if endpointPatchCount != 1 {
			t.Fatalf("Update() endpoint PATCH count = %d, want 1 (no recycle)", endpointPatchCount)
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
			case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl-xyz":
				drifted := templateResponse()
				drifted.ImageName = "runpod/worker-v1-vllm:old"
				_ = json.NewEncoder(w).Encode(drifted)
			case r.Method == http.MethodPatch && r.URL.Path == "/templates/tpl-xyz":
				templatePatched = true
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "tpl-xyz"})
			default:
				t.Fatalf("unexpected request: %q %q", r.Method, r.URL.Path)
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

	t.Run("TemplateModeUpdate", func(t *testing.T) {
		// Scenario 3: templateId mode -> PATCH /endpoints carries templateId,
		// no template PATCH/GET, no recycle.
		var gotEndpoint runpodclient.UpdateEndpointRequest
		var calls []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			if r.Method == http.MethodPatch && r.URL.Path == "/endpoints/ep-123" {
				if err := json.NewDecoder(r.Body).Decode(&gotEndpoint); err != nil {
					t.Fatalf("decode endpoint patch: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ep-123"})
				return
			}
			t.Fatalf("unexpected request: %q %q", r.Method, r.URL.Path)
		}))
		defer server.Close()

		spec := v1alpha1.EndpointParameters{
			TemplateID: ptrString("tpl-ext"),
			WorkersMax: ptrInt32(2),
		}
		ep := &v1alpha1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-from-template"},
			Spec:       v1alpha1.EndpointSpec{ForProvider: spec},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-ext"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		want := []string{"PATCH /endpoints/ep-123"}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("Update() calls = %v, want %v", calls, want)
		}
		if gotEndpoint.TemplateID == nil || *gotEndpoint.TemplateID != "tpl-ext" {
			t.Fatalf("Update() endpoint templateId = %#v, want %q", gotEndpoint.TemplateID, "tpl-ext")
		}
	})

	t.Run("InvalidExternalNameReturnsError", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{Spec: v1alpha1.EndpointSpec{ForProvider: matchingSpec()}}
		meta.SetExternalName(ep, "ep-123/../../v1/pods/victim")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), ep); err == nil {
			t.Fatal("Update() error = nil, want non-nil")
		}
		if calls != 0 {
			t.Fatalf("Update() HTTP calls = %d, want 0", calls)
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

	t.Run("NotFoundDeleteIsSuccess", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
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
	})

	t.Run("EndpointDeleteServerErrorReturnsError", func(t *testing.T) {
		// A failed endpoint DELETE must be retried, not swallowed — the
		// endpoint would keep running and billing with the CR gone.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Delete(context.Background(), ep)
		if err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errDeleteEndpoint) {
			t.Fatalf("Delete() error = %q, want wrapped %q", err.Error(), errDeleteEndpoint)
		}
	})

	t.Run("TemplateDeleteFailureIsTolerated", func(t *testing.T) {
		// A leaked template costs nothing; endpoint deletion must not be
		// blocked on template cleanup.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/templates/") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
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
	})

	t.Run("InvalidExternalNameReturnsError", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-xyz"},
			},
		}
		meta.SetExternalName(ep, "ep-123/../../v1/pods/victim")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), ep); err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if calls != 0 {
			t.Fatalf("Delete() HTTP calls = %d, want 0", calls)
		}
	})

	t.Run("TemplateModeNeverDeletesReferencedTemplate", func(t *testing.T) {
		// Scenario 4: DELETE /endpoints only — the referenced template MUST
		// NOT be deleted, even though it is recorded in atProvider.
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		ep := &v1alpha1.Endpoint{
			Spec: v1alpha1.EndpointSpec{
				ForProvider: v1alpha1.EndpointParameters{TemplateID: ptrString("tpl-ext")},
			},
			Status: v1alpha1.EndpointStatus{
				AtProvider: v1alpha1.EndpointObservation{TemplateID: "tpl-ext"},
			},
		}
		meta.SetExternalName(ep, "ep-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), ep); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		want := []string{"DELETE /endpoints/ep-123"}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("Delete() calls = %v, want %v", paths, want)
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
		"EmptyGPUTypeIDsDoNotDrift": {
			// Empty and nil both mean "unmanaged": the PATCH payload uses
			// omitempty, so an empty list could never be reconciled anyway.
			mutate: func(s *v1alpha1.EndpointParameters) { s.GPUTypeIDs = []string{} },
			want:   false,
		},
		"ComputeTypeDoesNotDrift": {
			// Write-only: the observation never echoes computeType.
			mutate: func(s *v1alpha1.EndpointParameters) { s.ComputeType = ptrString("CPU") },
			want:   false,
		},
		"VCPUCountDoesNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.VCPUCount = ptrInt32(4) },
			want:   false,
		},
		"CPUFlavorIDsDoNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.CPUFlavorIDs = []string{"cpu3c"} },
			want:   false,
		},
		"AllowedCudaVersionsDoNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.AllowedCudaVersions = []string{"12.1"} },
			want:   false,
		},
		"MinCudaVersionDoesNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.MinCudaVersion = ptrString("11.8") },
			want:   false,
		},
		"NetworkVolumeIDsDoNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.NetworkVolumeIDs = []string{"nv-1"} },
			want:   false,
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
			mutate: func(s *v1alpha1.EndpointParameters) { s.ImageName = ptrString("other:latest") },
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
		"EmptyEnvDoesNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.Env = []v1alpha1.EnvVar{} },
			want:   false,
		},
		"DockerStartCmdDrifts": {
			// Order-sensitive: same elements, different order still drifts.
			mutate: func(s *v1alpha1.EndpointParameters) { s.DockerStartCmd = []string{"run.py", "python"} },
			want:   true,
		},
		"DockerEntrypointDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.DockerEntrypoint = []string{"other"} },
			want:   true,
		},
		"ContainerRegistryAuthIDDrifts": {
			mutate: func(s *v1alpha1.EndpointParameters) { s.ContainerRegistryAuthID = ptrString("other-auth") },
			want:   true,
		},
		"NilDockerAndAuthFieldsDoNotDrift": {
			mutate: func(s *v1alpha1.EndpointParameters) {
				s.DockerStartCmd = nil
				s.DockerEntrypoint = nil
				s.ContainerRegistryAuthID = nil
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

	return runpodclient.NewClient("test-key",
		runpodclient.WithBaseURL(server.URL),
		runpodclient.WithHTTPClient(server.Client()),
	)
}

func ptrInt32(v int32) *int32 { return &v }

func ptrBool(b bool) *bool { return &b }

func ptrString(s string) *string { return &s }
