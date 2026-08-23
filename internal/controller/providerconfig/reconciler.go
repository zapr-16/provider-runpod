package providerconfig

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
)

const (
	errGetProviderConfig  = "cannot get ProviderConfig"
	errReadCredentials    = "cannot read ProviderConfig credentials"
	errInvalidCredentials = "RunPod API rejected ProviderConfig credentials"
	errUpdateStatus       = "cannot update ProviderConfig status"

	// requeueInterval re-validates credentials periodically: the referenced
	// Secret is not watched, so rotation or deletion would otherwise leave
	// the readiness condition stale until something else touched the
	// ProviderConfig.
	requeueInterval = 5 * time.Minute
)

// credentialStatus is implemented by both ProviderConfig and
// ClusterProviderConfig status structs.
type credentialStatus interface {
	SetConditions(c ...xpv2.Condition)
}

// providerConfigObject is implemented by both v1beta1.ProviderConfig and
// v1beta1.ClusterProviderConfig. It is the minimal surface reconcileOne
// needs to validate credentials and persist status for either kind,
// letting a single generic implementation replace the ~90 duplicated lines
// that used to live separately in Reconciler.Reconcile and
// ClusterReconciler.Reconcile.
type providerConfigObject interface {
	client.Object
	credentialStatus
	// GetCondition returns the condition of the given type, the zero
	// Condition if absent. Used to detect whether validateCredentials
	// actually changed the Ready condition, so an unchanged outcome can
	// skip the status write.
	GetCondition(ct xpv2.ConditionType) xpv2.Condition
	// Credentials resolves this object's credential selectors to a
	// xpv2.CommonCredentialSelectors ready for xpresource.ExtractSecret
	// (e.g. binding a namespace-less secretRef to the object's own
	// namespace).
	Credentials() xpv2.CommonCredentialSelectors
}

// readyConditionEqual reports whether two Ready conditions are equivalent
// for the purpose of deciding whether a status write is needed, ignoring
// LastTransitionTime: SetConditions already leaves it untouched when the
// new condition is otherwise equal to the existing one, so comparing the
// remaining fields is exactly "did anything observable change".
func readyConditionEqual(a, b xpv2.Condition) bool {
	return a.Type == b.Type && a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
}

// validateCredentials builds a RunPod client from the given credentials and
// makes a cheap authenticated call (Ping) to confirm the API key is
// actually still accepted, setting Available/Unavailable on status
// accordingly. Checking only that the secret exists and is non-empty would
// still report Available for a revoked key. It returns an error if
// credentials could not be read or were rejected, so the reconciler can
// still persist the Unavailable status before returning. baseURL, if
// non-empty, overrides the RunPod REST base URL (used by tests to point at
// an httptest server instead of the real API).
func validateCredentials(ctx context.Context, kube client.Client, creds xpv2.CommonCredentialSelectors, status credentialStatus, baseURL string) error {
	var opts []runpodclient.Option
	if baseURL != "" {
		opts = append(opts, runpodclient.WithBaseURL(baseURL))
	}

	rc, err := runpodclient.ClientFromCredentials(ctx, kube, creds, opts...)
	if err != nil {
		status.SetConditions(xpv2.Unavailable())
		return errors.Wrap(err, errReadCredentials)
	}

	if err := rc.Ping(ctx); err != nil {
		status.SetConditions(xpv2.Unavailable())
		return errors.Wrap(err, errInvalidCredentials)
	}

	status.SetConditions(xpv2.Available())
	return nil
}

// reconcileOne implements the shared body of Reconciler.Reconcile and
// ClusterReconciler.Reconcile: get the object, validate its credentials,
// persist status, and requeue. newObj must return a fresh zero-value
// pointer for kube.Get to decode into; readyMsg/notReadyMsg are the
// kind-specific log lines.
func reconcileOne[T providerConfigObject](ctx context.Context, kube client.Client, baseURL string, log *zap.Logger, req ctrl.Request, newObj func() T, readyMsg, notReadyMsg string) (ctrl.Result, error) {
	pc := newObj()
	if err := kube.Get(ctx, req.NamespacedName, pc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, errGetProviderConfig)
	}

	before := pc.GetCondition(xpv2.TypeReady)

	validateErr := validateCredentials(ctx, kube, pc.Credentials(), pc, baseURL)
	if validateErr != nil {
		log.Error(notReadyMsg, zap.Error(validateErr))
	}

	// Skip the write entirely when nothing observable changed: the
	// reconciler re-validates credentials every 5 minutes even when nothing
	// changed, and writing status on every one of those polls is pure
	// conflict churn with no benefit.
	if after := pc.GetCondition(xpv2.TypeReady); !readyConditionEqual(before, after) {
		if err := kube.Status().Update(ctx, pc); err != nil {
			// Aggregate so a failed status write never hides WHY the config
			// was unavailable; NewAggregate drops the nil when credentials
			// were fine.
			return ctrl.Result{}, kerrors.NewAggregate([]error{validateErr, errors.Wrap(err, errUpdateStatus)})
		}
	}
	if validateErr != nil {
		return ctrl.Result{}, validateErr
	}

	log.Info(readyMsg)
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// Reconciler reconciles namespaced ProviderConfig resources.
type Reconciler struct {
	kube      client.Client
	zapLogger *zap.Logger
	// baseURL overrides the RunPod REST base URL used for credential
	// validation. It is only ever set by tests, to point at an httptest
	// server instead of the real API.
	baseURL string
}

// SetupWithManager registers the namespaced ProviderConfig controller with
// the manager.
func SetupWithManager(mgr ctrl.Manager, zapLogger *zap.Logger) error {
	r := &Reconciler{
		kube:      mgr.GetClient(),
		zapLogger: zapLogger,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.ProviderConfig{}).
		Complete(r)
}

// Reconcile validates ProviderConfig credentials and updates readiness.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.zapLogger.With(zap.String("providerConfig", req.Name), zap.String("namespace", req.Namespace))

	return reconcileOne(ctx, r.kube, r.baseURL, log, req,
		func() *v1beta1.ProviderConfig { return &v1beta1.ProviderConfig{} },
		"provider config is ready", "provider config is not ready")
}

// ClusterReconciler reconciles cluster-scoped ClusterProviderConfig
// resources.
type ClusterReconciler struct {
	kube      client.Client
	zapLogger *zap.Logger
	// baseURL overrides the RunPod REST base URL used for credential
	// validation. It is only ever set by tests, to point at an httptest
	// server instead of the real API.
	baseURL string
}

// SetupClusterWithManager registers the ClusterProviderConfig controller
// with the manager.
func SetupClusterWithManager(mgr ctrl.Manager, zapLogger *zap.Logger) error {
	r := &ClusterReconciler{
		kube:      mgr.GetClient(),
		zapLogger: zapLogger,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.ClusterProviderConfig{}).
		Complete(r)
}

// Reconcile validates ClusterProviderConfig credentials and updates
// readiness.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.zapLogger.With(zap.String("clusterProviderConfig", req.Name))

	return reconcileOne(ctx, r.kube, r.baseURL, log, req,
		func() *v1beta1.ClusterProviderConfig { return &v1beta1.ClusterProviderConfig{} },
		"cluster provider config is ready", "cluster provider config is not ready")
}
