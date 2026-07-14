package endpoint

import (
	"context"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	managed "github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errNotEndpoint           = "managed resource is not an Endpoint"
	errMissingProviderConfig = "endpoint is missing providerConfigRef"
	errGetProviderConfig     = "cannot get ProviderConfig"
	errCreateClient          = "cannot create RunPod client from ProviderConfig"
)

type connector struct {
	kube client.Client
	log  logr.Logger
}

func (c *connector) Connect(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
	ep, ok := mg.(*v1alpha1.Endpoint)
	if !ok {
		return nil, errors.New(errNotEndpoint)
	}

	ref := ep.GetProviderConfigReference()
	if ref == nil || ref.Name == "" {
		return nil, errors.New(errMissingProviderConfig)
	}

	pc := &v1beta1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetProviderConfig)
	}

	rc, err := runpodclient.ClientFromProviderConfig(ctx, c.kube, pc)
	if err != nil {
		return nil, errors.Wrap(err, errCreateClient)
	}

	return &external{
		client: rc,
		log:    c.log.WithValues("endpoint", ep.GetName()),
	}, nil
}

// Setup registers the Endpoint managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger) error {
	name := xpresource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind("Endpoint"))
	r := managed.NewReconciler(
		mgr,
		name,
		managed.WithExternalConnecter(&connector{
			kube: mgr.GetClient(),
			log:  log,
		}),
		managed.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Endpoint{}).
		Complete(r)
}
