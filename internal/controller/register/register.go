// Package register wires managed-resource controllers into the manager,
// deduplicating the reconciler setup and connector logic shared by every
// kind.
package register

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
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const errTrackUsage = "cannot track ProviderConfigUsage"

// Connector is a generic managed.ExternalConnector shared by every kind in
// this provider. It type-asserts the reconciled object to T, validates and
// normalizes its providerConfigRef, tracks ProviderConfigUsage, resolves a
// RunPod client for the ref, and hands off to NewExternal to build the
// kind's external client.
type Connector[T xpresource.ModernManaged] struct {
	Kube  client.Client
	Usage *xpresource.ProviderConfigUsageTracker
	Log   logr.Logger

	// ErrNotKind is returned when the reconciled object is not a T.
	ErrNotKind string
	// ErrMissingProviderConfig is returned when providerConfigRef is nil or
	// empty.
	ErrMissingProviderConfig string

	// NewExternal builds the kind's external client from a resolved RunPod
	// client, the typed managed resource, and a logger scoped to it.
	NewExternal func(rc *runpodclient.Client, cr T, log logr.Logger) managed.ExternalClient
}

var _ managed.ExternalConnector = (*Connector[*v1alpha1.Pod])(nil)

// Connect implements managed.ExternalConnector.
func (c *Connector[T]) Connect(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(T)
	if !ok {
		return nil, errors.New(c.ErrNotKind)
	}

	ref := cr.GetProviderConfigReference()
	if ref == nil || ref.Name == "" {
		return nil, errors.New(c.ErrMissingProviderConfig)
	}
	runpodclient.NormalizeProviderConfigRefKind(ref)

	// Record the usage so Crossplane's in-use protection blocks deletion
	// of the ProviderConfig while this resource still needs it.
	if err := c.Usage.Track(ctx, cr); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}

	rc, err := runpodclient.ClientForProviderConfigRef(ctx, c.Kube, cr.GetNamespace(), *ref)
	if err != nil {
		return nil, err
	}

	return c.NewExternal(rc, cr, c.Log), nil
}

// ManagedController registers a managed-resource reconciler for the given
// kind, delegating external-client construction to the kind's connector.
func ManagedController(mgr ctrl.Manager, kind string, obj client.Object, conn managed.ExternalConnector, log logr.Logger) error {
	name := xpresource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(kind))
	r := managed.NewReconciler(
		mgr,
		name,
		managed.WithExternalConnector(conn),
		managed.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(obj).
		Complete(r)
}
