package v1alpha1

import (
	"reflect"

	resource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// interface checks to ensure Endpoint conforms to the crossplane-runtime v2
// namespaced managed-resource interfaces.
var (
	_ resource.ModernManaged = &Endpoint{}
	_ resource.ManagedList   = &EndpointList{}
)

// ScalerType selects the autoscaling strategy for a serverless endpoint.
type ScalerType string

const (
	// ScalerTypeQueueDelay scales up when requests wait in queue longer than scalerValue seconds.
	ScalerTypeQueueDelay ScalerType = "QUEUE_DELAY"
	// ScalerTypeRequestCount scales workers to queued requests divided by scalerValue.
	ScalerTypeRequestCount ScalerType = "REQUEST_COUNT"
)

// EndpointParameters define the desired state inputs for a RunPod
// serverless endpoint. By default (imageName set) the controller implicitly
// manages the backing RunPod template: Create provisions the template then
// the endpoint, Delete removes both, and template drift is folded into
// endpoint drift. In templateId mode, the endpoint instead references an
// existing template owned elsewhere, and the controller never creates,
// patches, or deletes any template.
// +kubebuilder:validation:XValidation:rule="!(has(self.networkVolumeId) && has(self.networkVolumeIds))",message="networkVolumeId and networkVolumeIds are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="has(self.imageName) != has(self.templateId)",message="exactly one of imageName or templateId must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.templateId) || (!has(self.env) && !has(self.containerDiskInGb) && !has(self.dockerStartCmd) && !has(self.dockerEntrypoint) && !has(self.containerRegistryAuthId))",message="template-carried fields cannot be set together with templateId"
type EndpointParameters struct {
	// Container image the serverless workers run, e.g. runpod/worker-v1-vllm:stable.
	// Mutually exclusive with templateId.
	// +optional
	ImageName *string `json:"imageName,omitempty"`

	// Existing RunPod template to run the endpoint from, e.g. one managed
	// by a Template resource. Mutually exclusive with imageName and the
	// other template-carried fields (env, containerDiskInGb,
	// dockerStartCmd, dockerEntrypoint, containerRegistryAuthId): when
	// templateId is set the controller does NOT create, patch, or delete
	// any template — the referenced template is owned elsewhere.
	// +optional
	TemplateID *string `json:"templateId,omitempty"`

	// Recycle running/idle workers after the implicit template changes
	// (image/env/disk), by cycling workersMax through 0. Without this,
	// FlashBoot standby workers keep serving the old template
	// indefinitely. Defaults to true. Ignored in templateId mode.
	// +kubebuilder:default=true
	// +optional
	RecycleWorkersOnTemplateChange *bool `json:"recycleWorkersOnTemplateChange,omitempty"`

	// Environment variables injected into the workers (carried by the template).
	// +optional
	Env []EnvVar `json:"env,omitempty"`

	// Size of the ephemeral container disk in GiB (carried by the template).
	// +optional
	ContainerDiskInGb *int32 `json:"containerDiskInGb,omitempty"`

	// Ordered list of acceptable RunPod GPU type IDs for the workers
	// (e.g. "NVIDIA GeForce RTX 3090"). Order determines rental priority.
	// Note: the REST API selects serverless workers by GPU type IDs,
	// the same identifiers used for pods — not by VRAM tier.
	// +optional
	GPUTypeIDs []string `json:"gpuTypeIds,omitempty"`

	// Number of GPUs attached to each worker.
	// +optional
	GPUCount *int32 `json:"gpuCount,omitempty"`

	// Minimum number of always-on workers. 0 enables scale-to-zero.
	// +kubebuilder:validation:Minimum=0
	// +optional
	WorkersMin *int32 `json:"workersMin,omitempty"`

	// Maximum number of concurrently running workers.
	// +kubebuilder:validation:Minimum=0
	// +optional
	WorkersMax *int32 `json:"workersMax,omitempty"`

	// Seconds a worker may sit idle before being scaled down (RunPod default 5).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	// +optional
	IdleTimeout *int32 `json:"idleTimeout,omitempty"`

	// Enable FlashBoot for faster cold starts.
	// +optional
	FlashBoot *bool `json:"flashBoot,omitempty"`

	// Autoscaling strategy, QUEUE_DELAY or REQUEST_COUNT.
	// +kubebuilder:validation:Enum=QUEUE_DELAY;REQUEST_COUNT
	// +optional
	ScalerType *ScalerType `json:"scalerType,omitempty"`

	// Threshold for the scaler: queue seconds for QUEUE_DELAY, queued
	// requests per worker for REQUEST_COUNT (RunPod default 4).
	// +optional
	ScalerValue *int32 `json:"scalerValue,omitempty"`

	// Network volume to attach to the workers, e.g. for model weight caching.
	// +optional
	NetworkVolumeID *string `json:"networkVolumeId,omitempty"`

	// Restrict workers to specific RunPod data center IDs.
	// +optional
	DataCenterIDs []string `json:"dataCenterIds,omitempty"`

	// Maximum milliseconds a single request may run on a worker.
	// +optional
	ExecutionTimeoutMs *int32 `json:"executionTimeoutMs,omitempty"`

	// Worker compute class: GPU or CPU. Create-only; RunPod does not allow
	// changing an endpoint's compute type after creation, and it is absent
	// from the PATCH schema.
	// +kubebuilder:validation:Enum=GPU;CPU
	// +optional
	// +immutable
	ComputeType *string `json:"computeType,omitempty"`

	// Number of vCPUs allocated per worker for CPU endpoints.
	// +optional
	VCPUCount *int32 `json:"vcpuCount,omitempty"`

	// Ordered list of acceptable RunPod CPU flavor IDs for CPU endpoints.
	// +optional
	CPUFlavorIDs []string `json:"cpuFlavorIds,omitempty"`

	// CUDA versions the workers are allowed to run on.
	// +optional
	AllowedCudaVersions []string `json:"allowedCudaVersions,omitempty"`

	// Minimum CUDA version required on the workers.
	// +optional
	MinCudaVersion *string `json:"minCudaVersion,omitempty"`

	// Network volumes to attach to the workers, for multi-volume mounting.
	// Mutually exclusive with networkVolumeId.
	// +optional
	NetworkVolumeIDs []string `json:"networkVolumeIds,omitempty"`

	// Command RunPod runs to start the container (carried by the template).
	// +optional
	DockerStartCmd []string `json:"dockerStartCmd,omitempty"`

	// Entrypoint overriding the image's default (carried by the template).
	// +optional
	DockerEntrypoint []string `json:"dockerEntrypoint,omitempty"`

	// ID of the container registry credentials used to pull the image
	// (carried by the template).
	// +optional
	ContainerRegistryAuthID *string `json:"containerRegistryAuthId,omitempty"`
}

// EndpointObservation captures the observed state returned by RunPod.
type EndpointObservation struct {
	// RunPod serverless endpoint ID, mirrored from the external name.
	EndpointID string `json:"endpointId,omitempty"`

	// ID of the implicitly managed RunPod template backing this endpoint.
	TemplateID string `json:"templateId,omitempty"`

	// Data-plane base URL (https://api.runpod.ai/v2/{endpointId}).
	// Requests require an Authorization: Bearer <RunPod API key> header —
	// unlike pod proxy URLs, which are unauthenticated.
	RuntimeEndpoint string `json:"runtimeEndpoint,omitempty"`

	// OpenAI-compatible base URL served by RunPod's vLLM worker
	// (https://api.runpod.ai/v2/{endpointId}/openai/v1). Only meaningful
	// when the image implements the OpenAI route.
	OpenAIBaseURL string `json:"openAIBaseURL,omitempty"`

	// Number of workers currently in RUNNING state.
	WorkersReady int32 `json:"workersReady"`

	// Total number of workers currently provisioned for the endpoint.
	WorkersTotal int32 `json:"workersTotal"`
}

// EndpointSpec defines the desired state of a RunPod serverless Endpoint.
type EndpointSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              EndpointParameters `json:"forProvider"`
}

// EndpointStatus reflects the observed state of a RunPod serverless Endpoint.
type EndpointStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 EndpointObservation `json:"atProvider,omitempty"`
}

// An Endpoint is a managed RunPod serverless endpoint: autoscaled GPU
// workers with scale-to-zero, billed per active second.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=crossplane
// +kubebuilder:subresource:status
type Endpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EndpointSpec   `json:"spec"`
	Status EndpointStatus `json:"status,omitempty"`
}

// EndpointList contains a list of Endpoint resources.
// +kubebuilder:object:root=true
type EndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Endpoint `json:"items"`
}

// SetConditions sets the supplied conditions on the Endpoint status.
func (e *Endpoint) SetConditions(c ...xpv2.Condition) {
	e.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (e *Endpoint) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return e.Status.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this Endpoint.
func (e *Endpoint) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	e.Spec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this Endpoint.
func (e *Endpoint) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return e.Spec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (e *Endpoint) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	e.Spec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (e *Endpoint) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return e.Spec.WriteConnectionSecretToReference
}

// SetManagementPolicies sets management policies for this Endpoint.
func (e *Endpoint) SetManagementPolicies(mp xpv2.ManagementPolicies) {
	e.Spec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this Endpoint.
func (e *Endpoint) GetManagementPolicies() xpv2.ManagementPolicies {
	return e.Spec.ManagementPolicies
}

// GetItems returns the list items as the runtime's Managed interface.
func (l *EndpointList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// Endpoint type metadata.
var (
	EndpointKind             = reflect.TypeOf(Endpoint{}).Name()
	EndpointGroupKind        = schema.GroupKind{Group: Group, Kind: EndpointKind}.String()
	EndpointKindAPIVersion   = EndpointKind + "." + SchemeGroupVersion.String()
	EndpointGroupVersionKind = SchemeGroupVersion.WithKind(EndpointKind)
)
