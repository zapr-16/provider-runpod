package providerconfig

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

func newProviderConfig(name string) *v1beta1.ProviderConfig {
	return &v1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
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

func newClusterProviderConfig(name string) *v1beta1.ClusterProviderConfig {
	return &v1beta1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
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

func newSecret(apiKey string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "runpod-creds", Namespace: "crossplane-system"},
		Data:       map[string][]byte{"apiKey": []byte(apiKey)},
	}
}

func TestReconcileProviderConfigNotFound(t *testing.T) {
	s := testScheme(t)
	kube := fake.NewClientBuilder().WithScheme(s).Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "missing"}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result (no requeue)", res)
	}
}

func TestReconcileValidCredentialsSetsReadyAndRequeues(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")
	secret := newSecret("test-key")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if res.RequeueAfter != requeueInterval {
		t.Fatalf("Reconcile() RequeueAfter = %v, want %v", res.RequeueAfter, requeueInterval)
	}

	got := &v1beta1.ProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default"}, got); err != nil {
		t.Fatalf("Get(ProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionTrue || cond.Reason != xpv2.ReasonAvailable {
		t.Fatalf("condition = %#v, want Ready/True/Available", cond)
	}
}

func TestReconcileMissingSecretSetsUnavailableAndReturnsError(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc).WithStatusSubresource(pc).Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errReadCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errReadCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}

	got := &v1beta1.ProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default"}, got); err != nil {
		t.Fatalf("Get(ProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionFalse || cond.Reason != xpv2.ReasonUnavailable {
		t.Fatalf("condition = %#v, want Ready/False/Unavailable", cond)
	}
}

func TestReconcileEmptySecretValueSetsUnavailableAndReturnsError(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")
	secret := newSecret("   ")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errReadCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errReadCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}

	got := &v1beta1.ProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default"}, got); err != nil {
		t.Fatalf("Get(ProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionFalse || cond.Reason != xpv2.ReasonUnavailable {
		t.Fatalf("condition = %#v, want Ready/False/Unavailable", cond)
	}
}

func TestReconcileStatusUpdateFailureOnValidCredentials(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")
	secret := newSecret("test-key")

	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc, secret).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					return errors.New("status update boom")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errUpdateStatus) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errUpdateStatus)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}
}

func TestReconcileStatusUpdateFailureOnMissingCredentials(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")

	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					return errors.New("status update boom")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	// When credentials are invalid AND the status update fails, BOTH causes
	// must surface in the aggregated error: dropping the credentials cause
	// would leave operators debugging "cannot update status" with no idea
	// why the ProviderConfig was unavailable in the first place.
	if !strings.Contains(err.Error(), errUpdateStatus) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errUpdateStatus)
	}
	if !strings.Contains(err.Error(), errReadCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errReadCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}
}

func TestClusterReconcileProviderConfigNotFound(t *testing.T) {
	s := testScheme(t)
	kube := fake.NewClientBuilder().WithScheme(s).Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "missing"}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result (no requeue)", res)
	}
}

func TestClusterReconcileValidCredentialsSetsReadyAndRequeues(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")
	secret := newSecret("test-key")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if res.RequeueAfter != requeueInterval {
		t.Fatalf("Reconcile() RequeueAfter = %v, want %v", res.RequeueAfter, requeueInterval)
	}

	got := &v1beta1.ClusterProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default"}, got); err != nil {
		t.Fatalf("Get(ClusterProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionTrue || cond.Reason != xpv2.ReasonAvailable {
		t.Fatalf("condition = %#v, want Ready/True/Available", cond)
	}
}

func TestClusterReconcileMissingSecretSetsUnavailableAndReturnsError(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc).WithStatusSubresource(pc).Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errReadCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errReadCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}

	got := &v1beta1.ClusterProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default"}, got); err != nil {
		t.Fatalf("Get(ClusterProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionFalse || cond.Reason != xpv2.ReasonUnavailable {
		t.Fatalf("condition = %#v, want Ready/False/Unavailable", cond)
	}
}

func TestClusterReconcileEmptySecretValueSetsUnavailableAndReturnsError(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")
	secret := newSecret("   ")

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errReadCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errReadCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}

	got := &v1beta1.ClusterProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default"}, got); err != nil {
		t.Fatalf("Get(ClusterProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionFalse || cond.Reason != xpv2.ReasonUnavailable {
		t.Fatalf("condition = %#v, want Ready/False/Unavailable", cond)
	}
}

func TestClusterReconcileStatusUpdateFailureOnValidCredentials(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")
	secret := newSecret("test-key")

	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc, secret).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					return errors.New("status update boom")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errUpdateStatus) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errUpdateStatus)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}
}

func TestClusterReconcileStatusUpdateFailureOnMissingCredentials(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")

	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					return errors.New("status update boom")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	// When credentials are invalid AND the status update fails, BOTH causes
	// must surface in the aggregated error: dropping the credentials cause
	// would leave operators debugging "cannot update status" with no idea
	// why the ProviderConfig was unavailable in the first place.
	if !strings.Contains(err.Error(), errUpdateStatus) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errUpdateStatus)
	}
	if !strings.Contains(err.Error(), errReadCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errReadCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}
}

func TestReconcileGetErrorOtherThanNotFoundIsWrapped(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")

	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return errors.New("etcd unavailable")
			},
		}).
		Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errGetProviderConfig) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errGetProviderConfig)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}
}

func TestClusterReconcileGetErrorOtherThanNotFoundIsWrapped(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")

	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return errors.New("etcd unavailable")
			},
		}).
		Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errGetProviderConfig) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errGetProviderConfig)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}
}

// requeueIntervalIsPositive guards against a future refactor accidentally
// zeroing the interval, which would silently turn periodic re-validation off.
func TestRequeueIntervalIsPositive(t *testing.T) {
	if requeueInterval <= 0 {
		t.Fatalf("requeueInterval = %v, want > 0", requeueInterval)
	}
	if requeueInterval != 5*time.Minute {
		t.Fatalf("requeueInterval = %v, want 5m", requeueInterval)
	}
}
