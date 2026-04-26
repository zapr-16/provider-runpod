package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

const (
	// Group identifies the API group for this provider.
	Group = "runpod.crossplane.io"
	// Version identifies the v1alpha1 API version.
	Version = "v1alpha1"
)

var (
	// SchemeGroupVersion is the group and version used to register these objects.
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}
	// SchemeBuilder registers the provider API types with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}
	// AddToScheme adds all registered types to the supplied scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&Pod{}, &PodList{})
}
