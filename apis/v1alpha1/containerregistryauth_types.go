package v1alpha1

import (
	"reflect"

	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// interface checks to ensure ContainerRegistryAuth conforms to the
// crossplane-runtime v2 namespaced managed-resource interfaces.
var (
	_ resource.ModernManaged = &ContainerRegistryAuth{}
	_ resource.ManagedList   = &ContainerRegistryAuthList{}
)

// ContainerRegistryAuthParameters define a private container registry
// credential. The referenced secret must live in the resource's namespace.
type ContainerRegistryAuthParameters struct {
	// Display name in RunPod. Defaults to the resource name. The RunPod
	// API has no update call for registry auths, so all fields are
	// immutable: rotate credentials by deleting and recreating.
	// +optional
	// +immutable
	Name *string `json:"name,omitempty"`

	// Reference to a Secret in this resource's namespace holding the
	// registry username and password.
	// +immutable
	CredentialsSecretRef ContainerRegistrySecretRef `json:"credentialsSecretRef"`
}

// ContainerRegistrySecretRef points at the credential secret keys.
type ContainerRegistrySecretRef struct {
	// Name of the secret.
	Name string `json:"name"`

	// Key holding the registry username.
	// +kubebuilder:default=username
	// +optional
	UsernameKey string `json:"usernameKey,omitempty"`

	// Key holding the registry password or token.
	// +kubebuilder:default=password
	// +optional
	PasswordKey string `json:"passwordKey,omitempty"`
}

// ContainerRegistryAuthObservation captures the observed state.
type ContainerRegistryAuthObservation struct {
	ContainerRegistryAuthID string `json:"containerRegistryAuthId,omitempty"`
	Name                    string `json:"name,omitempty"`
}

// ContainerRegistryAuthSpec defines the desired state of a RunPod
// ContainerRegistryAuth resource.
type ContainerRegistryAuthSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              ContainerRegistryAuthParameters `json:"forProvider"`
}

// ContainerRegistryAuthStatus reflects the observed state of a RunPod
// ContainerRegistryAuth resource.
type ContainerRegistryAuthStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 ContainerRegistryAuthObservation `json:"atProvider,omitempty"`
}

// A ContainerRegistryAuth is a managed RunPod private container registry
// credential.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
type ContainerRegistryAuth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerRegistryAuthSpec   `json:"spec"`
	Status ContainerRegistryAuthStatus `json:"status,omitempty"`
}

// ContainerRegistryAuthList contains a list of ContainerRegistryAuth
// resources.
// +kubebuilder:object:root=true
type ContainerRegistryAuthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerRegistryAuth `json:"items"`
}

// SetConditions sets the supplied conditions on the ContainerRegistryAuth status.
func (c *ContainerRegistryAuth) SetConditions(cond ...xpv2.Condition) {
	c.Status.SetConditions(cond...)
}

// GetCondition returns the condition of the supplied type if present.
func (c *ContainerRegistryAuth) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return c.Status.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this ContainerRegistryAuth.
func (c *ContainerRegistryAuth) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	c.Spec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this ContainerRegistryAuth.
func (c *ContainerRegistryAuth) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return c.Spec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (c *ContainerRegistryAuth) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	c.Spec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (c *ContainerRegistryAuth) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return c.Spec.WriteConnectionSecretToReference
}

// SetManagementPolicies sets management policies for this ContainerRegistryAuth.
func (c *ContainerRegistryAuth) SetManagementPolicies(mp xpv2.ManagementPolicies) {
	c.Spec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this ContainerRegistryAuth.
func (c *ContainerRegistryAuth) GetManagementPolicies() xpv2.ManagementPolicies {
	return c.Spec.ManagementPolicies
}

// GetItems returns the list items as the runtime's Managed interface.
func (l *ContainerRegistryAuthList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// ContainerRegistryAuth type metadata.
var (
	ContainerRegistryAuthKind             = reflect.TypeOf(ContainerRegistryAuth{}).Name()
	ContainerRegistryAuthGroupKind        = schema.GroupKind{Group: Group, Kind: ContainerRegistryAuthKind}.String()
	ContainerRegistryAuthKindAPIVersion   = ContainerRegistryAuthKind + "." + SchemeGroupVersion.String()
	ContainerRegistryAuthGroupVersionKind = SchemeGroupVersion.WithKind(ContainerRegistryAuthKind)
)
