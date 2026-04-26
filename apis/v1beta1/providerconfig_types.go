package v1beta1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderConfigSpec defines the desired state of a RunPod ProviderConfig.
type ProviderConfigSpec struct {
	// Credentials used to authenticate to the RunPod REST API.
	Credentials xpv1.CommonCredentialSelectors `json:"credentials"`
}

// ProviderConfigStatus reflects the observed state of a RunPod ProviderConfig.
type ProviderConfigStatus struct {
	xpv1.ConditionedStatus `json:",inline"`
}

// A ProviderConfig configures credentials for the RunPod provider.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
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

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
}
