package containerregistryauth

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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	return s
}

func newTestClient(t *testing.T, server *httptest.Server) *runpodclient.Client {
	t.Helper()
	return runpodclient.NewClient("test-key",
		runpodclient.WithBaseURL(server.URL),
		runpodclient.WithHTTPClient(server.Client()),
	)
}

func readyResponse() *runpodclient.ContainerRegistryAuthResponse {
	return &runpodclient.ContainerRegistryAuthResponse{ID: "cra-123", Name: "ghcr"}
}

func TestObserve(t *testing.T) {
	type want struct {
		exists      bool
		upToDate    bool
		readyStatus corev1.ConditionStatus
		id          string
		name        string
		connection  managed.ConnectionDetails
	}

	tests := map[string]struct {
		externalName string
		statusCode   int
		response     *runpodclient.ContainerRegistryAuthResponse
		wantCalls    int
		wantErr      bool
		want         want
	}{
		"EmptyExternalName": {
			want: want{exists: false},
		},
		"NotFoundTreatsAuthAsMissing": {
			externalName: "cra-123",
			statusCode:   http.StatusNotFound,
			wantCalls:    1,
			want:         want{exists: false},
		},
		"ServerErrorReturnsError": {
			externalName: "cra-123",
			statusCode:   http.StatusInternalServerError,
			wantCalls:    1,
			wantErr:      true,
		},
		"InvalidExternalNameReturnsError": {
			externalName: "cra-123/../../v1/pods/victim",
			wantCalls:    0,
			wantErr:      true,
		},
		"FoundIsAlwaysUpToDateAndAvailable": {
			externalName: "cra-123",
			statusCode:   http.StatusOK,
			response:     readyResponse(),
			wantCalls:    1,
			want: want{
				exists:      true,
				upToDate:    true,
				readyStatus: corev1.ConditionTrue,
				id:          "cra-123",
				name:        "ghcr",
				connection:  managed.ConnectionDetails{"containerRegistryAuthId": []byte("cra-123")},
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
				if r.URL.Path != "/containerregistryauth/"+tc.externalName {
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

			cra := &v1alpha1.ContainerRegistryAuth{}
			if tc.externalName != "" {
				meta.SetExternalName(cra, tc.externalName)
			}

			e := &external{client: newTestClient(t, server), log: logr.Discard()}

			got, err := e.Observe(context.Background(), cra)
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
			ready := cra.GetCondition(xpv2.TypeReady)
			if ready.Status != tc.want.readyStatus {
				t.Errorf("Observe() Ready status = %v, want %v", ready.Status, tc.want.readyStatus)
			}
			at := cra.Status.AtProvider
			if at.ContainerRegistryAuthID != tc.want.id {
				t.Errorf("Observe() ContainerRegistryAuthID = %q, want %q", at.ContainerRegistryAuthID, tc.want.id)
			}
			if at.Name != tc.want.name {
				t.Errorf("Observe() Name = %q, want %q", at.Name, tc.want.name)
			}
			if !reflect.DeepEqual(got.ConnectionDetails, tc.want.connection) {
				t.Errorf("Observe() ConnectionDetails = %#v, want %#v", got.ConnectionDetails, tc.want.connection)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("PostsWithMetadataNameAndSecretCredentials", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr-creds", Namespace: "default"},
			Data: map[string][]byte{
				"username": []byte("my-user"),
				"password": []byte("my-pass"),
			},
		}
		kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()

		var gotBody runpodclient.CreateContainerRegistryAuthRequest
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "cra-created"})
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr", Namespace: "default"},
			Spec: v1alpha1.ContainerRegistryAuthSpec{ForProvider: v1alpha1.ContainerRegistryAuthParameters{
				CredentialsSecretRef: v1alpha1.ContainerRegistrySecretRef{Name: "ghcr-creds"},
			}},
		}

		e := &external{client: newTestClient(t, server), kube: kube, log: logr.Discard()}
		got, err := e.Create(context.Background(), cra)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if gotPath != "/containerregistryauth" {
			t.Fatalf("Create() path = %q, want %q", gotPath, "/containerregistryauth")
		}
		if gotBody.Name != "ghcr" || gotBody.Username != "my-user" || gotBody.Password != "my-pass" {
			t.Fatalf("Create() request = %#v", gotBody)
		}
		if meta.GetExternalName(cra) != "cra-created" {
			t.Fatalf("Create() external name = %q, want %q", meta.GetExternalName(cra), "cra-created")
		}
		wantDetails := managed.ConnectionDetails{"containerRegistryAuthId": []byte("cra-created")}
		if !reflect.DeepEqual(got.ConnectionDetails, wantDetails) {
			t.Fatalf("Create() connection details = %#v, want %#v", got.ConnectionDetails, wantDetails)
		}
	})

	t.Run("UsesSpecNameAndCustomSecretKeysWhenSet", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "custom-creds", Namespace: "default"},
			Data: map[string][]byte{
				"registryUser": []byte("custom-user"),
				"registryPass": []byte("custom-pass"),
			},
		}
		kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()

		var gotBody runpodclient.CreateContainerRegistryAuthRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "cra-created"})
		}))
		defer server.Close()

		customName := "custom-name"
		cra := &v1alpha1.ContainerRegistryAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "resource-name", Namespace: "default"},
			Spec: v1alpha1.ContainerRegistryAuthSpec{ForProvider: v1alpha1.ContainerRegistryAuthParameters{
				Name: &customName,
				CredentialsSecretRef: v1alpha1.ContainerRegistrySecretRef{
					Name:        "custom-creds",
					UsernameKey: "registryUser",
					PasswordKey: "registryPass",
				},
			}},
		}

		e := &external{client: newTestClient(t, server), kube: kube, log: logr.Discard()}
		if _, err := e.Create(context.Background(), cra); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if gotBody.Name != "custom-name" || gotBody.Username != "custom-user" || gotBody.Password != "custom-pass" {
			t.Fatalf("Create() request = %#v", gotBody)
		}
	})

	t.Run("MissingSecretReturnsError", func(t *testing.T) {
		kube := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Create() should not call the RunPod API when the secret is missing")
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr", Namespace: "default"},
			Spec: v1alpha1.ContainerRegistryAuthSpec{ForProvider: v1alpha1.ContainerRegistryAuthParameters{
				CredentialsSecretRef: v1alpha1.ContainerRegistrySecretRef{Name: "missing-secret"},
			}},
		}

		e := &external{client: newTestClient(t, server), kube: kube, log: logr.Discard()}
		_, err := e.Create(context.Background(), cra)
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errGetCredentialsSecret) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errGetCredentialsSecret)
		}
	})

	t.Run("SecretInOtherNamespaceIsNotFound", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr-creds", Namespace: "other-namespace"},
			Data: map[string][]byte{
				"username": []byte("my-user"),
				"password": []byte("my-pass"),
			},
		}
		kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Create() should not call the RunPod API when the secret is in a different namespace")
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr", Namespace: "default"},
			Spec: v1alpha1.ContainerRegistryAuthSpec{ForProvider: v1alpha1.ContainerRegistryAuthParameters{
				CredentialsSecretRef: v1alpha1.ContainerRegistrySecretRef{Name: "ghcr-creds"},
			}},
		}

		e := &external{client: newTestClient(t, server), kube: kube, log: logr.Discard()}
		if _, err := e.Create(context.Background(), cra); err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
	})

	t.Run("MissingSecretKeyReturnsError", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr-creds", Namespace: "default"},
			Data: map[string][]byte{
				"username": []byte("my-user"),
				// password key missing
			},
		}
		kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Create() should not call the RunPod API when a secret key is missing")
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr", Namespace: "default"},
			Spec: v1alpha1.ContainerRegistryAuthSpec{ForProvider: v1alpha1.ContainerRegistryAuthParameters{
				CredentialsSecretRef: v1alpha1.ContainerRegistrySecretRef{Name: "ghcr-creds"},
			}},
		}

		e := &external{client: newTestClient(t, server), kube: kube, log: logr.Discard()}
		_, err := e.Create(context.Background(), cra)
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "missing key") {
			t.Fatalf("Create() error = %q, want it to mention missing key", err.Error())
		}
	})

	t.Run("CreateFailureReturnsError", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr-creds", Namespace: "default"},
			Data: map[string][]byte{
				"username": []byte("my-user"),
				"password": []byte("my-pass"),
			},
		}
		kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr", Namespace: "default"},
			Spec: v1alpha1.ContainerRegistryAuthSpec{ForProvider: v1alpha1.ContainerRegistryAuthParameters{
				CredentialsSecretRef: v1alpha1.ContainerRegistrySecretRef{Name: "ghcr-creds"},
			}},
		}

		e := &external{client: newTestClient(t, server), kube: kube, log: logr.Discard()}
		_, err := e.Create(context.Background(), cra)
		if err == nil {
			t.Fatal("Create() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errCreateContainerRegistryAuth) {
			t.Fatalf("Create() error = %q, want wrapped %q", err.Error(), errCreateContainerRegistryAuth)
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("IsANoOp", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{}
		meta.SetExternalName(cra, "cra-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Update(context.Background(), cra); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if calls != 0 {
			t.Fatalf("Update() HTTP calls = %d, want 0", calls)
		}
	})
}

func TestDelete(t *testing.T) {
	t.Run("DeletesAuth", func(t *testing.T) {
		var gotMethod, gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{}
		meta.SetExternalName(cra, "cra-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), cra); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if gotMethod != http.MethodDelete || gotPath != "/containerregistryauth/cra-123" {
			t.Fatalf("Delete() = %s %s, want DELETE /containerregistryauth/cra-123", gotMethod, gotPath)
		}
	})

	t.Run("EmptyExternalNameSkipsHTTPCalls", func(t *testing.T) {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		defer server.Close()

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), &v1alpha1.ContainerRegistryAuth{}); err != nil {
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

		cra := &v1alpha1.ContainerRegistryAuth{}
		meta.SetExternalName(cra, "cra-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		if _, err := e.Delete(context.Background(), cra); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("ServerErrorReturnsError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cra := &v1alpha1.ContainerRegistryAuth{}
		meta.SetExternalName(cra, "cra-123")

		e := &external{client: newTestClient(t, server), log: logr.Discard()}
		_, err := e.Delete(context.Background(), cra)
		if err == nil {
			t.Fatal("Delete() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), errDeleteContainerRegistryAuth) {
			t.Fatalf("Delete() error = %q, want wrapped %q", err.Error(), errDeleteContainerRegistryAuth)
		}
	})
}
