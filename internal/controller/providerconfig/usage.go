package providerconfig

import (
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/providerconfig"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	if err := setupUsageTracking(mgr, log, v1beta1.ProviderConfigGroupVersionKind, v1beta1.ProviderConfigGroupKind, v1beta1.ProviderConfigKind, &v1beta1.ProviderConfig{}); err != nil {
		return err
	}
	return setupUsageTracking(mgr, log, v1beta1.ClusterProviderConfigGroupVersionKind, v1beta1.ClusterProviderConfigGroupKind, v1beta1.ClusterProviderConfigKind, &v1beta1.ClusterProviderConfig{})
}

// setupUsageTracking registers the usage reconciler for one config kind.
func setupUsageTracking(mgr ctrl.Manager, log logr.Logger, configGVK schema.GroupVersionKind, configGK string, kind string, config client.Object) error {
	of := xpresource.ProviderConfigKinds{
		Config:    configGVK,
		Usage:     v1beta1.ProviderConfigUsageGroupVersionKind,
		UsageList: v1beta1.ProviderConfigUsageListGroupVersionKind,
	}

	r := providerconfig.NewReconciler(mgr, of,
		providerconfig.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(providerconfig.ControllerName(configGK)).
		For(config).
		Watches(&v1beta1.ProviderConfigUsage{}, &xpresource.EnqueueRequestForProviderConfig{Kind: kind}).
		Complete(r)
}
