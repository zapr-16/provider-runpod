package containerregistryauth

import (
	xpcontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
	"github.com/zapr-16/provider-runpod/internal/controller/register"
)

const (
	errNotContainerRegistryAuth = "managed resource is not a ContainerRegistryAuth"
	errMissingProviderConfig    = "container registry auth is missing providerConfigRef"
)

var _ managed.ExternalClient = (*external)(nil)

// Setup registers the ContainerRegistryAuth managed-resource controller
// with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger, o xpcontroller.Options) error {
	kube := mgr.GetClient()
	conn := &register.Connector[*v1alpha1.ContainerRegistryAuth]{
		Kube:                     kube,
		Usage:                    xpresource.NewProviderConfigUsageTracker(kube, &v1beta1.ProviderConfigUsage{}),
		Log:                      log,
		ErrNotKind:               errNotContainerRegistryAuth,
		ErrMissingProviderConfig: errMissingProviderConfig,
		NewExternal: func(rc *runpodclient.Client, cr *v1alpha1.ContainerRegistryAuth, log logr.Logger) managed.ExternalClient {
			return &external{
				client: rc,
				kube:   kube,
				log:    log.WithValues("containerregistryauth", cr.GetName()),
			}
		},
	}
	// deterministicExternalName is false: Create() sends metadata.name as-is,
	// so the reconciler must not retry Create after an unrecorded outcome.
	return register.ManagedController(mgr, "ContainerRegistryAuth", &v1alpha1.ContainerRegistryAuth{}, &v1alpha1.ContainerRegistryAuthList{}, conn, log, o, false)
}
