package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CloudType identifies the RunPod cloud class used for scheduling.
type CloudType string

const (
	// CloudTypeSecure schedules the pod on the secure cloud.
	CloudTypeSecure CloudType = "SECURE"
	// CloudTypeCommunity schedules the pod on the community cloud.
	CloudTypeCommunity CloudType = "COMMUNITY"
)

// EnvVar defines an environment variable passed to the pod container.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Port defines a container port exposed by the pod.
type Port struct {
	Number   int32  `json:"number"`
	Protocol string `json:"protocol,omitempty"`
}

// PodParameters define the desired state inputs for a RunPod pod.
type PodParameters struct {
	// Container image to run for the pod workload.
	// +optional
	// +immutable
	ImageName *string `json:"imageName,omitempty"`

	// Ordered set of acceptable RunPod GPU type IDs for placement.
	// +optional
	// +immutable
	GPUTypeIDs []string `json:"gpuTypeIds,omitempty"`

	// Number of GPUs requested for the pod.
	// +optional
	// +immutable
	GPUCount *int32 `json:"gpuCount,omitempty"`

	// RunPod cloud class for scheduling, limited to SECURE or COMMUNITY.
	// +optional
	// +immutable
	CloudType *CloudType `json:"cloudType,omitempty"`

	// Request a public IP for COMMUNITY cloud pods so exposed ports receive external mappings.
	// +optional
	// +immutable
	SupportPublicIP *bool `json:"supportPublicIp,omitempty"`

	// Size of the ephemeral container disk in GiB.
	// +optional
	// +immutable
	ContainerDiskInGb *int32 `json:"containerDiskInGb,omitempty"`

	// Size of the persisted pod volume in GiB.
	// +optional
	// +immutable
	VolumeInGb *int32 `json:"volumeInGb,omitempty"`

	// Mount path inside the container for the persisted pod volume.
	// +optional
	// +immutable
	VolumeMountPath *string `json:"volumeMountPath,omitempty"`

	// Environment variables injected into the container at startup.
	// +optional
	Env []EnvVar `json:"env,omitempty"`

	// Container ports to expose, later serialized to RunPod "<port>/<protocol>" strings.
	// +optional
	Ports []Port `json:"ports,omitempty"`

	// Command array passed to the container as its startup command.
	// +optional
	// +immutable
	DockerStartCmd []string `json:"dockerStartCmd,omitempty"`

	// RecreateOnTerminate causes the controller to clear the external
	// name and report the resource as missing whenever RunPod marks
	// the pod EXITED or TERMINATED (e.g. Spot reclaim, OOM, manual
	// console delete). Crossplane's next reconcile will then call
	// Create() and provision a fresh pod with the same spec. Useful
	// for Spot-backed workloads where occasional reclaim is expected
	// and continuous availability is preferred over preserving the
	// specific instance. Defaults to false (manual recreate).
	// +optional
	RecreateOnTerminate *bool `json:"recreateOnTerminate,omitempty"`
}

// PodObservation captures the observed state returned by RunPod.
type PodObservation struct {
	// RunPod pod ID returned by create and mirrored from the external name.
	PodID string `json:"podId,omitempty"`

	// Raw RunPod lifecycle status from GET /pods/{podId}.
	DesiredStatus string `json:"desiredStatus,omitempty"`

	// Public IP assigned to the pod once networking is ready.
	PublicIP string `json:"publicIp,omitempty"`

	// External port numbers keyed by RunPod port token, absent during networking initialization.
	PortMappings map[string]int32 `json:"portMappings,omitempty"`

	// Derived endpoint URL built from the first HTTP port mapping when networking is ready.
	RuntimeEndpoint string `json:"runtimeEndpoint,omitempty"`

	// Effective hourly pod cost from the RunPod observation response.
	CostPerHr float64 `json:"costPerHr,omitempty"`

	// Human-readable GPU name from machine.gpuDisplayName, or from the nested GPU object as fallback.
	GPUDisplayName string `json:"gpuDisplayName,omitempty"`

	// Timestamp of the last pod start event, parsed from the RunPod response.
	LastStartedAt *metav1.Time `json:"lastStartedAt,omitempty"`

	// Derived readiness flag, true only when PublicIP is non-empty and PortMappings is non-nil.
	NetworkingReady bool `json:"networkingReady"`
}

// PodSpec defines the desired state of a RunPod Pod resource.
type PodSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       PodParameters `json:"forProvider"`
}

// PodStatus reflects the observed state of a RunPod Pod resource.
type PodStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          PodObservation `json:"atProvider,omitempty"`
}

// A Pod is a managed RunPod GPU workload.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
type Pod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodSpec   `json:"spec"`
	Status PodStatus `json:"status,omitempty"`
}

// PodList contains a list of Pod resources.
// +kubebuilder:object:root=true
type PodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pod `json:"items"`
}

// SetConditions sets the supplied conditions on the Pod status.
func (p *Pod) SetConditions(c ...xpv1.Condition) {
	p.Status.ResourceStatus.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (p *Pod) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return p.Status.ResourceStatus.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this Pod.
func (p *Pod) SetProviderConfigReference(r *xpv1.Reference) {
	p.Spec.ResourceSpec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this Pod.
func (p *Pod) GetProviderConfigReference() *xpv1.Reference {
	return p.Spec.ResourceSpec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (p *Pod) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	p.Spec.ResourceSpec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (p *Pod) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return p.Spec.ResourceSpec.WriteConnectionSecretToReference
}

// SetPublishConnectionDetailsTo sets the publish-connection-details target.
func (p *Pod) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	p.Spec.ResourceSpec.PublishConnectionDetailsTo = r
}

// GetPublishConnectionDetailsTo gets the publish-connection-details target.
func (p *Pod) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return p.Spec.ResourceSpec.PublishConnectionDetailsTo
}

// SetManagementPolicies sets management policies for this Pod.
func (p *Pod) SetManagementPolicies(mp xpv1.ManagementPolicies) {
	p.Spec.ResourceSpec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this Pod.
func (p *Pod) GetManagementPolicies() xpv1.ManagementPolicies {
	return p.Spec.ResourceSpec.ManagementPolicies
}

// SetDeletionPolicy sets the deletion policy for this Pod.
func (p *Pod) SetDeletionPolicy(dp xpv1.DeletionPolicy) {
	p.Spec.ResourceSpec.DeletionPolicy = dp
}

// GetDeletionPolicy gets the deletion policy for this Pod.
func (p *Pod) GetDeletionPolicy() xpv1.DeletionPolicy {
	return p.Spec.ResourceSpec.DeletionPolicy
}
