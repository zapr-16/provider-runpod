package clients

import (
	"context"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := v1beta1.AddToScheme(s); err != nil {
		t.Fatalf("add v1beta1 scheme: %v", err)
	}
	return s
}

func newLocalProviderConfig(name, namespace, secretName string) *v1beta1.ProviderConfig {
	return &v1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1beta1.LocalProviderConfigSpec{
			Credentials: v1beta1.LocalCredentialSelectors{
				SecretRef: &xpv2.LocalSecretKeySelector{
					LocalSecretReference: xpv2.LocalSecretReference{Name: secretName},
					Key:                  "apiKey",
				},
			},
		},
	}
}

// TestClientFromProviderConfigResolvesSecretInOwnNamespaceOnly is the core
// tenancy guarantee for the namespaced ProviderConfig: its secretRef has no
// namespace field, so resolution MUST use the ProviderConfig's own namespace
// even when a same-named secret exists elsewhere holding different data.
func TestClientFromProviderConfigResolvesSecretInOwnNamespaceOnly(t *testing.T) {
	s := testScheme(t)

	ownSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "team-a"},
		Data:       map[string][]byte{"apiKey": []byte("own-team-key")},
	}
	// Same name, different namespace, different value: if resolution ever
	// leaked cross-namespace this would be picked up instead.
	otherSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "other-team"},
		Data:       map[string][]byte{"apiKey": []byte("other-teams-runpod-key")},
	}
	pc := newLocalProviderConfig("team-config", "team-a", "runpod-creds")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(ownSecret, otherSecret, pc).Build()

	c, err := ClientFromProviderConfig(context.Background(), kube, pc)
	if err != nil {
		t.Fatalf("ClientFromProviderConfig() error = %v", err)
	}
	if c.apiKey != "own-team-key" {
		t.Fatalf("apiKey = %q, want %q (must resolve secret in PC's own namespace)", c.apiKey, "own-team-key")
	}
}

// TestClientFromProviderConfigCannotReachSecretInAnotherNamespace asserts the
// negative case explicitly: with no secret in the PC's own namespace, a
// same-named secret living in another namespace must NOT be reachable.
func TestClientFromProviderConfigCannotReachSecretInAnotherNamespace(t *testing.T) {
	s := testScheme(t)

	otherSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "other-team"},
		Data:       map[string][]byte{"apiKey": []byte("other-teams-runpod-key")},
	}
	pc := newLocalProviderConfig("team-config", "team-a", "runpod-creds")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(otherSecret, pc).Build()

	if _, err := ClientFromProviderConfig(context.Background(), kube, pc); err == nil {
		t.Fatal("ClientFromProviderConfig() error = nil, want error (no secret in PC's own namespace)")
	}
}
