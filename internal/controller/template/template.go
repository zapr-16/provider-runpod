package template

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
	errNotTemplate           = "managed resource is not a Template"
	errMissingProviderConfig = "template is missing providerConfigRef"
	errTrackUsage            = "cannot track ProviderConfigUsage"
)

type connector struct {
	kube  client.Client
	usage *xpresource.ProviderConfigUsageTracker
	log   logr.Logger
}

var (
	_ managed.ExternalConnector = (*connector)(nil)
	_ managed.ExternalClient    = (*external)(nil)
)

func (c *connector) Connect(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
	tmpl, ok := mg.(*v1alpha1.Template)
	if !ok {
		return nil, errors.New(errNotTemplate)
	}

	ref := tmpl.GetProviderConfigReference()
	if ref == nil || ref.Name == "" {
		return nil, errors.New(errMissingProviderConfig)
	}

	runpodclient.NormalizeProviderConfigRefKind(ref)

	// Record the usage so Crossplane's in-use protection blocks deletion
	// of the ProviderConfig while this Template still needs it.
	if err := c.usage.Track(ctx, tmpl); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}

	rc, err := runpodclient.ClientForProviderConfigRef(ctx, c.kube, tmpl.GetNamespace(), *ref)
	if err != nil {
		return nil, err
	}

	return &external{
		client: rc,
		log:    c.log.WithValues("template", tmpl.GetName()),
	}, nil
}

// Setup registers the Template managed-resource controller with the manager.
func Setup(mgr ctrl.Manager, log logr.Logger) error {
	conn := &connector{
		kube:  mgr.GetClient(),
		usage: xpresource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1beta1.ProviderConfigUsage{}),
		log:   log,
	}
	return register.ManagedController(mgr, "Template", &v1alpha1.Template{}, conn, log)
}
