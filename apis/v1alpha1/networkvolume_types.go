package v1alpha1

import (
	"reflect"

	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// interface checks to ensure NetworkVolume conforms to the crossplane-runtime
// v2 namespaced managed-resource interfaces.
var (
	_ resource.ModernManaged = &NetworkVolume{}
	_ resource.ManagedList   = &NetworkVolumeList{}
)

// NetworkVolumeParameters define the desired state of a RunPod network volume.
type NetworkVolumeParameters struct {
	// Volume name shown in the RunPod console. Defaults to the resource name.
	// +optional
	Name *string `json:"name,omitempty"`

	// Volume size in GB. RunPod only supports growing a volume; shrinking
	// is rejected by the API.
	// +kubebuilder:validation:Minimum=1
	Size int32 `json:"size"`

	// Data center hosting the volume (e.g. "EU-RO-1"). Pods/endpoints using
	// the volume must be scheduled in the same data center.
	// +immutable
	DataCenterID string `json:"dataCenterId"`
}

// NetworkVolumeObservation captures the observed state returned by RunPod.
type NetworkVolumeObservation struct {
	NetworkVolumeID string `json:"networkVolumeId,omitempty"`
	Name            string `json:"name,omitempty"`
	Size            int32  `json:"size,omitempty"`
	DataCenterID    string `json:"dataCenterId,omitempty"`
}

// NetworkVolumeSpec defines the desired state of a RunPod NetworkVolume resource.
type NetworkVolumeSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              NetworkVolumeParameters `json:"forProvider"`
}

// NetworkVolumeStatus reflects the observed state of a RunPod NetworkVolume resource.
type NetworkVolumeStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 NetworkVolumeObservation `json:"atProvider,omitempty"`
}

// A NetworkVolume is a managed RunPod persistent storage volume.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
type NetworkVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkVolumeSpec   `json:"spec"`
	Status NetworkVolumeStatus `json:"status,omitempty"`
}

// NetworkVolumeList contains a list of NetworkVolume resources.
// +kubebuilder:object:root=true
type NetworkVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkVolume `json:"items"`
}

// SetConditions sets the supplied conditions on the NetworkVolume status.
func (n *NetworkVolume) SetConditions(c ...xpv2.Condition) {
	n.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (n *NetworkVolume) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return n.Status.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this NetworkVolume.
func (n *NetworkVolume) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	n.Spec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this NetworkVolume.
func (n *NetworkVolume) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return n.Spec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (n *NetworkVolume) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	n.Spec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (n *NetworkVolume) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return n.Spec.WriteConnectionSecretToReference
}

// SetManagementPolicies sets management policies for this NetworkVolume.
func (n *NetworkVolume) SetManagementPolicies(mp xpv2.ManagementPolicies) {
	n.Spec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this NetworkVolume.
func (n *NetworkVolume) GetManagementPolicies() xpv2.ManagementPolicies {
	return n.Spec.ManagementPolicies
}

// GetItems returns the list items as the runtime's Managed interface.
func (l *NetworkVolumeList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// NetworkVolume type metadata.
var (
	NetworkVolumeKind             = reflect.TypeOf(NetworkVolume{}).Name()
	NetworkVolumeGroupKind        = schema.GroupKind{Group: Group, Kind: NetworkVolumeKind}.String()
	NetworkVolumeKindAPIVersion   = NetworkVolumeKind + "." + SchemeGroupVersion.String()
	NetworkVolumeGroupVersionKind = SchemeGroupVersion.WithKind(NetworkVolumeKind)
)
