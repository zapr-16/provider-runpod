package v1beta1

import (
	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A ProviderConfigUsage indicates that a managed resource is using a
// ProviderConfig or ClusterProviderConfig. Crossplane's in-use protection
// blocks deletion of the referenced config while usages of it exist. There
// is deliberately no cluster-scoped usage type: usages always live in the
// namespace of the managed resource that created them, and the typed
// providerConfigRef records which kind of config (ProviderConfig or
// ClusterProviderConfig) is referenced.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="CONFIG-NAME",type="string",JSONPath=".providerConfigRef.name"
// +kubebuilder:printcolumn:name="RESOURCE-KIND",type="string",JSONPath=".resourceRef.kind"
// +kubebuilder:printcolumn:name="RESOURCE-NAME",type="string",JSONPath=".resourceRef.name"
type ProviderConfigUsage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	xpv2.TypedProviderConfigUsage `json:",inline"`
}

// ProviderConfigUsageList contains a list of ProviderConfigUsage resources.
// +kubebuilder:object:root=true
type ProviderConfigUsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfigUsage `json:"items"`
}

// GetProviderConfigReference returns the referenced ProviderConfig.
func (p *ProviderConfigUsage) GetProviderConfigReference() xpv2.ProviderConfigReference {
	return p.ProviderConfigReference
}

// SetProviderConfigReference sets the referenced ProviderConfig.
func (p *ProviderConfigUsage) SetProviderConfigReference(r xpv2.ProviderConfigReference) {
	p.ProviderConfigReference = r
}

// GetResourceReference returns the managed resource using the ProviderConfig.
func (p *ProviderConfigUsage) GetResourceReference() xpv2.TypedReference {
	return p.ResourceReference
}

// SetResourceReference sets the managed resource using the ProviderConfig.
func (p *ProviderConfigUsage) SetResourceReference(r xpv2.TypedReference) {
	p.ResourceReference = r
}

// GetItems returns the list items as the runtime's ProviderConfigUsage
// interface, as required by the providerconfig usage reconciler.
func (p *ProviderConfigUsageList) GetItems() []resource.ProviderConfigUsage {
	items := make([]resource.ProviderConfigUsage, len(p.Items))
	for i := range p.Items {
		items[i] = &p.Items[i]
	}
	return items
}

var (
	_ resource.TypedProviderConfigUsage = &ProviderConfigUsage{}
	_ resource.ProviderConfigUsageList  = &ProviderConfigUsageList{}
)

func init() {
	SchemeBuilder.Register(&ProviderConfigUsage{}, &ProviderConfigUsageList{})
}
