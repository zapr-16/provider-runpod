package v1alpha1

import (
	"reflect"

	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// interface checks to ensure Pod conforms to the crossplane-runtime v2
// namespaced managed-resource interfaces.
var (
	_ resource.ModernManaged = &Pod{}
	_ resource.ManagedList   = &PodList{}
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
	// Name of the environment variable.
	Name string `json:"name"`
	// Value assigned to the environment variable.
	Value string `json:"value"`
}

// Port defines a container port exposed by the pod.
type Port struct {
	// Number is the container port to expose.
	Number int32 `json:"number"`
	// Protocol for the exposed port: tcp or http. Defaults to tcp.
	Protocol string `json:"protocol,omitempty"`
}

// PodParameters define the desired state inputs for a RunPod pod.
type PodParameters struct {
	// Container image to run for the pod workload.
	// +optional
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
	ContainerDiskInGb *int32 `json:"containerDiskInGb,omitempty"`

	// Size of the persisted pod volume in GiB.
	// +optional
	VolumeInGb *int32 `json:"volumeInGb,omitempty"`

	// Mount path inside the container for the persisted pod volume.
	// +optional
	VolumeMountPath *string `json:"volumeMountPath,omitempty"`

	// Environment variables injected into the container at startup.
	// +optional
	Env []EnvVar `json:"env,omitempty"`

	// Container ports to expose, later serialized to RunPod "<port>/<protocol>" strings.
	// +optional
	Ports []Port `json:"ports,omitempty"`

	// Command array passed to the container as its startup command.
	// +optional
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

	// DesiredState drives the pod lifecycle: RUNNING starts/resumes the
	// pod, EXITED stops it (storage keeps billing while stopped).
	// Unset means RUNNING.
	// +kubebuilder:validation:Enum=RUNNING;EXITED
	// +optional
	DesiredState *string `json:"desiredState,omitempty"`

	// Entrypoint array overriding the image ENTRYPOINT.
	// +optional
	DockerEntrypoint []string `json:"dockerEntrypoint,omitempty"`

	// Prevent the pod from being modified or deleted from the RunPod console.
	// +optional
	Locked *bool `json:"locked,omitempty"`

	// Enable RunPod global networking for the pod.
	// Write-only: the API accepts it but does not echo it back.
	// +optional
	GlobalNetworking *bool `json:"globalNetworking,omitempty"`

	// ID of a ContainerRegistryAuth for pulling private images.
	// +optional
	ContainerRegistryAuthID *string `json:"containerRegistryAuthId,omitempty"`

	// Rent interruptible (Spot) capacity: cheaper, may be reclaimed.
	// Combine with recreateOnTerminate for auto-replacement.
	// +optional
	// +immutable
	Interruptible *bool `json:"interruptible,omitempty"`

	// GPU vs CPU pod. Defaults to GPU server-side.
	// +kubebuilder:validation:Enum=GPU;CPU
	// +optional
	// +immutable
	ComputeType *string `json:"computeType,omitempty"`

	// Number of vCPUs (CPU pods).
	// +optional
	// +immutable
	VCPUCount *int32 `json:"vcpuCount,omitempty"`

	// Acceptable CPU flavor IDs (CPU pods).
	// +optional
	// +immutable
	CPUFlavorIDs []string `json:"cpuFlavorIds,omitempty"`

	// CPU flavor selection strategy: availability or custom.
	// +kubebuilder:validation:Enum=availability;custom
	// +optional
	// +immutable
	CPUFlavorPriority *string `json:"cpuFlavorPriority,omitempty"`

	// Restrict placement to specific data center IDs.
	// +optional
	// +immutable
	DataCenterIDs []string `json:"dataCenterIds,omitempty"`

	// Data center selection strategy: availability or custom.
	// +kubebuilder:validation:Enum=availability;custom
	// +optional
	// +immutable
	DataCenterPriority *string `json:"dataCenterPriority,omitempty"`

	// GPU type selection strategy: availability or custom.
	// +kubebuilder:validation:Enum=availability;custom
	// +optional
	// +immutable
	GPUTypePriority *string `json:"gpuTypePriority,omitempty"`

	// Restrict placement to specific countries (ISO codes).
	// +optional
	// +immutable
	CountryCodes []string `json:"countryCodes,omitempty"`

	// Acceptable CUDA versions on the host.
	// +optional
	// +immutable
	AllowedCudaVersions []string `json:"allowedCudaVersions,omitempty"`

	// Minimum RAM per GPU in GB.
	// +optional
	// +immutable
	MinRAMPerGPU *int32 `json:"minRAMPerGPU,omitempty"`

	// Minimum vCPUs per GPU.
	// +optional
	// +immutable
	MinVCPUPerGPU *int32 `json:"minVCPUPerGPU,omitempty"`

	// Minimum disk bandwidth in MBps.
	// +optional
	// +immutable
	MinDiskBandwidthMBps *int32 `json:"minDiskBandwidthMBps,omitempty"`

	// Minimum download bandwidth in Mbps.
	// +optional
	// +immutable
	MinDownloadMbps *int32 `json:"minDownloadMbps,omitempty"`

	// Minimum upload bandwidth in Mbps.
	// +optional
	// +immutable
	MinUploadMbps *int32 `json:"minUploadMbps,omitempty"`

	// RunPod template to create the pod from. Direct fields set here
	// override template values server-side.
	// +optional
	// +immutable
	TemplateID *string `json:"templateId,omitempty"`

	// Network volume to attach (must be in the same data center).
	// +optional
	// +immutable
	NetworkVolumeID *string `json:"networkVolumeId,omitempty"`

	// Encrypt the pod volume.
	// +optional
	// +immutable
	VolumeEncrypted *bool `json:"volumeEncrypted,omitempty"`
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

	// Derived endpoint URL for the first HTTP port, served through RunPod's
	// TLS proxy (https://{podId}-{port}.proxy.runpod.net). Present as soon as
	// the pod ID is known; the proxy returns 502 until the workload listens.
	RuntimeEndpoint string `json:"runtimeEndpoint,omitempty"`

	// Effective hourly pod cost from the RunPod observation response.
	CostPerHr float64 `json:"costPerHr,omitempty"`

	// Human-readable GPU name from machine.gpuDisplayName, or from the nested GPU object as fallback.
	GPUDisplayName string `json:"gpuDisplayName,omitempty"`

	// Timestamp of the last pod start event, parsed from the RunPod response.
	LastStartedAt *metav1.Time `json:"lastStartedAt,omitempty"`

	// Derived readiness flag, true when a connection endpoint is resolvable:
	// either an HTTP proxy URL or a public IP with port mappings.
	NetworkingReady bool `json:"networkingReady"`

	// True when immutable spec fields diverge from the running pod
	// (currently: observed GPU type not in gpuTypeIds, or interruptible
	// mismatch). Immutable drift can only come from spec edits after
	// creation and can never be reconciled in place. Mutable-field drift
	// is reconciled via PATCH instead and never appears here.
	DriftDetected bool `json:"driftDetected,omitempty"`
}

// PodSpec defines the desired state of a RunPod Pod resource.
type PodSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              PodParameters `json:"forProvider"`
}

// PodStatus reflects the observed state of a RunPod Pod resource.
type PodStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 PodObservation `json:"atProvider,omitempty"`
}

// A Pod is a managed RunPod GPU workload. The name RunPod assigns to the
// pod on create is metadata.name with "-" plus the first 8 hex characters
// of this resource's UID appended, making it deterministic and reproducible
// across reconciles. This lets the controller recover if it crashes
// between requesting the pod and recording its ID, instead of either
// leaking a billed GPU or refusing to proceed.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
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
func (p *Pod) SetConditions(c ...xpv2.Condition) {
	p.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (p *Pod) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return p.Status.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this Pod.
func (p *Pod) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	p.Spec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this Pod.
func (p *Pod) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return p.Spec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (p *Pod) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	p.Spec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (p *Pod) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return p.Spec.WriteConnectionSecretToReference
}

// SetManagementPolicies sets management policies for this Pod.
func (p *Pod) SetManagementPolicies(mp xpv2.ManagementPolicies) {
	p.Spec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this Pod.
func (p *Pod) GetManagementPolicies() xpv2.ManagementPolicies {
	return p.Spec.ManagementPolicies
}

// GetItems returns the list items as the runtime's Managed interface.
func (l *PodList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// Pod type metadata.
var (
	PodKind             = reflect.TypeFor[Pod]().Name()
	PodGroupKind        = schema.GroupKind{Group: Group, Kind: PodKind}.String()
	PodKindAPIVersion   = PodKind + "." + SchemeGroupVersion.String()
	PodGroupVersionKind = SchemeGroupVersion.WithKind(PodKind)
)
