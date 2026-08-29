package providerconfig

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

// newPingServer starts an httptest server that answers every GET /pods
// with the given status code, standing in for the RunPod API so credential
// validation tests stay hermetic (no real network call).
func newPingServer(t *testing.T, status int) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

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
		// The namespaced ProviderConfig's secretRef has no namespace field:
		// the secret is always resolved in the ProviderConfig's own
		// namespace, so it must live in the same namespace as the secret
		// used by these tests (newSecret uses "crossplane-system").
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "crossplane-system"},
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
	srv := newPingServer(t, http.StatusOK)

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if res.RequeueAfter != requeueInterval {
		t.Fatalf("Reconcile() RequeueAfter = %v, want %v", res.RequeueAfter, requeueInterval)
	}

	got := &v1beta1.ProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: "crossplane-system"}, got); err != nil {
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

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
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
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: "crossplane-system"}, got); err != nil {
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

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
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
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: "crossplane-system"}, got); err != nil {
		t.Fatalf("Get(ProviderConfig) error = %v", err)
	}
	cond := got.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionFalse || cond.Reason != xpv2.ReasonUnavailable {
		t.Fatalf("condition = %#v, want Ready/False/Unavailable", cond)
	}
}

// TestReconcileRevokedCredentialsSetsUnavailableAndReturnsError is the core
// A2 guarantee: a secret that exists and is non-empty, but that the RunPod
// API itself rejects (e.g. a revoked key), must NOT be reported Available.
func TestReconcileRevokedCredentialsSetsUnavailableAndReturnsError(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")
	secret := newSecret("revoked-key")
	srv := newPingServer(t, http.StatusUnauthorized)

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errInvalidCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errInvalidCredentials)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %#v, want empty result", res)
	}

	got := &v1beta1.ProviderConfig{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: "crossplane-system"}, got); err != nil {
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
	srv := newPingServer(t, http.StatusOK)

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
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
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

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
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

// TestReconcileSkipsStatusUpdateWhenReadyConditionUnchanged is the A4
// guarantee: reconciling twice with the same outcome must not repeat the
// status write (SetConditions already preserves LastTransitionTime for an
// equal-state Ready condition, so re-writing status on every 5-minute
// requeue is pure conflict churn with no observable benefit).
func TestReconcileSkipsStatusUpdateWhenReadyConditionUnchanged(t *testing.T) {
	s := testScheme(t)
	pc := newProviderConfig("default")
	secret := newSecret("test-key")
	srv := newPingServer(t, http.StatusOK)

	var statusUpdateCount int
	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc, secret).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					statusUpdateCount++
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if statusUpdateCount != 1 {
		t.Fatalf("status update count after first Reconcile() = %d, want 1", statusUpdateCount)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if statusUpdateCount != 1 {
		t.Fatalf("status update count after second Reconcile() = %d, want 1 (unchanged Ready condition must skip the write)", statusUpdateCount)
	}
}

// TestClusterReconcileSkipsStatusUpdateWhenReadyConditionUnchanged mirrors
// TestReconcileSkipsStatusUpdateWhenReadyConditionUnchanged for the
// cluster-scoped reconciler.
func TestClusterReconcileSkipsStatusUpdateWhenReadyConditionUnchanged(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")
	secret := newSecret("test-key")
	srv := newPingServer(t, http.StatusOK)

	var statusUpdateCount int
	kube := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pc, secret).
		WithStatusSubresource(pc).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					statusUpdateCount++
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if statusUpdateCount != 1 {
		t.Fatalf("status update count after first Reconcile() = %d, want 1", statusUpdateCount)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if statusUpdateCount != 1 {
		t.Fatalf("status update count after second Reconcile() = %d, want 1 (unchanged Ready condition must skip the write)", statusUpdateCount)
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
	srv := newPingServer(t, http.StatusOK)

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}

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

// TestClusterReconcileRevokedCredentialsSetsUnavailableAndReturnsError
// mirrors TestReconcileRevokedCredentialsSetsUnavailableAndReturnsError for
// the cluster-scoped reconciler: a revoked key must not be Available.
func TestClusterReconcileRevokedCredentialsSetsUnavailableAndReturnsError(t *testing.T) {
	s := testScheme(t)
	pc := newClusterProviderConfig("default")
	secret := newSecret("revoked-key")
	srv := newPingServer(t, http.StatusUnauthorized)

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(pc, secret).WithStatusSubresource(pc).Build()
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), errInvalidCredentials) {
		t.Fatalf("Reconcile() error = %q, want to contain %q", err.Error(), errInvalidCredentials)
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
	srv := newPingServer(t, http.StatusOK)

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
	r := &ClusterReconciler{kube: kube, zapLogger: zap.NewNop(), baseURL: srv.URL}

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
			Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
				return errors.New("etcd unavailable")
			},
		}).
		Build()
	r := &Reconciler{kube: kube, zapLogger: zap.NewNop()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "default", Namespace: "crossplane-system"}})
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
			Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
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
