package providerconfig

import (
	"context"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

const (
	errGetProviderConfig = "cannot get ProviderConfig"
	errReadCredentials   = "cannot read ProviderConfig credentials"
	errUpdateStatus      = "cannot update ProviderConfig status"
)

// Reconciler reconciles ProviderConfig resources.
type Reconciler struct {
	kube      client.Client
	zapLogger *zap.Logger
}

// SetupWithManager registers the ProviderConfig controller with the manager.
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
	log := r.zapLogger.With(zap.String("providerConfig", req.Name))

	pc := &v1beta1.ProviderConfig{}
	if err := r.kube.Get(ctx, req.NamespacedName, pc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, errGetProviderConfig)
	}

	if _, err := runpodclient.ClientFromProviderConfig(ctx, r.kube, pc); err != nil {
		pc.Status.SetConditions(xpv1.Unavailable())
		if updateErr := r.kube.Status().Update(ctx, pc); updateErr != nil {
			return ctrl.Result{}, errors.Wrap(updateErr, errUpdateStatus)
		}
		log.Error("provider config is not ready", zap.Error(err))
		return ctrl.Result{}, errors.Wrap(err, errReadCredentials)
	}

	pc.Status.SetConditions(xpv1.Available())
	if err := r.kube.Status().Update(ctx, pc); err != nil {
		return ctrl.Result{}, errors.Wrap(err, errUpdateStatus)
	}

	log.Info("provider config is ready")
	return ctrl.Result{}, nil
}
