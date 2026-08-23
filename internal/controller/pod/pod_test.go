package pod

import (
	"context"
	"strings"
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

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	if err := v1beta1.AddToScheme(s); err != nil {
		t.Fatalf("add v1beta1 scheme: %v", err)
	}
	return s
}

func TestConnectTracksProviderConfigUsage(t *testing.T) {
	s := testScheme(t)

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
	pod := &v1alpha1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "default", UID: types.UID("uid-123")},
	}
	pod.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	pod.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: v1beta1.ClusterProviderConfigKind})

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(secret, pc).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	if _, err := c.Connect(context.Background(), pod); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	// A ProviderConfigUsage must exist so the ProviderConfig cannot be
	// deleted out from under managed resources that still need it.
	pcus := &v1beta1.ProviderConfigUsageList{}
	if err := kube.List(context.Background(), pcus); err != nil {
		t.Fatalf("List(ProviderConfigUsage) error = %v", err)
	}
	if len(pcus.Items) != 1 {
		t.Fatalf("ProviderConfigUsage count = %d, want 1", len(pcus.Items))
	}
	got := pcus.Items[0]
	if got.GetProviderConfigReference().Name != "default" {
		t.Fatalf("ProviderConfigUsage providerConfigRef = %q, want %q", got.GetProviderConfigReference().Name, "default")
	}
	if got.GetResourceReference().Name != "my-pod" || got.GetResourceReference().Kind != "Pod" {
		t.Fatalf("ProviderConfigUsage resourceRef = %#v, want Pod/my-pod", got.GetResourceReference())
	}
}

func TestConnectDefaultsEmptyKindToClusterProviderConfig(t *testing.T) {
	// The CRD's whole-object default only applies when providerConfigRef is
	// omitted entirely, and the usage tracker rejects an empty Kind — so the
	// connector must normalize Kind itself before tracking, or objects with
	// a bare {name: default} ref (e.g. stored pre-v2 CRs) can never Connect.
	s := testScheme(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "crossplane-system"},
		Data:       map[string][]byte{"apiKey": []byte("test-key")},
	}
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
	pod := &v1alpha1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "default", UID: types.UID("uid-123")},
	}
	pod.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	pod.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default"})

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(secret, pc).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	if _, err := c.Connect(context.Background(), pod); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	pcus := &v1beta1.ProviderConfigUsageList{}
	if err := kube.List(context.Background(), pcus); err != nil {
		t.Fatalf("List(ProviderConfigUsage) error = %v", err)
	}
	if len(pcus.Items) != 1 {
		t.Fatalf("ProviderConfigUsage count = %d, want 1", len(pcus.Items))
	}
	if got := pcus.Items[0].GetProviderConfigReference().Kind; got != v1beta1.ClusterProviderConfigKind {
		t.Fatalf("ProviderConfigUsage providerConfigRef.kind = %q, want %q", got, v1beta1.ClusterProviderConfigKind)
	}
}

func TestConnectResolvesNamespacedProviderConfig(t *testing.T) {
	s := testScheme(t)

	// A same-named decoy secret lives in a DIFFERENT namespace. The
	// namespaced ProviderConfig's secretRef has no namespace field, so
	// resolution must use the secret in the ProviderConfig's own namespace
	// ("team-a") and must never reach this one.
	decoySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "other-team"},
		Data:       map[string][]byte{"apiKey": []byte("other-teams-runpod-key")},
	}
	secretInPCNamespace := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "team-a"},
		Data:       map[string][]byte{"apiKey": []byte("test-key")},
	}
	pc := &v1beta1.ProviderConfig{
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
	pod := &v1alpha1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "team-a", UID: types.UID("uid-123")},
	}
	pod.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	pod.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "team-config", Kind: v1beta1.ProviderConfigKind})

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(decoySecret, secretInPCNamespace, pc).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	if _, err := c.Connect(context.Background(), pod); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

// TestConnectNamespacedProviderConfigCannotUseSecretFromAnotherNamespace is
// the negative half of the tenancy guarantee: with no secret in the
// ProviderConfig's own namespace, a same-named secret living elsewhere must
// NOT be reachable, even though it would satisfy the same secretRef.name.
func TestConnectNamespacedProviderConfigCannotUseSecretFromAnotherNamespace(t *testing.T) {
	s := testScheme(t)

	otherNamespaceSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "other-team"},
		Data:       map[string][]byte{"apiKey": []byte("other-teams-runpod-key")},
	}
	pc := &v1beta1.ProviderConfig{
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
	pod := &v1alpha1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "team-a", UID: types.UID("uid-123")},
	}
	pod.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	pod.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "team-config", Kind: v1beta1.ProviderConfigKind})

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(otherNamespaceSecret, pc).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	if _, err := c.Connect(context.Background(), pod); err == nil {
		t.Fatal("Connect() error = nil, want error (secret only exists in another namespace)")
	}
}

func TestConnectUnsupportedKindReturnsError(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	pod := &v1alpha1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "default", UID: types.UID("uid-123")},
	}
	pod.SetGroupVersionKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	pod.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "SomethingElse"})

	_, err := c.Connect(context.Background(), pod)
	if err == nil {
		t.Fatal("Connect() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "unsupported providerConfigRef kind") {
		t.Fatalf("Connect() error = %q, want unsupported-kind error", err.Error())
	}
}

func TestConnectMissingProviderConfigRefReturnsError(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	c := &connector{
		kube:  kube,
		usage: xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		log:   logr.Discard(),
	}

	_, err := c.Connect(context.Background(), &v1alpha1.Pod{})
	if err == nil {
		t.Fatal("Connect() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errMissingProviderConfig) {
		t.Fatalf("Connect() error = %q, want %q", err.Error(), errMissingProviderConfig)
	}
}
