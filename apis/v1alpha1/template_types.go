package v1alpha1

import (
	"reflect"

	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// interface checks to ensure Template conforms to the crossplane-runtime v2
// namespaced managed-resource interfaces.
var (
	_ resource.ModernManaged = &Template{}
	_ resource.ManagedList   = &TemplateList{}
)

// TemplateParameters define the desired state of a standalone RunPod
// template (as distinct from the implicit template RunPod creates for a
// serverless Endpoint).
type TemplateParameters struct {
	// Container image to run for the template.
	ImageName string `json:"imageName"`

	// Marks the template for use by serverless workers. The RunPod PATCH
	// schema has no isServerless field, so this cannot be reconciled after
	// creation.
	// +optional
	// +immutable
	IsServerless *bool `json:"isServerless,omitempty"`

	// Template name shown in the RunPod console. Defaults to the resource name.
	// +optional
	Name *string `json:"name,omitempty"`

	// Environment variables injected into the container at startup.
	// +optional
	Env []EnvVar `json:"env,omitempty"`

	// Size of the ephemeral container disk in GiB.
	// +optional
	ContainerDiskInGb *int32 `json:"containerDiskInGb,omitempty"`

	// Command array passed to the container as its startup command.
	// +optional
	DockerStartCmd []string `json:"dockerStartCmd,omitempty"`

	// Entrypoint array overriding the image ENTRYPOINT.
	// +optional
	DockerEntrypoint []string `json:"dockerEntrypoint,omitempty"`

	// ID of a ContainerRegistryAuth for pulling private images.
	// +optional
	ContainerRegistryAuthID *string `json:"containerRegistryAuthId,omitempty"`

	// Container ports to expose, later serialized to RunPod "<port>/<protocol>" strings.
	// +optional
	Ports []Port `json:"ports,omitempty"`

	// Size of the persisted volume in GiB.
	// +optional
	VolumeInGb *int32 `json:"volumeInGb,omitempty"`

	// Mount path inside the container for the persisted volume.
	// +optional
	VolumeMountPath *string `json:"volumeMountPath,omitempty"`
}

// TemplateObservation captures the observed state returned by RunPod.
type TemplateObservation struct {
	TemplateID string `json:"templateId,omitempty"`
	Name       string `json:"name,omitempty"`
}

// TemplateSpec defines the desired state of a RunPod Template resource.
type TemplateSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              TemplateParameters `json:"forProvider"`
}

// TemplateStatus reflects the observed state of a RunPod Template resource.
type TemplateStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 TemplateObservation `json:"atProvider,omitempty"`
}

// A Template is a standalone managed RunPod template.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
type Template struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TemplateSpec   `json:"spec"`
	Status TemplateStatus `json:"status,omitempty"`
}

// TemplateList contains a list of Template resources.
// +kubebuilder:object:root=true
type TemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Template `json:"items"`
}

// SetConditions sets the supplied conditions on the Template status.
func (t *Template) SetConditions(c ...xpv2.Condition) {
	t.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (t *Template) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return t.Status.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this Template.
func (t *Template) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	t.Spec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this Template.
func (t *Template) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return t.Spec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (t *Template) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	t.Spec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (t *Template) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return t.Spec.WriteConnectionSecretToReference
}

// SetManagementPolicies sets management policies for this Template.
func (t *Template) SetManagementPolicies(mp xpv2.ManagementPolicies) {
	t.Spec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this Template.
func (t *Template) GetManagementPolicies() xpv2.ManagementPolicies {
	return t.Spec.ManagementPolicies
}

// GetItems returns the list items as the runtime's Managed interface.
func (l *TemplateList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// Template type metadata.
var (
	TemplateKind             = reflect.TypeOf(Template{}).Name()
	TemplateGroupKind        = schema.GroupKind{Group: Group, Kind: TemplateKind}.String()
	TemplateKindAPIVersion   = TemplateKind + "." + SchemeGroupVersion.String()
	TemplateGroupVersionKind = SchemeGroupVersion.WithKind(TemplateKind)
)
