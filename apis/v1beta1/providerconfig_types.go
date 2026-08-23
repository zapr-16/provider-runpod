package v1beta1

import (
	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// interface checks to ensure our types conform to the crossplane-runtime v2
// provider config interfaces.
var (
	_ resource.ProviderConfig = &ProviderConfig{}
	_ resource.ProviderConfig = &ClusterProviderConfig{}
)

// ProviderConfigSpec defines the desired state of a RunPod ProviderConfig.
// The credential shape (a secret reference with an "apiKey" key) is
// preserved unchanged from the pre-v2 ProviderConfig.
type ProviderConfigSpec struct {
	// Credentials used to authenticate to the RunPod REST API.
	Credentials xpv2.CommonCredentialSelectors `json:"credentials"`
}

// ProviderConfigStatus reflects the observed state of a RunPod ProviderConfig.
type ProviderConfigStatus struct {
	xpv2.ProviderConfigStatus `json:",inline"`
}

// A ProviderConfig configures credentials for the RunPod provider. It is
// namespace-scoped, so it can only be referenced by managed resources in the
// same namespace. Use ClusterProviderConfig to share credentials across
// namespaces.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type ProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderConfigSpec   `json:"spec"`
	Status ProviderConfigStatus `json:"status,omitempty"`
}

// ProviderConfigList contains a list of ProviderConfig resources.
// +kubebuilder:object:root=true
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

// SetConditions sets the supplied conditions on the ProviderConfig status.
func (p *ProviderConfig) SetConditions(c ...xpv2.Condition) {
	p.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (p *ProviderConfig) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return p.Status.GetCondition(ct)
}

// GetUsers returns the number of resources currently using the ProviderConfig.
func (p *ProviderConfig) GetUsers() int64 {
	return p.Status.Users
}

// SetUsers sets the number of resources currently using the ProviderConfig.
func (p *ProviderConfig) SetUsers(i int64) {
	p.Status.Users = i
}

// A ClusterProviderConfig configures credentials for the RunPod provider
// that can be shared across namespaces. It carries the same credential
// shape as the namespaced ProviderConfig.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=crossplane
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
type ClusterProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderConfigSpec   `json:"spec"`
	Status ProviderConfigStatus `json:"status,omitempty"`
}

// ClusterProviderConfigList contains a list of ClusterProviderConfig resources.
// +kubebuilder:object:root=true
type ClusterProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProviderConfig `json:"items"`
}

// SetConditions sets the supplied conditions on the ClusterProviderConfig status.
func (p *ClusterProviderConfig) SetConditions(c ...xpv2.Condition) {
	p.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (p *ClusterProviderConfig) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return p.Status.GetCondition(ct)
}

// GetUsers returns the number of resources currently using the ClusterProviderConfig.
func (p *ClusterProviderConfig) GetUsers() int64 {
	return p.Status.Users
}

// SetUsers sets the number of resources currently using the ClusterProviderConfig.
func (p *ClusterProviderConfig) SetUsers(i int64) {
	p.Status.Users = i
}

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
	SchemeBuilder.Register(&ClusterProviderConfig{}, &ClusterProviderConfigList{})
}
