// Package register wires managed-resource controllers into the manager,
// deduplicating the reconciler setup shared by every kind.
package register

import (
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
)

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
