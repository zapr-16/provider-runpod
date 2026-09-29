package clients

import (
	"context"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
)

const (
	errGetProviderConfig = "cannot get ProviderConfig"
	errCreateClient      = "cannot create RunPod client from ProviderConfig"
	errUnsupportedPCKind = "unsupported providerConfigRef kind"
)

// NormalizeProviderConfigRefKind defaults an empty providerConfigRef Kind to
// ClusterProviderConfig in place. Connectors must call this BEFORE usage
// tracking: the CRD's whole-object default fills in the Kind only when
// providerConfigRef is omitted entirely (structural-schema defaulting never
// backfills individual subfields), and the runtime's usage tracker rejects a
// reference with an empty Kind outright — so a stored object carrying a bare
// {name: ...} ref could otherwise never Connect.
func NormalizeProviderConfigRefKind(ref *xpv2.ProviderConfigReference) {
	if ref != nil && ref.Kind == "" {
		ref.Kind = v1beta1.ClusterProviderConfigKind
	}
}

// ClientForProviderConfigRef resolves a RunPod client from a typed
// providerConfigRef: a cluster-scoped ClusterProviderConfig, or a namespaced
// ProviderConfig looked up in the referencing managed resource's own
// namespace. The Kind must already be set (see NormalizeProviderConfigRefKind).
//
// A namespaced ProviderConfig's secretRef has no namespace field, so its
// secret is always read from the ProviderConfig's own namespace - a
// namespace-scoped tenant can never read a secret living elsewhere in the
// cluster.
func ClientForProviderConfigRef(ctx context.Context, kube client.Client, namespace string, ref xpv2.ProviderConfigReference) (*Client, error) {
	var pc interface {
		client.Object
		Credentials() xpv2.CommonCredentialSelectors
	}
	key := types.NamespacedName{Name: ref.Name}
	switch ref.Kind {
	case v1beta1.ClusterProviderConfigKind:
		pc = &v1beta1.ClusterProviderConfig{}
	case v1beta1.ProviderConfigKind:
		pc = &v1beta1.ProviderConfig{}
		key.Namespace = namespace
	default:
		return nil, errors.Errorf("%s: %q", errUnsupportedPCKind, ref.Kind)
	}

	if err := kube.Get(ctx, key, pc); err != nil {
		return nil, errors.Wrap(err, errGetProviderConfig)
	}
	rc, err := ClientFromCredentials(ctx, kube, pc.Credentials())
	if err != nil {
		return nil, errors.Wrap(err, errCreateClient)
	}
	return rc, nil
}
