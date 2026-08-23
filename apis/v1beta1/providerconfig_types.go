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

// ProviderConfigSpec defines the desired state of a RunPod
// ClusterProviderConfig. The credential shape (a secret reference with an
// "apiKey" key) is preserved unchanged from the pre-v2 ProviderConfig; its
// secretRef carries a namespace, letting cluster admins centralize keys in
// one namespace (e.g. crossplane-system) for use across the whole cluster.
type ProviderConfigSpec struct {
	// Credentials used to authenticate to the RunPod REST API.
	Credentials xpv2.CommonCredentialSelectors `json:"credentials"`
}

// LocalCredentialSelectors mirrors xpv2.CommonCredentialSelectors but uses a
// namespace-less xpv2.LocalSecretKeySelector for its secretRef. This is a
// deliberate structural restriction for the namespace-scoped ProviderConfig:
// without a namespace field on the selector, it is impossible for a
// namespace-scoped tenant to author a ProviderConfig that reads a Secret
// outside its own namespace.
type LocalCredentialSelectors struct {
	// Fs is a reference to a filesystem location that contains credentials
	// that must be used to connect to the provider.
	// +optional
	Fs *xpv2.FsSelector `json:"fs,omitempty"`

	// Env is a reference to an environment variable that contains
	// credentials that must be used to connect to the provider.
	// +optional
	Env *xpv2.EnvSelector `json:"env,omitempty"`

	// A SecretRef is a reference to a secret key, in this ProviderConfig's
	// own namespace, that contains the credentials that must be used to
	// connect to the provider.
	// +optional
	SecretRef *xpv2.LocalSecretKeySelector `json:"secretRef,omitempty"`
}

// ToCommonCredentialSelectors resolves the local, namespace-less selector
// into a xpv2.CommonCredentialSelectors bound to the supplied namespace, for
// use with xpresource.ExtractSecret. Callers MUST pass the ProviderConfig's
// own namespace so the secretRef can never be redirected elsewhere.
func (s LocalCredentialSelectors) ToCommonCredentialSelectors(namespace string) xpv2.CommonCredentialSelectors {
	out := xpv2.CommonCredentialSelectors{
		Fs:  s.Fs,
		Env: s.Env,
	}
	if s.SecretRef != nil {
		out.SecretRef = s.SecretRef.ToSecretKeySelector(namespace)
	}
	return out
}

// LocalProviderConfigSpec defines the desired state of a namespace-scoped
// RunPod ProviderConfig. Its credentials.secretRef has NO namespace field:
// the referenced secret is always resolved in the ProviderConfig's own
// namespace, so a namespace-scoped tenant can never point at a secret
// living elsewhere in the cluster.
type LocalProviderConfigSpec struct {
	// Credentials used to authenticate to the RunPod REST API. Any
	// secretRef is resolved in this ProviderConfig's own namespace.
	Credentials LocalCredentialSelectors `json:"credentials"`
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

	Spec   LocalProviderConfigSpec `json:"spec"`
	Status ProviderConfigStatus    `json:"status,omitempty"`
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

// Credentials resolves the ProviderConfig's local credential selectors into
// xpv2.CommonCredentialSelectors, binding any secretRef to this
// ProviderConfig's own namespace so it can never resolve a secret living
// elsewhere in the cluster.
func (p *ProviderConfig) Credentials() xpv2.CommonCredentialSelectors {
	return p.Spec.Credentials.ToCommonCredentialSelectors(p.Namespace)
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

// Credentials resolves the ClusterProviderConfig's credential selectors,
// carrying their secretRef's namespace as authored: cluster admins may
// centralize keys in one namespace for use across the whole cluster.
func (p *ClusterProviderConfig) Credentials() xpv2.CommonCredentialSelectors {
	return p.Spec.Credentials
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
