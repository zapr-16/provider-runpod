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
	errGetProviderConfig = "cannot get ProviderConfig"
	errReadCredentials   = "cannot read ProviderConfig credentials"
	errUpdateStatus      = "cannot update ProviderConfig status"

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

// validateCredentials attempts to build a RunPod client from the given
// credentials, setting Available/Unavailable on status accordingly. It
// returns an error if credentials could not be read, so the reconciler can
// still persist the Unavailable status before returning.
func validateCredentials(ctx context.Context, kube client.Client, creds xpv2.CommonCredentialSelectors, status credentialStatus) error {
	if _, err := runpodclient.ClientFromCredentials(ctx, kube, creds); err != nil {
		status.SetConditions(xpv2.Unavailable())
		return errors.Wrap(err, errReadCredentials)
	}

	status.SetConditions(xpv2.Available())
	return nil
}

// Reconciler reconciles namespaced ProviderConfig resources.
type Reconciler struct {
	kube      client.Client
	zapLogger *zap.Logger
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

	pc := &v1beta1.ProviderConfig{}
	if err := r.kube.Get(ctx, req.NamespacedName, pc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, errGetProviderConfig)
	}

	validateErr := validateCredentials(ctx, r.kube, pc.Spec.Credentials, &pc.Status)
	if validateErr != nil {
		log.Error("provider config is not ready", zap.Error(validateErr))
	}
	if err := r.kube.Status().Update(ctx, pc); err != nil {
		// Aggregate so a failed status write never hides WHY the config was
		// unavailable; NewAggregate drops the nil when credentials were fine.
		return ctrl.Result{}, kerrors.NewAggregate([]error{validateErr, errors.Wrap(err, errUpdateStatus)})
	}
	if validateErr != nil {
		return ctrl.Result{}, validateErr
	}

	log.Info("provider config is ready")
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// ClusterReconciler reconciles cluster-scoped ClusterProviderConfig
// resources.
type ClusterReconciler struct {
	kube      client.Client
	zapLogger *zap.Logger
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

	pc := &v1beta1.ClusterProviderConfig{}
	if err := r.kube.Get(ctx, req.NamespacedName, pc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, errGetProviderConfig)
	}

	validateErr := validateCredentials(ctx, r.kube, pc.Spec.Credentials, &pc.Status)
	if validateErr != nil {
		log.Error("cluster provider config is not ready", zap.Error(validateErr))
	}
	if err := r.kube.Status().Update(ctx, pc); err != nil {
		// Aggregate so a failed status write never hides WHY the config was
		// unavailable; NewAggregate drops the nil when credentials were fine.
		return ctrl.Result{}, kerrors.NewAggregate([]error{validateErr, errors.Wrap(err, errUpdateStatus)})
	}
	if validateErr != nil {
		return ctrl.Result{}, validateErr
	}

	log.Info("cluster provider config is ready")
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}
