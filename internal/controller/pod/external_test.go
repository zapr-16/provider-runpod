package pod

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

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

func TestObserve(t *testing.T) {
	type want struct {
		exists          bool
		upToDate        bool
		lateInit        bool
		readyStatus     corev1.ConditionStatus
		readyReason     xpv1.ConditionReason
		networkingReady bool
		podID           string
		runtimeEndpoint string
		connection      managed.ConnectionDetails
	}

	readyResponse := &runpodclient.PodResponse{
		ID:            "pod-123",
		DesiredStatus: "RUNNING",
		PublicIP:      "1.2.3.4",
		PortMappings: map[string]int32{
			"22/tcp":    30022,
			"8888/http": 31000,
		},
		CostPerHr: 1.25,
		Env: map[string]string{
			"MODE": "prod",
		},
		Ports: []string{"8888/http", "22/tcp"},
		Machine: struct {
			GPUDisplayName string `json:"gpuDisplayName"`
		}{
			GPUDisplayName: "NVIDIA A100",
		},
	}

	tests := map[string]struct {
		externalName string
		spec         v1alpha1.PodParameters
		status       v1alpha1.PodObservation
		statusCode   int
		response     *runpodclient.PodResponse
		wantCalls    int
		want         want
	}{
		"EmptyExternalName": {
			want: want{
				exists: false,
			},
		},
		"Non2xxTreatsPodAsMissing": {
			externalName: "pod-123",
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want: want{
				exists: false,
			},
		},
		"RunningWithNetworkingReadyIsAvailable": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				Ports: []v1alpha1.Port{
					{Number: 22, Protocol: "tcp"},
					{Number: 8888, Protocol: "http"},
				},
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response:   readyResponse,
			wantCalls:  1,
			want: want{
				exists:          true,
				upToDate:        true,
				lateInit:        false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv1.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				runtimeEndpoint: "http://1.2.3.4:31000",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("http://1.2.3.4:31000"),
					"port":     []byte("31000"),
				},
			},
		},
		"RunningWithoutPublicIPIsCreating": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
				PublicIP:      "",
				PortMappings:  map[string]int32{"8888/http": 31000},
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				lateInit:        false,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv1.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"ExitedIsUnavailable": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "EXITED",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv1.ReasonUnavailable,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"TerminatedIsUnavailable": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "TERMINATED",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv1.ReasonUnavailable,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"UnknownStatusIsUnavailable": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "MYSTERY",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv1.ReasonUnavailable,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"LateInitPopulatesAtProvider": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				Ports: []v1alpha1.Port{
					{Number: 22, Protocol: "tcp"},
					{Number: 8888, Protocol: "http"},
				},
			},
			statusCode: http.StatusOK,
			response:   readyResponse,
			wantCalls:  1,
			want: want{
				exists:          true,
				upToDate:        true,
				lateInit:        true,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv1.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				runtimeEndpoint: "http://1.2.3.4:31000",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("http://1.2.3.4:31000"),
					"port":     []byte("31000"),
				},
			},
		},
		"EnvDriftMarksNotUpToDate": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				Env: []v1alpha1.EnvVar{{Name: "MODE", Value: "dev"}},
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response:   readyResponse,
			wantCalls:  1,
			want: want{
				exists:          true,
				upToDate:        false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv1.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"NilEnvDoesNotTriggerDrift": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response:     readyResponse,
			wantCalls:    1,
			want: want{
				exists:          true,
				upToDate:        true,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv1.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"PortsDriftMarksNotUpToDate": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				Ports: []v1alpha1.Port{{Number: 9999, Protocol: "http"}},
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response:   readyResponse,
			wantCalls:  1,
			want: want{
				exists:          true,
				upToDate:        false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv1.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"NilPortsDoNotTriggerDriftAndOnlyPublishPodID": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response:     readyResponse,
			wantCalls:    1,
			want: want{
				exists:          true,
				upToDate:        true,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv1.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
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
				if r.URL.Path != "/pods/"+tc.externalName {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if got := r.URL.Query().Get("includeMachine"); got != "true" {
					t.Fatalf("unexpected includeMachine query: %q", got)
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

			p := &v1alpha1.Pod{
				Spec:   v1alpha1.PodSpec{ForProvider: tc.spec},
				Status: v1alpha1.PodStatus{AtProvider: tc.status},
			}
			if tc.externalName != "" {
				meta.SetExternalName(p, tc.externalName)
			}

			e := &external{
				client: newTestClient(t, server),
				log:    logr.Discard(),
			}

			got, err := e.Observe(context.Background(), p)
			if err != nil {
				t.Fatalf("Observe() error = %v", err)
			}

			if calls != tc.wantCalls {
				t.Fatalf("Observe() HTTP calls = %d, want %d", calls, tc.wantCalls)
			}
			if got.ResourceExists != tc.want.exists {
				t.Fatalf("Observe() ResourceExists = %v, want %v", got.ResourceExists, tc.want.exists)
			}
			if tc.want.exists {
				if got.ResourceUpToDate != tc.want.upToDate {
					t.Fatalf("Observe() ResourceUpToDate = %v, want %v", got.ResourceUpToDate, tc.want.upToDate)
				}
				if got.ResourceLateInitialized != tc.want.lateInit {
					t.Fatalf("Observe() ResourceLateInitialized = %v, want %v", got.ResourceLateInitialized, tc.want.lateInit)
				}
				ready := p.GetCondition(xpv1.TypeReady)
				if ready.Status != tc.want.readyStatus {
					t.Fatalf("Observe() Ready status = %v, want %v", ready.Status, tc.want.readyStatus)
				}
				if ready.Reason != tc.want.readyReason {
					t.Fatalf("Observe() Ready reason = %v, want %v", ready.Reason, tc.want.readyReason)
				}
				if p.Status.AtProvider.NetworkingReady != tc.want.networkingReady {
					t.Fatalf("Observe() NetworkingReady = %v, want %v", p.Status.AtProvider.NetworkingReady, tc.want.networkingReady)
				}
				if p.Status.AtProvider.PodID != tc.want.podID {
					t.Fatalf("Observe() AtProvider.PodID = %q, want %q", p.Status.AtProvider.PodID, tc.want.podID)
				}
				if p.Status.AtProvider.RuntimeEndpoint != tc.want.runtimeEndpoint {
					t.Fatalf("Observe() RuntimeEndpoint = %q, want %q", p.Status.AtProvider.RuntimeEndpoint, tc.want.runtimeEndpoint)
				}
				if !reflect.DeepEqual(got.ConnectionDetails, tc.want.connection) {
					t.Fatalf("Observe() ConnectionDetails = %#v, want %#v", got.ConnectionDetails, tc.want.connection)
				}
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("HappyPathSetsExternalNameAndPodIDConnectionDetail", func(t *testing.T) {
		var gotBody runpodclient.CreatePodRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if r.URL.Path != "/pods" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("unexpected authorization header: %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "pod-created"})
		}))
		defer server.Close()

		image := "runpod/image:latest"
		gpuCount := int32(2)
		cloudType := v1alpha1.CloudTypeCommunity
		supportPublicIP := true
		containerDisk := int32(50)
		volume := int32(20)
		mountPath := "/workspace"

		p := &v1alpha1.Pod{
			Spec: v1alpha1.PodSpec{
				ForProvider: v1alpha1.PodParameters{
					ImageName:         &image,
					GPUTypeIDs:        []string{"NVIDIA A100-SXM4-80GB"},
					GPUCount:          &gpuCount,
					CloudType:         &cloudType,
					SupportPublicIP:   &supportPublicIP,
					ContainerDiskInGb: &containerDisk,
					VolumeInGb:        &volume,
					VolumeMountPath:   &mountPath,
					Env:               []v1alpha1.EnvVar{{Name: "MODE", Value: "prod"}},
					Ports:             []v1alpha1.Port{{Number: 8888, Protocol: "http"}, {Number: 22}},
					DockerStartCmd:    []string{"python", "serve.py"},
				},
			},
		}

		e := &external{
			client: newTestClient(t, server),
			log:    logr.Discard(),
		}

		got, err := e.Create(context.Background(), p)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		if meta.GetExternalName(p) != "pod-created" {
			t.Fatalf("Create() external name = %q, want %q", meta.GetExternalName(p), "pod-created")
		}
		if diff := reflect.DeepEqual(got.ConnectionDetails, managed.ConnectionDetails{"podId": []byte("pod-created")}); !diff {
			t.Fatalf("Create() connection details = %#v, want %#v", got.ConnectionDetails, managed.ConnectionDetails{"podId": []byte("pod-created")})
		}
		if gotBody.ImageName == nil || *gotBody.ImageName != image {
			t.Fatalf("Create() imageName = %#v, want %q", gotBody.ImageName, image)
		}
		if !reflect.DeepEqual(gotBody.GPUTypeIDs, []string{"NVIDIA A100-SXM4-80GB"}) {
			t.Fatalf("Create() gpuTypeIds = %#v", gotBody.GPUTypeIDs)
		}
		if gotBody.CloudType == nil || *gotBody.CloudType != string(cloudType) {
			t.Fatalf("Create() cloudType = %#v, want %q", gotBody.CloudType, cloudType)
		}
		if gotBody.SupportPublicIP == nil || *gotBody.SupportPublicIP != supportPublicIP {
			t.Fatalf("Create() supportPublicIp = %#v, want %v", gotBody.SupportPublicIP, supportPublicIP)
		}
		if !reflect.DeepEqual(gotBody.Env, map[string]string{"MODE": "prod"}) {
			t.Fatalf("Create() env = %#v", gotBody.Env)
		}
		if !reflect.DeepEqual(gotBody.Ports, []string{"8888/http", "22/tcp"}) {
			t.Fatalf("Create() ports = %#v", gotBody.Ports)
		}
		if !reflect.DeepEqual(gotBody.DockerStartCmd, []string{"python", "serve.py"}) {
			t.Fatalf("Create() dockerStartCmd = %#v", gotBody.DockerStartCmd)
		}
	})

	t.Run("APINon2xxReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		e := &external{
			client: newTestClient(t, server),
			log:    logr.Discard(),
		}

		_, err := e.Create(context.Background(), &v1alpha1.Pod{})
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errCreatePod) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errCreatePod)
		}
	})
}

func TestDelete(t *testing.T) {
	t.Run("HappyPathReturnsNil", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if r.URL.Path != "/pods/pod-123" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		p := &v1alpha1.Pod{}
		meta.SetExternalName(p, "pod-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), p); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("Delete() HTTP calls = %d, want 1", calls)
		}
	})

	t.Run("Non2xxTreatsDeleteAsSuccess", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		p := &v1alpha1.Pod{}
		meta.SetExternalName(p, "pod-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), p); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCall", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), &v1alpha1.Pod{}); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Delete() HTTP calls = %d, want 0", calls)
		}
	})
}

func TestUpdate(t *testing.T) {
	e := &external{log: logr.Discard()}
	got, err := e.Update(context.Background(), &v1alpha1.Pod{})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !reflect.DeepEqual(got, managed.ExternalUpdate{}) {
		t.Fatalf("Update() = %#v, want empty update", got)
	}
}

func TestHasEnvDrift(t *testing.T) {
	tests := map[string]struct {
		desired  []v1alpha1.EnvVar
		observed map[string]string
		want     bool
	}{
		"NilDesiredDoesNotDrift": {
			observed: map[string]string{},
			want:     false,
		},
		"MatchingValuesDoNotDrift": {
			desired:  []v1alpha1.EnvVar{{Name: "MODE", Value: "prod"}},
			observed: map[string]string{"MODE": "prod"},
			want:     false,
		},
		"DifferingValuesDrift": {
			desired:  []v1alpha1.EnvVar{{Name: "MODE", Value: "dev"}},
			observed: map[string]string{"MODE": "prod"},
			want:     true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := hasEnvDrift(tc.desired, tc.observed); got != tc.want {
				t.Fatalf("hasEnvDrift() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasPortsDrift(t *testing.T) {
	tests := map[string]struct {
		desired  []v1alpha1.Port
		observed []string
		want     bool
	}{
		"NilDesiredDoesNotDrift": {
			observed: []string{"8888/http"},
			want:     false,
		},
		"MatchingSetsDoNotDrift": {
			desired:  []v1alpha1.Port{{Number: 8888, Protocol: "http"}, {Number: 22}},
			observed: []string{"22/tcp", "8888/http"},
			want:     false,
		},
		"DifferingSetsDrift": {
			desired:  []v1alpha1.Port{{Number: 9999, Protocol: "http"}},
			observed: []string{"8888/http"},
			want:     true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := hasPortsDrift(tc.desired, tc.observed); got != tc.want {
				t.Fatalf("hasPortsDrift() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveConnectionTarget(t *testing.T) {
	tests := map[string]struct {
		ports    []v1alpha1.Port
		publicIP string
		mappings map[string]int32
		wantURL  string
		wantPort string
	}{
		"NoDeclaredPorts": {
			publicIP: "1.2.3.4",
			mappings: map[string]int32{"8888/http": 31000},
		},
		"HTTPPortResolvesEndpoint": {
			ports:    []v1alpha1.Port{{Number: 22}, {Number: 8888, Protocol: "http"}},
			publicIP: "1.2.3.4",
			mappings: map[string]int32{"22/tcp": 30022, "8888/http": 31000},
			wantURL:  "http://1.2.3.4:31000",
			wantPort: "31000",
		},
		"NoHTTPPortUsesFallbackPortOnly": {
			ports:    []v1alpha1.Port{{Number: 22}},
			publicIP: "1.2.3.4",
			mappings: map[string]int32{"22/tcp": 30022},
			wantPort: "30022",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			gotURL, gotPort := resolveConnectionTarget(tc.ports, tc.publicIP, tc.mappings)
			if gotURL != tc.wantURL || gotPort != tc.wantPort {
				t.Fatalf("resolveConnectionTarget() = (%q, %q), want (%q, %q)", gotURL, gotPort, tc.wantURL, tc.wantPort)
			}
		})
	}
}

func TestParsePodStartedAt(t *testing.T) {
	tests := map[string]struct {
		value   string
		wantErr bool
	}{
		"RFC3339": {
			value: "2026-04-21T00:06:57Z",
		},
		"GoStyleTimestamp": {
			value: "2026-04-21 00:06:57.505 +0000 UTC",
		},
		"Invalid": {
			value:   "not-a-timestamp",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parsePodStartedAt(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatal("parsePodStartedAt() error = nil, want non-nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("parsePodStartedAt() error = %v", err)
			}
			if got.IsZero() {
				t.Fatal("parsePodStartedAt() returned zero time")
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

func setUnexportedField(t *testing.T, target any, name string, value any) {
	t.Helper()

	v := reflect.ValueOf(target).Elem().FieldByName(name)
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}
