package pod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

func TestObserve(t *testing.T) {
	type want struct {
		exists          bool
		upToDate        bool
		lateInit        bool
		driftDetected   bool
		readyStatus     corev1.ConditionStatus
		readyReason     xpv2.ConditionReason
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
			GPUTypeID      string `json:"gpuTypeId"`
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
		probeDown    bool
		wantCalls    int
		wantErr      bool
		want         want
	}{
		"EmptyExternalName": {
			want: want{
				exists: false,
			},
		},
		"NotFoundTreatsPodAsMissing": {
			externalName: "pod-123",
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want: want{
				exists: false,
			},
		},
		"ServerErrorReturnsError": {
			// A transient 5xx must NOT look like a missing pod: the
			// reconciler would Create() a duplicate and orphan the old
			// (still billing) one.
			externalName: "pod-123",
			statusCode:   http.StatusInternalServerError,
			wantCalls:    1,
			wantErr:      true,
		},
		"RateLimitedReturnsError": {
			externalName: "pod-123",
			statusCode:   http.StatusTooManyRequests,
			wantCalls:    1,
			wantErr:      true,
		},
		"UnauthorizedReturnsError": {
			externalName: "pod-123",
			statusCode:   http.StatusUnauthorized,
			wantCalls:    1,
			wantErr:      true,
		},
		"InvalidExternalNameReturnsError": {
			// A crafted external-name must never reach the RunPod API as a
			// URL path segment.
			externalName: "pod-123/../../v1/endpoints/victim",
			wantCalls:    0,
			wantErr:      true,
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
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				runtimeEndpoint: "https://pod-123-8888.proxy.runpod.net",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("https://pod-123-8888.proxy.runpod.net"),
					"port":     []byte("8888"),
				},
			},
		},
		"RunningCommunityHTTPWithoutPublicIPIsAvailable": {
			// Regression: COMMUNITY pods with http ports never receive a
			// public IP — the proxy endpoint alone must mark them ready.
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				Ports: []v1alpha1.Port{{Number: 8000, Protocol: "http"}},
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
				Ports:         []string{"8000/http"},
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				lateInit:        false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				runtimeEndpoint: "https://pod-123-8000.proxy.runpod.net",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("https://pod-123-8000.proxy.runpod.net"),
					"port":     []byte("8000"),
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
				readyReason:     xpv2.ReasonCreating,
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
				upToDate:        false,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonUnavailable,
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
				readyReason:     xpv2.ReasonUnavailable,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"RunningWithProxyProbeFailingIsCreating": {
			// The proxy URL is derived from the pod ID alone; until the
			// container actually listens the proxy returns 502 — the pod
			// must not go Available on a dead endpoint.
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
			probeDown:  true,
			wantCalls:  1,
			want: want{
				exists:          true,
				upToDate:        true,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				runtimeEndpoint: "https://pod-123-8888.proxy.runpod.net",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("https://pod-123-8888.proxy.runpod.net"),
					"port":     []byte("8888"),
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
				readyReason:     xpv2.ReasonUnavailable,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"ObservePopulatesAtProvider": {
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
				lateInit:        false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				runtimeEndpoint: "https://pod-123-8888.proxy.runpod.net",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("https://pod-123-8888.proxy.runpod.net"),
					"port":     []byte("8888"),
				},
			},
		},
		"EnvDriftIsPatchableAndNotUpToDate": {
			// env is a PATCHable field now: drift means ResourceUpToDate is
			// false so Update() runs, but DriftDetected stays false because
			// it now covers only immutable-field drift.
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
				driftDetected:   false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"ImageNameDriftIsNotUpToDate": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				ImageName: strPtr("img:v2"),
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
				Image:         "img:v1",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        false,
				driftDetected:   false,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"NoDriftNoLifecycleMismatchIsUpToDate": {
			externalName: "pod-123",
			status:       v1alpha1.PodObservation{PodID: "existing"},
			statusCode:   http.StatusOK,
			response:     readyResponse,
			wantCalls:    1,
			want: want{
				exists:          true,
				upToDate:        true,
				driftDetected:   false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"GPUTypeDriftIsImmutableAndUpToDateButFlagged": {
			// GPU type is not PATCHable: divergence is surfaced only via
			// status.atProvider.driftDetected, never via ResourceUpToDate.
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				GPUTypeIDs: []string{"NVIDIA L4"},
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
				Machine: struct {
					GPUDisplayName string `json:"gpuDisplayName"`
					GPUTypeID      string `json:"gpuTypeId"`
				}{
					GPUTypeID: "NVIDIA RTX A4000",
				},
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				driftDetected:   true,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"GPUTypeMatchingOneOfPriorityListIsNotDrift": {
			// gpuTypeIds is a priority-ordered list: membership, not
			// equality with the first element, is the correct check.
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				GPUTypeIDs: []string{"NVIDIA RTX A4000", "NVIDIA L4"},
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
				Machine: struct {
					GPUDisplayName string `json:"gpuDisplayName"`
					GPUTypeID      string `json:"gpuTypeId"`
				}{
					GPUTypeID: "NVIDIA L4",
				},
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				driftDetected:   false,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"InterruptibleDriftIsImmutableAndFlagged": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				Interruptible: ptrBool(true),
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
				Interruptible: false,
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				driftDetected:   true,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"LifecycleDriftExitedSpecVsRunningObservedIsNotUpToDate": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				DesiredState: strPtr("EXITED"),
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "RUNNING",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        false,
				driftDetected:   false,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonCreating,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"LifecycleDriftRunningSpecVsExitedObservedIsNotUpToDate": {
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				DesiredState: strPtr("RUNNING"),
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "EXITED",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        false,
				driftDetected:   false,
				readyStatus:     corev1.ConditionFalse,
				readyReason:     xpv2.ReasonUnavailable,
				networkingReady: false,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"DesiredStateExitedMatchingResponseIsAvailableAndUpToDate": {
			// Stopped-by-request IS the desired state: this must be
			// Available, not Unavailable, and recreateOnTerminate must not
			// fire even though it's set.
			externalName: "pod-123",
			spec: v1alpha1.PodParameters{
				DesiredState:        strPtr("EXITED"),
				RecreateOnTerminate: ptrBool(true),
			},
			status:     v1alpha1.PodObservation{PodID: "existing"},
			statusCode: http.StatusOK,
			response: &runpodclient.PodResponse{
				ID:            "pod-123",
				DesiredStatus: "EXITED",
			},
			wantCalls: 1,
			want: want{
				exists:          true,
				upToDate:        true,
				driftDetected:   false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: false,
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
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				connection: managed.ConnectionDetails{
					"podId": []byte("pod-123"),
				},
			},
		},
		"PortsDriftIsPatchableAndNotUpToDate": {
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
				driftDetected:   false,
				readyStatus:     corev1.ConditionTrue,
				readyReason:     xpv2.ReasonAvailable,
				networkingReady: true,
				podID:           "pod-123",
				runtimeEndpoint: "https://pod-123-9999.proxy.runpod.net",
				connection: managed.ConnectionDetails{
					"podId":    []byte("pod-123"),
					"endpoint": []byte("https://pod-123-9999.proxy.runpod.net"),
					"port":     []byte("9999"),
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
				readyReason:     xpv2.ReasonAvailable,
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
				client:    newTestClient(t, server),
				log:       logr.Discard(),
				probeHTTP: stubProbe(!tc.probeDown),
			}

			got, err := e.Observe(context.Background(), p)
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
			if tc.want.exists {
				if got.ResourceUpToDate != tc.want.upToDate {
					t.Fatalf("Observe() ResourceUpToDate = %v, want %v", got.ResourceUpToDate, tc.want.upToDate)
				}
				if got.ResourceLateInitialized != tc.want.lateInit {
					t.Fatalf("Observe() ResourceLateInitialized = %v, want %v", got.ResourceLateInitialized, tc.want.lateInit)
				}
				ready := p.GetCondition(xpv2.TypeReady)
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
				if p.Status.AtProvider.DriftDetected != tc.want.driftDetected {
					t.Fatalf("Observe() DriftDetected = %v, want %v", p.Status.AtProvider.DriftDetected, tc.want.driftDetected)
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
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-test"},
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
		if !reflect.DeepEqual(got.ConnectionDetails, managed.ConnectionDetails{"podId": []byte("pod-created")}) {
			t.Fatalf("Create() connection details = %#v, want %#v", got.ConnectionDetails, managed.ConnectionDetails{"podId": []byte("pod-created")})
		}
		if gotBody.Name == nil || *gotBody.Name != "vllm-test" {
			t.Fatalf("Create() name = %#v, want %q", gotBody.Name, "vllm-test")
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

	t.Run("SendsNameWithUIDSuffixForDeterministicRecovery", func(t *testing.T) {
		var gotBody runpodclient.CreatePodRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "pod-created"})
		}))
		defer server.Close()

		p := &v1alpha1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-test", UID: "550e8400-e29b-41d4-a716-446655440000"},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Create(context.Background(), p); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		want := "vllm-test-550e8400"
		if gotBody.Name == nil || *gotBody.Name != want {
			t.Fatalf("Create() name = %#v, want %q", gotBody.Name, want)
		}
	})
}

// TestObserveAdoptsIncompleteCreate covers Observe()'s ambiguous-create
// recovery: an empty external-name annotation combined with
// meta.ExternalCreateIncomplete means a prior Create's result was never
// confirmed, so Observe must list pods and match on the deterministic
// create name instead of blindly reporting the resource missing (which
// would let the reconciler retry Create and orphan an already-billing pod).
func TestObserveAdoptsIncompleteCreate(t *testing.T) {
	image := "runpod/image:latest"
	newPod := func() *v1alpha1.Pod {
		return &v1alpha1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "vllm-test", UID: "550e8400-e29b-41d4-a716-446655440000"},
			Spec: v1alpha1.PodSpec{
				ForProvider: v1alpha1.PodParameters{
					ImageName:  &image,
					GPUTypeIDs: []string{"NVIDIA A100-SXM4-80GB"},
				},
			},
		}
	}
	markIncomplete := func(p *v1alpha1.Pod) {
		meta.SetExternalCreatePending(p, time.Now())
	}
	derivedName := "vllm-test-550e8400"

	t.Run("NoIncompleteCreateSkipsListCall", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Observe(context.Background(), newPod())
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
			if r.URL.Path != "/pods" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`[{"id":"pod-other","name":"unrelated"}]`))
		}))
		defer server.Close()

		p := newPod()
		markIncomplete(p)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Observe(context.Background(), p)
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if got.ResourceExists {
			t.Fatal("Observe() ResourceExists = true, want false")
		}
		if meta.GetExternalName(p) != "" {
			t.Fatalf("Observe() external-name = %q, want empty", meta.GetExternalName(p))
		}
	})

	t.Run("SingleMatchAdoptsAndLateInitializes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/pods" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode([]runpodclient.PodResponse{
				{ID: "pod-recovered", Name: derivedName, Image: image, DesiredStatus: "RUNNING"},
			})
		}))
		defer server.Close()

		p := newPod()
		markIncomplete(p)

		e := &external{client: newTestClient(t, server), log: logr.Discard(), probeHTTP: stubProbe(true)}
		got, err := e.Observe(context.Background(), p)
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if !got.ResourceExists {
			t.Fatal("Observe() ResourceExists = false, want true")
		}
		if !got.ResourceLateInitialized {
			t.Fatal("Observe() ResourceLateInitialized = false, want true (must persist the adopted external-name)")
		}
		if meta.GetExternalName(p) != "pod-recovered" {
			t.Fatalf("Observe() external-name = %q, want %q", meta.GetExternalName(p), "pod-recovered")
		}
	})

	t.Run("MultipleMatchesReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]runpodclient.PodResponse{
				{ID: "pod-a", Name: derivedName, Image: image},
				{ID: "pod-b", Name: derivedName, Image: image},
			})
		}))
		defer server.Close()

		p := newPod()
		markIncomplete(p)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Observe(context.Background(), p)
		if err == nil {
			t.Fatal("Observe() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errAmbiguousCreate) {
			t.Fatalf("Observe() error = %q, want it to mention %q", err.Error(), errAmbiguousCreate)
		}
		if meta.GetExternalName(p) != "" {
			t.Fatalf("Observe() external-name = %q, want empty (must not guess)", meta.GetExternalName(p))
		}
	})

	t.Run("IdentityMismatchReturnsErrorAndDoesNotAdopt", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]runpodclient.PodResponse{
				// Same derived name, but a different image: this must never
				// be silently adopted, even though the name matches exactly.
				{ID: "pod-wrong-image", Name: derivedName, Image: "some/other-image:latest"},
			})
		}))
		defer server.Close()

		p := newPod()
		markIncomplete(p)

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Observe(context.Background(), p)
		if err == nil {
			t.Fatal("Observe() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errAmbiguousCreate) {
			t.Fatalf("Observe() error = %q, want it to mention %q", err.Error(), errAmbiguousCreate)
		}
		if meta.GetExternalName(p) != "" {
			t.Fatalf("Observe() external-name = %q, want empty (must not adopt on identity mismatch)", meta.GetExternalName(p))
		}
	})
}

// TestHasMutableDriftIgnoresDerivedNameSuffix confirms that the deterministic
// -uid8 suffix appended to the name sent on create never surfaces as drift:
// hasMutableDrift never compares against a pod's name in the first place, so
// the response's name is free to include the suffix (or anything else)
// without affecting up-to-date evaluation.
func TestHasMutableDriftIgnoresDerivedNameSuffix(t *testing.T) {
	image := "runpod/image:latest"
	spec := v1alpha1.PodParameters{ImageName: &image}
	response := &runpodclient.PodResponse{Name: "vllm-test-550e8400", Image: image}

	if hasMutableDrift(spec, response) {
		t.Fatal("hasMutableDrift() = true, want false: the derived-name suffix must never be reported as drift")
	}
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

	t.Run("NotFoundTreatsDeleteAsSuccess", func(t *testing.T) {
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

	t.Run("GoneTreatsDeleteAsSuccess", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
		}))
		defer server.Close()

		p := &v1alpha1.Pod{}
		meta.SetExternalName(p, "pod-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), p); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("ServerErrorReturnsError", func(t *testing.T) {
		// A failed DELETE must be retried: swallowing it removes the
		// finalizer while the pod keeps running and billing.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		p := &v1alpha1.Pod{}
		meta.SetExternalName(p, "pod-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Delete(context.Background(), p)
		if err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errDeletePod) {
			t.Fatalf("Delete() error = %q, want wrapped %q", err.Error(), errDeletePod)
		}
	})

	t.Run("InvalidExternalNameReturnsError", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		p := &v1alpha1.Pod{}
		meta.SetExternalName(p, "pod-123/../../v1/endpoints/victim")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), p); err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if calls != 0 {
			t.Fatalf("Delete() HTTP calls = %d, want 0", calls)
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

func TestObserveRecreateOnTerminate(t *testing.T) {
	terminated := &runpodclient.PodResponse{
		ID:            "pod-123",
		DesiredStatus: "TERMINATED",
	}

	newPod := func() *v1alpha1.Pod {
		p := &v1alpha1.Pod{
			Spec: v1alpha1.PodSpec{ForProvider: v1alpha1.PodParameters{
				RecreateOnTerminate: ptrBool(true),
			}},
			Status: v1alpha1.PodStatus{AtProvider: v1alpha1.PodObservation{PodID: "existing"}},
		}
		meta.SetExternalName(p, "pod-123")
		return p
	}

	t.Run("DeletesOldPodThenClearsExternalName", func(t *testing.T) {
		// Terminated pods retain their disk and keep billing storage; the
		// old pod must be deleted before the ID is dropped, or every Spot
		// reclaim leaks one.
		var methods []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			if r.URL.Path != "/pods/pod-123" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(terminated)
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method: %s", r.Method)
			}
		}))
		defer server.Close()

		p := newPod()
		e := &external{client: newTestClient(t, server), log: logr.Discard(), probeHTTP: stubProbe(true)}

		got, err := e.Observe(context.Background(), p)
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if got.ResourceExists {
			t.Fatal("Observe() ResourceExists = true, want false")
		}
		if !reflect.DeepEqual(methods, []string{"GET", "DELETE"}) {
			t.Fatalf("Observe() HTTP methods = %v, want [GET DELETE]", methods)
		}
		if meta.GetExternalName(p) != "" {
			t.Fatalf("Observe() external name = %q, want cleared", meta.GetExternalName(p))
		}
	})

	t.Run("AlreadyGoneOldPodStillClearsExternalName", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(terminated)
			case http.MethodDelete:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		p := newPod()
		e := &external{client: newTestClient(t, server), log: logr.Discard(), probeHTTP: stubProbe(true)}

		got, err := e.Observe(context.Background(), p)
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
		if got.ResourceExists {
			t.Fatal("Observe() ResourceExists = true, want false")
		}
		if meta.GetExternalName(p) != "" {
			t.Fatalf("Observe() external name = %q, want cleared", meta.GetExternalName(p))
		}
	})

	t.Run("DeleteFailureReturnsErrorAndKeepsExternalName", func(t *testing.T) {
		// If cleanup fails the pod ID must survive so the next reconcile
		// can retry — clearing it would permanently orphan the pod.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(terminated)
			case http.MethodDelete:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		p := newPod()
		e := &external{client: newTestClient(t, server), log: logr.Discard(), probeHTTP: stubProbe(true)}

		if _, err := e.Observe(context.Background(), p); err == nil {
			t.Fatal("Observe() error = nil, want non-nil")
		}
		if meta.GetExternalName(p) != "pod-123" {
			t.Fatalf("Observe() external name = %q, want preserved %q", meta.GetExternalName(p), "pod-123")
		}
	})
}

func TestUpdate(t *testing.T) {
	newPod := func(spec v1alpha1.PodParameters) *v1alpha1.Pod {
		p := &v1alpha1.Pod{Spec: v1alpha1.PodSpec{ForProvider: spec}}
		meta.SetExternalName(p, "pod-123")
		return p
	}

	t.Run("PatchesMutableFieldsOnlyNoLifecycleCallWhenStatesMatch", func(t *testing.T) {
		var requests []string
		var gotPatchBody runpodclient.UpdatePodRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(&runpodclient.PodResponse{
					ID:            "pod-123",
					DesiredStatus: "RUNNING",
					Image:         "img:v1",
				})
			case http.MethodPatch:
				if err := json.NewDecoder(r.Body).Decode(&gotPatchBody); err != nil {
					t.Fatalf("decode PATCH body: %v", err)
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method: %s", r.Method)
			}
		}))
		defer server.Close()

		p := newPod(v1alpha1.PodParameters{ImageName: strPtr("img:v2")})
		e := &external{client: newTestClient(t, server), log: logr.Discard()}

		got, err := e.Update(context.Background(), p)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if !reflect.DeepEqual(got, managed.ExternalUpdate{}) {
			t.Fatalf("Update() = %#v, want empty update", got)
		}
		if !reflect.DeepEqual(requests, []string{"GET /pods/pod-123", "PATCH /pods/pod-123"}) {
			t.Fatalf("Update() requests = %v, want GET then PATCH only (no lifecycle call)", requests)
		}
		if gotPatchBody.ImageName == nil || *gotPatchBody.ImageName != "img:v2" {
			t.Fatalf("Update() PATCH imageName = %#v, want %q", gotPatchBody.ImageName, "img:v2")
		}
	})

	t.Run("StopsPodWhenDesiredStateExitedAndObservedRunning", func(t *testing.T) {
		var requests []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodGet:
				_ = json.NewEncoder(w).Encode(&runpodclient.PodResponse{
					ID:            "pod-123",
					DesiredStatus: "RUNNING",
				})
			case r.Method == http.MethodPatch:
				w.WriteHeader(http.StatusOK)
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/stop"):
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		p := newPod(v1alpha1.PodParameters{
			ImageName:    strPtr("img:v2"),
			DesiredState: strPtr("EXITED"),
		})
		e := &external{client: newTestClient(t, server), log: logr.Discard()}

		if _, err := e.Update(context.Background(), p); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		want := []string{"GET /pods/pod-123", "PATCH /pods/pod-123", "POST /pods/pod-123/stop"}
		if !reflect.DeepEqual(requests, want) {
			t.Fatalf("Update() requests = %v, want %v (PATCH before stop)", requests, want)
		}
	})

	t.Run("StartsPodWhenDesiredStateRunningOrUnsetAndObservedExited", func(t *testing.T) {
		var requests []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodGet:
				_ = json.NewEncoder(w).Encode(&runpodclient.PodResponse{
					ID:            "pod-123",
					DesiredStatus: "EXITED",
				})
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		p := newPod(v1alpha1.PodParameters{})
		e := &external{client: newTestClient(t, server), log: logr.Discard()}

		if _, err := e.Update(context.Background(), p); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		want := []string{"GET /pods/pod-123", "POST /pods/pod-123/start"}
		if !reflect.DeepEqual(requests, want) {
			t.Fatalf("Update() requests = %v, want %v", requests, want)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCall", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Update(context.Background(), &v1alpha1.Pod{})
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if !reflect.DeepEqual(got, managed.ExternalUpdate{}) {
			t.Fatalf("Update() = %#v, want empty update", got)
		}
		if calls != 0 {
			t.Fatalf("Update() HTTP calls = %d, want 0", calls)
		}
	})
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
		"EmptyDesiredDoesNotDrift": {
			// Empty and nil both mean "unmanaged": the PATCH payload uses
			// omitempty, so an empty value could never be reconciled anyway.
			desired:  []v1alpha1.EnvVar{},
			observed: map[string]string{"MODE": "prod"},
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
		"EmptyDesiredDoesNotDrift": {
			desired:  []v1alpha1.Port{},
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
		podID    string
		publicIP string
		mappings map[string]int32
		wantURL  string
		wantPort string
	}{
		"NoDeclaredPorts": {
			podID:    "pod-123",
			publicIP: "1.2.3.4",
			mappings: map[string]int32{"8888/http": 31000},
		},
		"HTTPPortResolvesProxyEndpoint": {
			ports:    []v1alpha1.Port{{Number: 22}, {Number: 8888, Protocol: "http"}},
			podID:    "pod-123",
			publicIP: "1.2.3.4",
			mappings: map[string]int32{"22/tcp": 30022, "8888/http": 31000},
			wantURL:  "https://pod-123-8888.proxy.runpod.net",
			wantPort: "8888",
		},
		"HTTPPortWithoutPublicIPStillResolvesProxy": {
			// COMMUNITY pods with http-only ports never get a public IP;
			// the proxy endpoint must resolve regardless.
			ports:    []v1alpha1.Port{{Number: 8000, Protocol: "http"}},
			podID:    "pod-123",
			wantURL:  "https://pod-123-8000.proxy.runpod.net",
			wantPort: "8000",
		},
		"NoHTTPPortUsesFallbackPortOnly": {
			ports:    []v1alpha1.Port{{Number: 22}},
			podID:    "pod-123",
			publicIP: "1.2.3.4",
			mappings: map[string]int32{"22/tcp": 30022},
			wantPort: "30022",
		},
		"NoPodIDNoPublicIPResolvesNothing": {
			ports: []v1alpha1.Port{{Number: 8000, Protocol: "http"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			gotURL, gotPort := resolveConnectionTarget(tc.ports, tc.podID, tc.publicIP, tc.mappings)
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

	return runpodclient.NewClient("test-key",
		runpodclient.WithBaseURL(server.URL),
		runpodclient.WithHTTPClient(server.Client()),
	)
}

func stubProbe(up bool) func(context.Context, string) bool {
	return func(context.Context, string) bool { return up }
}

func ptrBool(b bool) *bool { return &b }

func strPtr(s string) *string { return &s }
