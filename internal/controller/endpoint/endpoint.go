// Package endpoint reconciles the RunPod Endpoint managed resource.
package endpoint

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
	errNotEndpoint           = "managed resource is not an Endpoint"
	errMissingProviderConfig = "endpoint is missing providerConfigRef"
)

var _ managed.ExternalClient = (*external)(nil)

// newExternal builds the Endpoint external client from a resolved RunPod
// client.
func newExternal(rc *runpodclient.Client, cr *v1alpha1.Endpoint, log logr.Logger) managed.ExternalClient {
	return &external{
		client: rc,
		log:    log.WithValues("endpoint", cr.GetName()),
	}
}

// Setup registers the Endpoint managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger, o xpcontroller.Options) error {
	conn := &register.Connector[*v1alpha1.Endpoint]{
		Kube:                     mgr.GetClient(),
		Usage:                    xpresource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1beta1.ProviderConfigUsage{}),
		Log:                      log,
		ErrNotKind:               errNotEndpoint,
		ErrMissingProviderConfig: errMissingProviderConfig,
		NewExternal:              newExternal,
	}
	// deterministicExternalName is true: Create() always sends a name derived
	// from metadata.name and the resource UID (fieldcmp.DerivedName), which
	// lets Observe() safely recover from an ambiguous create instead of the
	// reconciler refusing to retry forever.
	return register.ManagedController(mgr, "Endpoint", &v1alpha1.Endpoint{}, &v1alpha1.EndpointList{}, conn, log, o, true)
}
