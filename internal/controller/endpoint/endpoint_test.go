package endpoint

import (
	"context"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
)

func TestConnectTracksProviderConfigUsage(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, v1alpha1.AddToScheme, v1beta1.AddToScheme} {
		if err := add(s); err != nil {
			t.Fatalf("build scheme: %v", err)
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "crossplane-system"},
		Data:       map[string][]byte{"apiKey": []byte("test-key")},
	}
	// A ClusterProviderConfig is used here because the typed
	// providerConfigRef defaults its Kind to ClusterProviderConfig
	// (matching the v2 ManagedResourceSpec kubebuilder default) when unset.
	pc := &v1beta1.ClusterProviderConfig{
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
	ep := &v1alpha1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "my-endpoint", Namespace: "default", UID: types.UID("uid-456")},
	}
	ep.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Endpoint"))
	ep.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: v1beta1.ClusterProviderConfigKind})

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(secret, pc).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	if _, err := c.Connect(context.Background(), ep); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	pcus := &v1beta1.ProviderConfigUsageList{}
	if err := kube.List(context.Background(), pcus); err != nil {
		t.Fatalf("List(ProviderConfigUsage) error = %v", err)
	}
	if len(pcus.Items) != 1 {
		t.Fatalf("ProviderConfigUsage count = %d, want 1", len(pcus.Items))
	}
	if got := pcus.Items[0].GetResourceReference(); got.Name != "my-endpoint" || got.Kind != "Endpoint" {
		t.Fatalf("ProviderConfigUsage resourceRef = %#v, want Endpoint/my-endpoint", got)
	}
}

func TestConnectDefaultsEmptyKindAndResolvesNamespacedConfig(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, v1alpha1.AddToScheme, v1beta1.AddToScheme} {
		if err := add(s); err != nil {
			t.Fatalf("build scheme: %v", err)
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "crossplane-system"},
		Data:       map[string][]byte{"apiKey": []byte("test-key")},
	}
	creds := v1beta1.ProviderConfigSpec{
		Credentials: xpv2.CommonCredentialSelectors{
			SecretRef: &xpv2.SecretKeySelector{
				SecretReference: xpv2.SecretReference{Name: "runpod-creds", Namespace: "crossplane-system"},
				Key:             "apiKey",
			},
		},
	}
	clusterPC := &v1beta1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec:       creds,
	}
	namespacedPC := &v1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "team-config", Namespace: "team-a"},
		Spec:       creds,
	}

	tests := map[string]struct {
		namespace string
		ref       *xpv2.ProviderConfigReference
	}{
		// An empty Kind must be normalized to ClusterProviderConfig by the
		// connector: the CRD's whole-object default only applies when
		// providerConfigRef is omitted entirely, and the usage tracker
		// rejects an empty Kind outright.
		"EmptyKindDefaultsToCluster": {
			namespace: "default",
			ref:       &xpv2.ProviderConfigReference{Name: "default"},
		},
		"NamespacedProviderConfigResolvedInOwnNamespace": {
			namespace: "team-a",
			ref:       &xpv2.ProviderConfigReference{Name: "team-config", Kind: v1beta1.ProviderConfigKind},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ep := &v1alpha1.Endpoint{
				ObjectMeta: metav1.ObjectMeta{Name: "my-endpoint", Namespace: tc.namespace, UID: types.UID("uid-456")},
			}
			ep.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Endpoint"))
			ep.SetProviderConfigReference(tc.ref)

			kube := fake.NewClientBuilder().WithScheme(s).WithObjects(secret, clusterPC, namespacedPC).Build()
			c := &connector{
				kube:  kube,
				usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
				log:   logr.Discard(),
			}

			if _, err := c.Connect(context.Background(), ep); err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
		})
	}
}
