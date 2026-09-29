package register

import (
	"context"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	testErrNotKind         = "managed resource is not a Pod"
	testErrMissingProvider = "pod is missing providerConfigRef"
)

func credsSecret(namespace, key string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: namespace},
		Data:       map[string][]byte{"apiKey": []byte(key)},
	}
}

func clusterPC() *v1beta1.ClusterProviderConfig {
	return &v1beta1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: v1beta1.ProviderConfigSpec{
			Credentials: xpv2.CommonCredentialSelectors{
				SecretRef: &xpv2.SecretKeySelector{
					SecretReference: xpv2.SecretReference{Name: "runpod-creds", Namespace: "crossplane-system"},
					Key:             "apiKey",
				},
			},
		},
	}
}

func namespacedPC() *v1beta1.ProviderConfig {
	return &v1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "team-config", Namespace: "team-a"},
		Spec: v1beta1.LocalProviderConfigSpec{
			Credentials: v1beta1.LocalCredentialSelectors{
				SecretRef: &xpv2.LocalSecretKeySelector{
					LocalSecretReference: xpv2.LocalSecretReference{Name: "runpod-creds"},
					Key:                  "apiKey",
				},
			},
		},
	}
}

func newPod(namespace string, ref *xpv2.ProviderConfigReference) *v1alpha1.Pod {
	p := &v1alpha1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: namespace, UID: types.UID("uid-123")},
	}
	p.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	p.SetProviderConfigReference(ref)
	return p
}

// newConnector builds a Connector over a fake cluster holding objs. The
// returned pointer records the pod NewExternal was invoked with.
func newConnector(t *testing.T, objs ...client.Object) (*Connector[*v1alpha1.Pod], client.Client, **v1alpha1.Pod) {
	t.Helper()

	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, v1alpha1.AddToScheme, v1beta1.AddToScheme} {
		if err := add(s); err != nil {
			t.Fatalf("build scheme: %v", err)
		}
	}

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	var built *v1alpha1.Pod
	return &Connector[*v1alpha1.Pod]{
		Kube:                     kube,
		Usage:                    xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		Log:                      logr.Discard(),
		ErrNotKind:               testErrNotKind,
		ErrMissingProviderConfig: testErrMissingProvider,
		NewExternal: func(_ *runpodclient.Client, cr *v1alpha1.Pod, _ logr.Logger) managed.ExternalClient {
			built = cr
			return nil
		},
	}, kube, &built
}

func TestConnectorConnect(t *testing.T) {
	tests := map[string]struct {
		objs      []client.Object
		namespace string
		ref       *xpv2.ProviderConfigReference
		// wantUsageKind is the providerConfigRef kind the recorded
		// ProviderConfigUsage must carry.
		wantUsageKind string
	}{
		"ClusterProviderConfigTracksUsage": {
			objs:          []client.Object{credsSecret("crossplane-system", "test-key"), clusterPC()},
			namespace:     "default",
			ref:           &xpv2.ProviderConfigReference{Name: "default", Kind: v1beta1.ClusterProviderConfigKind},
			wantUsageKind: v1beta1.ClusterProviderConfigKind,
		},
		// The CRD's whole-object default only applies when providerConfigRef
		// is omitted entirely, and the usage tracker rejects an empty Kind,
		// so the connector must normalize Kind itself before tracking, or
		// objects with a bare {name: default} ref (e.g. stored pre-v2 CRs)
		// can never Connect.
		"EmptyKindDefaultsToClusterProviderConfig": {
			objs:          []client.Object{credsSecret("crossplane-system", "test-key"), clusterPC()},
			namespace:     "default",
			ref:           &xpv2.ProviderConfigReference{Name: "default"},
			wantUsageKind: v1beta1.ClusterProviderConfigKind,
		},
		// A same-named decoy secret lives in a DIFFERENT namespace. The
		// namespaced ProviderConfig's secretRef has no namespace field, so
		// resolution must use the secret in the ProviderConfig's own
		// namespace ("team-a") and never reach the decoy.
		"NamespacedProviderConfigResolvesInOwnNamespace": {
			objs:          []client.Object{credsSecret("other-team", "other-teams-runpod-key"), credsSecret("team-a", "test-key"), namespacedPC()},
			namespace:     "team-a",
			ref:           &xpv2.ProviderConfigReference{Name: "team-config", Kind: v1beta1.ProviderConfigKind},
			wantUsageKind: v1beta1.ProviderConfigKind,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c, kube, built := newConnector(t, tc.objs...)
			pod := newPod(tc.namespace, tc.ref.DeepCopy())

			if _, err := c.Connect(context.Background(), pod); err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
			if *built != pod {
				t.Fatal("NewExternal was not called with the reconciled managed resource")
			}

			// A ProviderConfigUsage must exist so the ProviderConfig cannot
			// be deleted out from under managed resources that still need it.
			pcus := &v1beta1.ProviderConfigUsageList{}
			if err := kube.List(context.Background(), pcus); err != nil {
				t.Fatalf("List(ProviderConfigUsage) error = %v", err)
			}
			if len(pcus.Items) != 1 {
				t.Fatalf("ProviderConfigUsage count = %d, want 1", len(pcus.Items))
			}
			got := pcus.Items[0]
			if r := got.GetProviderConfigReference(); r.Name != tc.ref.Name || r.Kind != tc.wantUsageKind {
				t.Fatalf("ProviderConfigUsage providerConfigRef = %#v, want %s/%s", r, tc.wantUsageKind, tc.ref.Name)
			}
			if r := got.GetResourceReference(); r.Name != "my-pod" || r.Kind != "Pod" {
				t.Fatalf("ProviderConfigUsage resourceRef = %#v, want Pod/my-pod", r)
			}
		})
	}
}

// TestConnectorNamespacedProviderConfigCannotUseSecretFromAnotherNamespace is
// the negative half of the tenancy guarantee: with no secret in the
// ProviderConfig's own namespace, a same-named secret living elsewhere must
// NOT be reachable, even though it would satisfy the same secretRef.name.
func TestConnectorNamespacedProviderConfigCannotUseSecretFromAnotherNamespace(t *testing.T) {
	c, _, _ := newConnector(t, credsSecret("other-team", "other-teams-runpod-key"), namespacedPC())
	pod := newPod("team-a", &xpv2.ProviderConfigReference{Name: "team-config", Kind: v1beta1.ProviderConfigKind})

	if _, err := c.Connect(context.Background(), pod); err == nil {
		t.Fatal("Connect() error = nil, want error (secret only exists in another namespace)")
	}
}

func TestConnectorConnectErrors(t *testing.T) {
	tests := map[string]struct {
		mg      xpresource.Managed
		wantErr string
	}{
		"UnsupportedProviderConfigKind": {
			mg:      newPod("default", &xpv2.ProviderConfigReference{Name: "default", Kind: "SomethingElse"}),
			wantErr: "unsupported providerConfigRef kind",
		},
		"NilProviderConfigRef": {
			mg:      &v1alpha1.Pod{},
			wantErr: testErrMissingProvider,
		},
		"EmptyProviderConfigRefName": {
			mg:      newPod("default", &xpv2.ProviderConfigReference{}),
			wantErr: testErrMissingProvider,
		},
		"WrongManagedType": {
			mg:      &v1alpha1.Endpoint{},
			wantErr: testErrNotKind,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c, _, built := newConnector(t)

			_, err := c.Connect(context.Background(), tc.mg)
			if err == nil {
				t.Fatal("Connect() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Connect() error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
			if *built != nil {
				t.Fatal("NewExternal was called despite Connect() failing")
			}
		})
	}
}
