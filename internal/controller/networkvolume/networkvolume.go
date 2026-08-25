package networkvolume

import (
	"context"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errNotNetworkVolume      = "managed resource is not a NetworkVolume"
	errMissingProviderConfig = "network volume is missing providerConfigRef"
	errTrackUsage            = "cannot track ProviderConfigUsage"
)

type connector struct {
	kube  client.Client
	usage *xpresource.ProviderConfigUsageTracker
	log   logr.Logger
}

func (c *connector) Connect(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return nil, errors.New(errNotNetworkVolume)
	}

	ref := nv.GetProviderConfigReference()
	if ref == nil || ref.Name == "" {
		return nil, errors.New(errMissingProviderConfig)
	}

	runpodclient.NormalizeProviderConfigRefKind(ref)

	// Record the usage so Crossplane's in-use protection blocks deletion
	// of the ProviderConfig while this NetworkVolume still needs it.
	if err := c.usage.Track(ctx, nv); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}

	rc, err := runpodclient.ClientForProviderConfigRef(ctx, c.kube, nv.GetNamespace(), *ref)
	if err != nil {
		return nil, err
	}

	return &external{
		client: rc,
		log:    c.log.WithValues("networkvolume", nv.GetName()),
	}, nil
}

// Setup registers the NetworkVolume managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger) error {
	name := xpresource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind("NetworkVolume"))
	r := managed.NewReconciler(
		mgr,
		name,
		managed.WithExternalConnector(&connector{
			kube:  mgr.GetClient(),
			usage: xpresource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1beta1.ProviderConfigUsage{}),
			log:   log,
		}),
		managed.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NetworkVolume{}).
		Complete(r)
}
