package providerconfig

import (
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/providerconfig"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
)

// SetupUsageTracking registers crossplane-runtime's providerconfig usage
// reconciler for both ProviderConfig and ClusterProviderConfig, which
// maintains status.users and an in-use finalizer on each so they cannot be
// deleted while Pods or Endpoints still reference them — otherwise those
// resources could never Connect again and their own deletion would hang on
// the finalizer. There is deliberately no cluster-scoped usage type: every
// usage record is a namespaced ProviderConfigUsage living beside the
// namespaced MR that created it, and its typed providerConfigRef records
// which kind of config (ProviderConfig or ClusterProviderConfig) it refers
// to.
func SetupUsageTracking(mgr ctrl.Manager, log logr.Logger) error {
	if err := setupNamespacedUsageTracking(mgr, log); err != nil {
		return err
	}
	return setupClusterUsageTracking(mgr, log)
}

func setupNamespacedUsageTracking(mgr ctrl.Manager, log logr.Logger) error {
	of := xpresource.ProviderConfigKinds{
		Config:    v1beta1.ProviderConfigGroupVersionKind,
		Usage:     v1beta1.ProviderConfigUsageGroupVersionKind,
		UsageList: v1beta1.ProviderConfigUsageListGroupVersionKind,
	}

	r := providerconfig.NewReconciler(mgr, of,
		providerconfig.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(providerconfig.ControllerName(v1beta1.ProviderConfigGroupKind)).
		For(&v1beta1.ProviderConfig{}).
		Watches(&v1beta1.ProviderConfigUsage{}, &xpresource.EnqueueRequestForProviderConfig{Kind: v1beta1.ProviderConfigKind}).
		Complete(r)
}

func setupClusterUsageTracking(mgr ctrl.Manager, log logr.Logger) error {
	of := xpresource.ProviderConfigKinds{
		Config:    v1beta1.ClusterProviderConfigGroupVersionKind,
		Usage:     v1beta1.ProviderConfigUsageGroupVersionKind,
		UsageList: v1beta1.ProviderConfigUsageListGroupVersionKind,
	}

	r := providerconfig.NewReconciler(mgr, of,
		providerconfig.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(providerconfig.ControllerName(v1beta1.ClusterProviderConfigGroupKind)).
		For(&v1beta1.ClusterProviderConfig{}).
		Watches(&v1beta1.ProviderConfigUsage{}, &xpresource.EnqueueRequestForProviderConfig{Kind: v1beta1.ClusterProviderConfigKind}).
		Complete(r)
}
