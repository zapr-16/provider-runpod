package networkvolume

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
	errNotNetworkVolume      = "managed resource is not a NetworkVolume"
	errMissingProviderConfig = "network volume is missing providerConfigRef"
)

var _ managed.ExternalClient = (*external)(nil)

// Setup registers the NetworkVolume managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger, o xpcontroller.Options) error {
	conn := &register.Connector[*v1alpha1.NetworkVolume]{
		Kube:                     mgr.GetClient(),
		Usage:                    xpresource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1beta1.ProviderConfigUsage{}),
		Log:                      log,
		ErrNotKind:               errNotNetworkVolume,
		ErrMissingProviderConfig: errMissingProviderConfig,
		NewExternal: func(rc *runpodclient.Client, cr *v1alpha1.NetworkVolume, log logr.Logger) managed.ExternalClient {
			return &external{
				client: rc,
				log:    log.WithValues("networkvolume", cr.GetName()),
			}
		},
	}
	return register.ManagedController(mgr, "NetworkVolume", &v1alpha1.NetworkVolume{}, &v1alpha1.NetworkVolumeList{}, conn, log, o)
}
