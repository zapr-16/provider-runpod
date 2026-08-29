package networkvolume

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

func readyResponse() *runpodclient.NetworkVolumeResponse {
	return &runpodclient.NetworkVolumeResponse{
		ID:           "nv-123",
		Name:         "model-cache",
		Size:         200,
		DataCenterID: "EU-RO-1",
	}
}

func matchingSpec() v1alpha1.NetworkVolumeParameters {
	name := "model-cache"
	return v1alpha1.NetworkVolumeParameters{
		Name:         &name,
		Size:         200,
		DataCenterID: "EU-RO-1",
	}
}

func ptrString(s string) *string { return &s }

func TestObserve(t *testing.T) {
	type want struct {
		exists      bool
		upToDate    bool
		readyStatus corev1.ConditionStatus
		id          string
		name        string
		size        int32
		dataCenter  string
		connection  managed.ConnectionDetails
	}

	tests := map[string]struct {
		externalName string
		spec         func() v1alpha1.NetworkVolumeParameters
		statusCode   int
		response     *runpodclient.NetworkVolumeResponse
		wantCalls    int
		wantErr      bool
		want         want
	}{
		"EmptyExternalName": {
			spec: matchingSpec,
			want: want{exists: false},
		},
		"NotFoundTreatsVolumeAsMissing": {
			externalName: "nv-123",
			spec:         matchingSpec,
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want:         want{exists: false},
		},
		"ServerErrorReturnsError": {
			externalName: "nv-123",
			spec:         matchingSpec,
			statusCode:   http.StatusInternalServerError,
			wantCalls:    1,
			wantErr:      true,
		},
		"InvalidExternalNameReturnsError": {
			externalName: "nv-123/../../v1/pods/victim",
			spec:         matchingSpec,
			wantCalls:    0,
			wantErr:      true,
		},
		"MatchingSpecIsUpToDateAndAvailable": {
			externalName: "nv-123",
			spec:         matchingSpec,
			statusCode:   http.StatusOK,
			response:     readyResponse(),
			wantCalls:    1,
			want: want{
				exists:      true,
				upToDate:    true,
				readyStatus: corev1.ConditionTrue,
				id:          "nv-123",
				name:        "model-cache",
				size:        200,
				dataCenter:  "EU-RO-1",
				connection:  managed.ConnectionDetails{"networkVolumeId": []byte("nv-123")},
			},
		},
		"SizeDriftMarksNotUpToDate": {
			externalName: "nv-123",
			spec: func() v1alpha1.NetworkVolumeParameters {
				s := matchingSpec()
				s.Size = 100
				return s
			},
			statusCode: http.StatusOK,
			response:   readyResponse(),
			wantCalls:  1,
			want: want{
				exists:      true,
				upToDate:    false,
				readyStatus: corev1.ConditionTrue,
				id:          "nv-123",
				name:        "model-cache",
				size:        200,
				dataCenter:  "EU-RO-1",
				connection:  managed.ConnectionDetails{"networkVolumeId": []byte("nv-123")},
			},
		},
		"NilNameDoesNotDrift": {
			externalName: "nv-123",
			spec: func() v1alpha1.NetworkVolumeParameters {
				return v1alpha1.NetworkVolumeParameters{Size: 200, DataCenterID: "EU-RO-1"}
			},
			statusCode: http.StatusOK,
			response:   readyResponse(),
			wantCalls:  1,
			want: want{
				exists:      true,
				upToDate:    true,
				readyStatus: corev1.ConditionTrue,
				id:          "nv-123",
				name:        "model-cache",
				size:        200,
				dataCenter:  "EU-RO-1",
				connection:  managed.ConnectionDetails{"networkVolumeId": []byte("nv-123")},
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
				if r.URL.Path != "/networkvolumes/"+tc.externalName {
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

			nv := &v1alpha1.NetworkVolume{
				Spec: v1alpha1.NetworkVolumeSpec{ForProvider: tc.spec()},
			}
			if tc.externalName != "" {
				meta.SetExternalName(nv, tc.externalName)
			}

			e := &external{client: newTestClient(t, server), log: logr.Discard()}

			got, err := e.Observe(context.Background(), nv)
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
			ready := nv.GetCondition(xpv2.TypeReady)
			if ready.Status != tc.want.readyStatus {
				t.Errorf("Observe() Ready status = %v, want %v", ready.Status, tc.want.readyStatus)
			}
			at := nv.Status.AtProvider
			if at.NetworkVolumeID != tc.want.id {
				t.Errorf("Observe() NetworkVolumeID = %q, want %q", at.NetworkVolumeID, tc.want.id)
			}
			if at.Name != tc.want.name {
				t.Errorf("Observe() Name = %q, want %q", at.Name, tc.want.name)
			}
			if at.Size != tc.want.size {
				t.Errorf("Observe() Size = %d, want %d", at.Size, tc.want.size)
			}
			if at.DataCenterID != tc.want.dataCenter {
				t.Errorf("Observe() DataCenterID = %q, want %q", at.DataCenterID, tc.want.dataCenter)
			}
			if !reflect.DeepEqual(got.ConnectionDetails, tc.want.connection) {
				t.Errorf("Observe() ConnectionDetails = %#v, want %#v", got.ConnectionDetails, tc.want.connection)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("PostsWithMetadataNameWhenSpecNameNil", func(t *testing.T) {
		var gotBody runpodclient.CreateNetworkVolumeRequest
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "nv-created"})
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "model-cache"},
			Spec: v1alpha1.NetworkVolumeSpec{ForProvider: v1alpha1.NetworkVolumeParameters{
				Size:         200,
				DataCenterID: "EU-RO-1",
			}},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		got, err := e.Create(context.Background(), nv)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if gotPath != "/networkvolumes" {
			t.Fatalf("Create() path = %q, want %q", gotPath, "/networkvolumes")
		}
		if gotBody.Name != "model-cache" || gotBody.Size != 200 || gotBody.DataCenterID != "EU-RO-1" {
			t.Fatalf("Create() request = %#v", gotBody)
		}
		if meta.GetExternalName(nv) != "nv-created" {
			t.Fatalf("Create() external name = %q, want %q", meta.GetExternalName(nv), "nv-created")
		}
		wantDetails := managed.ConnectionDetails{"networkVolumeId": []byte("nv-created")}
		if !reflect.DeepEqual(got.ConnectionDetails, wantDetails) {
			t.Fatalf("Create() connection details = %#v, want %#v", got.ConnectionDetails, wantDetails)
		}
	})

	t.Run("UsesSpecNameWhenSet", func(t *testing.T) {
		var gotBody runpodclient.CreateNetworkVolumeRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "nv-created"})
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "resource-name"},
			Spec: v1alpha1.NetworkVolumeSpec{ForProvider: v1alpha1.NetworkVolumeParameters{
				Name:         ptrString("custom-name"),
				Size:         200,
				DataCenterID: "EU-RO-1",
			}},
		}

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Create(context.Background(), nv); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if gotBody.Name != "custom-name" {
			t.Fatalf("Create() name = %q, want %q", gotBody.Name, "custom-name")
		}
	})

	t.Run("CreateFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Create(context.Background(), &v1alpha1.NetworkVolume{})
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errCreateNetworkVolume) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errCreateNetworkVolume)
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("PatchesNameAndSize", func(t *testing.T) {
		var gotBody runpodclient.UpdateNetworkVolumeRequest
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "nv-123"})
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{
			Spec: v1alpha1.NetworkVolumeSpec{ForProvider: matchingSpec()},
		}
		meta.SetExternalName(nv, "nv-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), nv); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if gotPath != "/networkvolumes/nv-123" {
			t.Fatalf("Update() path = %q, want /networkvolumes/nv-123", gotPath)
		}
		if gotBody.Size == nil || *gotBody.Size != 200 {
			t.Fatalf("Update() size = %#v, want 200", gotBody.Size)
		}
		if gotBody.Name == nil || *gotBody.Name != "model-cache" {
			t.Fatalf("Update() name = %#v, want model-cache", gotBody.Name)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCalls", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), &v1alpha1.NetworkVolume{Spec: v1alpha1.NetworkVolumeSpec{ForProvider: matchingSpec()}}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Update() HTTP calls = %d, want 0", calls)
		}
	})

	t.Run("PatchFailureReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{Spec: v1alpha1.NetworkVolumeSpec{ForProvider: matchingSpec()}}
		meta.SetExternalName(nv, "nv-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Update(context.Background(), nv)
		if err == nil {
			t.Fatal("Update() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errUpdateNetworkVolume) {
			t.Fatalf("Update() error = %q, want wrapped %q", err.Error(), errUpdateNetworkVolume)
		}
	})
}

func TestDelete(t *testing.T) {
	t.Run("DeletesVolume", func(t *testing.T) {
		var gotMethod, gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{}
		meta.SetExternalName(nv, "nv-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), nv); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if gotMethod != http.MethodDelete || gotPath != "/networkvolumes/nv-123" {
			t.Fatalf("Delete() = %s %s, want DELETE /networkvolumes/nv-123", gotMethod, gotPath)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCalls", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), &v1alpha1.NetworkVolume{}); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Delete() HTTP calls = %d, want 0", calls)
		}
	})

	t.Run("NotFoundDeleteIsSuccess", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{}
		meta.SetExternalName(nv, "nv-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), nv); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("ServerErrorReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		nv := &v1alpha1.NetworkVolume{}
		meta.SetExternalName(nv, "nv-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Delete(context.Background(), nv)
		if err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errDeleteNetworkVolume) {
			t.Fatalf("Delete() error = %q, want wrapped %q", err.Error(), errDeleteNetworkVolume)
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
