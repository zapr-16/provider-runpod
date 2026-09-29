package register

import (
	"context"
	"testing"

	xpcontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpfake "github.com/crossplane/crossplane-runtime/v2/pkg/resource/fake"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
)

// TestReconcilerDoesNotDefaultExternalName drives a real managed.Reconciler
// built from reconcilerOptions and asserts Observe sees an empty
// external-name on a fresh resource. The runtime's default initializer would
// set it to metadata.name, which hides the ambiguous-create recovery path
// (it only runs when the external-name is empty).
func TestReconcilerDoesNotDefaultExternalName(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, v1alpha1.AddToScheme, v1beta1.AddToScheme} {
		if err := add(s); err != nil {
			t.Fatalf("build scheme: %v", err)
		}
	}

	pod := newPod("default", &xpv2.ProviderConfigReference{Kind: v1beta1.ClusterProviderConfigKind, Name: "default"})
	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).WithStatusSubresource(pod).Build()

	observed := "<not observed>"
	reg := Registration{
		Kind:   "Pod",
		Object: &v1alpha1.Pod{},
		Connector: managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
			return &managed.ExternalClientFns{
				ObserveFn: func(_ context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
					observed = meta.GetExternalName(mg)
					return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}, nil
				},
				DisconnectFn: func(context.Context) error { return nil },
			}, nil
		}),
	}

	mgr := &xpfake.Manager{Client: kube, Scheme: s}
	r := managed.NewReconciler(mgr, xpresource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind("Pod")),
		reconcilerOptions(reg, logr.Discard(), xpcontroller.Options{})...)

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(pod)}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if observed != "" {
		t.Errorf("Observe saw external-name %q, want empty (no NameAsExternalName initializer)", observed)
	}
}
