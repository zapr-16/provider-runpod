// Package providerconfig reconciles the ProviderConfig, ClusterProviderConfig,
// and ProviderConfigUsage resources that supply RunPod API credentials to the
// other controllers.
package providerconfig

import (
	"context"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/go-logr/logr"
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
	errReadCredentials    = "cannot read ProviderConfig credentials"         //nolint:gosec // error message text, not a credential value
	errInvalidCredentials = "RunPod API rejected ProviderConfig credentials" //nolint:gosec // error message text, not a credential value
	errUpdateStatus       = "cannot update ProviderConfig status"

	// requeueInterval re-validates credentials periodically: the referenced
	// Secret is not watched, so rotation or deletion would otherwise leave
	// the readiness condition stale until something else touched the
	// ProviderConfig.
	requeueInterval = 5 * time.Minute
)

// providerConfigObject is implemented by both v1beta1.ProviderConfig and
// v1beta1.ClusterProviderConfig. It is the minimal surface the generic
// reconciler needs to validate credentials and persist status for either
// kind.
type providerConfigObject interface {
	client.Object
	SetConditions(c ...xpv2.Condition)
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

// validateCredentials builds a RunPod client from the object's credentials
// and makes a cheap authenticated call (Ping) to confirm the API key is
// actually still accepted, setting Available/Unavailable on its status
// accordingly. Checking only that the secret exists and is non-empty would
// still report Available for a revoked key. It returns an error if
// credentials could not be read or were rejected, so the reconciler can
// still persist the Unavailable status before returning. baseURL, if
// non-empty, overrides the RunPod REST base URL (used by tests to point at
// an httptest server instead of the real API).
func validateCredentials(ctx context.Context, kube client.Client, pc providerConfigObject, baseURL string) error {
	var opts []runpodclient.Option
	if baseURL != "" {
		opts = append(opts, runpodclient.WithBaseURL(baseURL))
	}

	rc, err := runpodclient.ClientFromCredentials(ctx, kube, pc.Credentials(), opts...)
	if err != nil {
		pc.SetConditions(xpv2.Unavailable())
		return errors.Wrap(err, errReadCredentials)
	}

	if err := rc.Ping(ctx); err != nil {
		pc.SetConditions(xpv2.Unavailable())
		return errors.Wrap(err, errInvalidCredentials)
	}

	pc.SetConditions(xpv2.Available())
	return nil
}

// reconciler validates the credentials of either ProviderConfig kind and
// reports the result as the object's Ready condition.
type reconciler[T providerConfigObject] struct {
	kube client.Client
	log  logr.Logger
	// baseURL overrides the RunPod REST base URL used for credential
	// validation. It is only ever set by tests, to point at an httptest
	// server instead of the real API.
	baseURL string
	// newObj returns a fresh zero-value object for kube.Get to decode into.
	newObj func() T
	// kind is the human-readable kind name used in log lines.
	kind string
}

// SetupWithManager registers the namespaced ProviderConfig controller with
// the manager.
func SetupWithManager(mgr ctrl.Manager, log logr.Logger) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.ProviderConfig{}).
		Complete(newProviderConfigReconciler(mgr.GetClient(), log))
}

// SetupClusterWithManager registers the ClusterProviderConfig controller
// with the manager.
func SetupClusterWithManager(mgr ctrl.Manager, log logr.Logger) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.ClusterProviderConfig{}).
		Complete(newClusterProviderConfigReconciler(mgr.GetClient(), log))
}

func newProviderConfigReconciler(kube client.Client, log logr.Logger) *reconciler[*v1beta1.ProviderConfig] {
	return &reconciler[*v1beta1.ProviderConfig]{
		kube:   kube,
		log:    log,
		newObj: func() *v1beta1.ProviderConfig { return &v1beta1.ProviderConfig{} },
		kind:   "provider config",
	}
}

func newClusterProviderConfigReconciler(kube client.Client, log logr.Logger) *reconciler[*v1beta1.ClusterProviderConfig] {
	return &reconciler[*v1beta1.ClusterProviderConfig]{
		kube:   kube,
		log:    log,
		newObj: func() *v1beta1.ClusterProviderConfig { return &v1beta1.ClusterProviderConfig{} },
		kind:   "cluster provider config",
	}
}

// Reconcile validates the ProviderConfig's credentials and updates readiness.
func (r *reconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log.WithValues("name", req.Name, "namespace", req.Namespace)

	pc := r.newObj()
	if err := r.kube.Get(ctx, req.NamespacedName, pc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, errGetProviderConfig)
	}

	before := pc.GetCondition(xpv2.TypeReady)

	validateErr := validateCredentials(ctx, r.kube, pc, r.baseURL)

	// Skip the write entirely when nothing observable changed: the
	// reconciler re-validates credentials every 5 minutes even when nothing
	// changed, and writing status on every one of those polls is pure
	// conflict churn with no benefit.
	if after := pc.GetCondition(xpv2.TypeReady); !before.Equal(after) {
		if err := r.kube.Status().Update(ctx, pc); err != nil {
			// Aggregate so a failed status write never hides WHY the config
			// was unavailable; NewAggregate drops the nil when credentials
			// were fine.
			return ctrl.Result{}, kerrors.NewAggregate([]error{validateErr, errors.Wrap(err, errUpdateStatus)})
		}
	}
	if validateErr != nil {
		return ctrl.Result{}, validateErr
	}

	log.Info(r.kind + " is ready")
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}
