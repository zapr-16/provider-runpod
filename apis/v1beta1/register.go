package v1beta1

import (
	"reflect"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

const (
	// Group identifies the API group for this provider.
	Group = "runpod.crossplane.io"
	// Version identifies the API version for these types.
	Version = "v1beta1"
)

var (
	// SchemeGroupVersion is the group and version used to register these objects.
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}
	// SchemeBuilder registers the provider API types with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}
	// AddToScheme adds all registered types to the supplied scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKinds used by the providerconfig usage reconciler and the
// managed-resource connectors' typed providerConfigRef.Kind switch.
var (
	// ProviderConfigKind is the Kind string of the namespaced ProviderConfig type.
	ProviderConfigKind = reflect.TypeOf(ProviderConfig{}).Name()
	// ProviderConfigGroupKind is the GroupKind string of the ProviderConfig type.
	ProviderConfigGroupKind = schema.GroupKind{Group: Group, Kind: ProviderConfigKind}.String()
	// ProviderConfigGroupVersionKind is the GVK of the ProviderConfig type.
	ProviderConfigGroupVersionKind = SchemeGroupVersion.WithKind(ProviderConfigKind)

	// ClusterProviderConfigKind is the Kind string of the cluster-scoped ClusterProviderConfig type.
	ClusterProviderConfigKind = reflect.TypeOf(ClusterProviderConfig{}).Name()
	// ClusterProviderConfigGroupKind is the GroupKind string of the ClusterProviderConfig type.
	ClusterProviderConfigGroupKind = schema.GroupKind{Group: Group, Kind: ClusterProviderConfigKind}.String()
	// ClusterProviderConfigGroupVersionKind is the GVK of the ClusterProviderConfig type.
	ClusterProviderConfigGroupVersionKind = SchemeGroupVersion.WithKind(ClusterProviderConfigKind)

	// ProviderConfigUsageGroupVersionKind is the GVK of the ProviderConfigUsage type.
	ProviderConfigUsageGroupVersionKind = SchemeGroupVersion.WithKind("ProviderConfigUsage")
	// ProviderConfigUsageListGroupVersionKind is the GVK of the ProviderConfigUsageList type.
	ProviderConfigUsageListGroupVersionKind = SchemeGroupVersion.WithKind("ProviderConfigUsageList")
)
