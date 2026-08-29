// Package register wires managed-resource controllers into the manager,
// deduplicating the reconciler setup and connector logic shared by every
// kind.
package register

import (
	"context"
	"os"

	xpcontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/statemetrics"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

// Gated defers fn until gate reports gvk's CustomResourceDefinition as
// Established, so that controllers watching provider-owned CRDs can be set
// up before those CRDs exist without crash-looping. If gate is nil, fn runs
// immediately (the non-gated default). Because the gate releases fn from a
// background goroutine, there is no synchronous caller left to report a
// setup failure to; it is treated the same as any other fatal startup
// error.
func Gated(gate xpcontroller.Gate, gvk schema.GroupVersionKind, log logr.Logger, fn func() error) error {
	if gate == nil {
		return fn()
	}

	gate.Register(func() {
		if err := fn(); err != nil {
			log.Error(err, "cannot set up gated controller", "gvk", gvk)
			os.Exit(1)
		}
	}, gvk)

	return nil
}

// Registration describes one managed-resource kind to ManagedController.
type Registration struct {
	// Kind is the resource's kind within this provider's group/version
	// (e.g. "Pod").
	Kind string
	// Object is a fresh, empty instance of the kind (e.g. &v1alpha1.Pod{}).
	Object client.Object
	// List is a fresh, empty list of the kind (e.g. &v1alpha1.PodList{}),
	// used only to drive the optional state-metrics recorder.
	List xpresource.ManagedList
	// Connector builds the kind's external client per reconcile.
	Connector managed.ExternalConnector
	// DeterministicExternalName must be true only for kinds whose external
	// client always sends a deterministic create name (currently Pod and
	// Endpoint; see fieldcmp.DerivedName). It is threaded straight into
	// managed.WithDeterministicExternalName, which controls whether the
	// reconciler is willing to retry Create after a prior attempt's outcome
	// went unrecorded (meta.ExternalCreateIncomplete) instead of refusing to
	// proceed forever: with a non-deterministic name that retry could
	// silently double-create and orphan a billed resource, so it must stay
	// false for any kind whose external client does not guarantee a
	// deterministic name.
	DeterministicExternalName bool
}

// ManagedController registers a managed-resource reconciler for the kind
// described by reg. o carries the poll interval, feature flags, rate
// limiting, metric recorders, and the safe-start gate shared by every kind
// in this provider; every option is wired here once instead of being
// repeated at each call site.
//
// When o.Gate is set, controller registration is deferred until the kind's
// CustomResourceDefinition is Established, so the provider can start (and
// report healthy) before its own CRDs exist in the cluster. If registration
// fails once the gate releases it, there is no synchronous caller left to
// report the error to, so it is treated as fatal, matching how every other
// setup failure in this provider is handled at startup.
func ManagedController(mgr ctrl.Manager, reg Registration, log logr.Logger, o xpcontroller.Options) error {
	gvk := v1alpha1.SchemeGroupVersion.WithKind(reg.Kind)
	name := xpresource.ManagedKind(gvk)

	setup := func() error {
		ropts := []managed.ReconcilerOption{
			managed.WithExternalConnector(reg.Connector),
			managed.WithLogger(logging.NewLogrLogger(log)),
			managed.WithPollInterval(o.PollInterval),
			managed.WithDeterministicExternalName(reg.DeterministicExternalName),
		}
		if o.Features.Enabled(feature.EnableBetaManagementPolicies) {
			ropts = append(ropts, managed.WithManagementPolicies())
		}
		if o.MetricOptions != nil && o.MetricOptions.MRMetrics != nil {
			ropts = append(ropts, managed.WithMetricRecorder(o.MetricOptions.MRMetrics))
		}

		r := managed.NewReconciler(mgr, name, ropts...)

		// Wrap the reconciler with the provider-wide rate limiter so the
		// aggregate reconcile rate across every kind stays bounded,
		// independent of the per-controller item backoff below.
		var rec reconcile.Reconciler = r
		if o.GlobalRateLimiter != nil {
			rec = ratelimiter.NewReconciler(reg.Kind, r, o.GlobalRateLimiter)
		}

		if err := ctrl.NewControllerManagedBy(mgr).
			For(reg.Object).
			WithOptions(o.ForControllerRuntime()).
			Complete(rec); err != nil {
			return err
		}

		if o.MetricOptions != nil && o.MetricOptions.MRStateMetrics != nil {
			interval := o.MetricOptions.PollStateMetricInterval
			if interval <= 0 {
				interval = o.PollInterval
			}

			recorder := statemetrics.NewMRStateRecorder(mgr.GetClient(), logging.NewLogrLogger(log), o.MetricOptions.MRStateMetrics, reg.List, interval)
			if err := mgr.Add(recorder); err != nil {
				return err
			}
		}

		return nil
	}

	return Gated(o.Gate, gvk, log, setup)
}
