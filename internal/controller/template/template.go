package template

import (
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
	errNotTemplate           = "managed resource is not a Template"
	errMissingProviderConfig = "template is missing providerConfigRef"
)

var _ managed.ExternalClient = (*external)(nil)

// Setup registers the Template managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger) error {
	conn := &register.Connector[*v1alpha1.Template]{
		Kube:                     mgr.GetClient(),
		Usage:                    xpresource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1beta1.ProviderConfigUsage{}),
		Log:                      log,
		ErrNotKind:               errNotTemplate,
		ErrMissingProviderConfig: errMissingProviderConfig,
		NewExternal: func(rc *runpodclient.Client, cr *v1alpha1.Template, log logr.Logger) managed.ExternalClient {
			return &external{
				client: rc,
				log:    log.WithValues("template", cr.GetName()),
			}
		},
	}
	return register.ManagedController(mgr, "Template", &v1alpha1.Template{}, conn, log)
}
