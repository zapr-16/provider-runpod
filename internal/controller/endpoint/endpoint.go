package endpoint

import (
	"context"

	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
	"github.com/zapr-16/provider-runpod/internal/controller/register"
)

const (
	errNotEndpoint           = "managed resource is not an Endpoint"
	errMissingProviderConfig = "endpoint is missing providerConfigRef"
	errTrackUsage            = "cannot track ProviderConfigUsage"
)

type connector struct {
	kube  client.Client
	usage *xpresource.ProviderConfigUsageTracker
	log   logr.Logger
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

	runpodclient.NormalizeProviderConfigRefKind(ref)

	// Record the usage so Crossplane's in-use protection blocks deletion
	// of the ProviderConfig while this Endpoint still needs it.
	if err := c.usage.Track(ctx, ep); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}

	rc, err := runpodclient.ClientForProviderConfigRef(ctx, c.kube, ep.GetNamespace(), *ref)
	if err != nil {
		return nil, err
	}

	return &external{
		client: rc,
		log:    c.log.WithValues("endpoint", ep.GetName()),
	}, nil
}

// Setup registers the Endpoint managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger) error {
	conn := &connector{
		kube:  mgr.GetClient(),
		usage: xpresource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1beta1.ProviderConfigUsage{}),
		log:   log,
	}
	return register.ManagedController(mgr, "Endpoint", &v1alpha1.Endpoint{}, conn, log)
}
