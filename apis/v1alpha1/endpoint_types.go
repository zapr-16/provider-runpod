package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
// serverless endpoint. The controller implicitly manages the backing
// RunPod template: Create provisions the template then the endpoint,
// Delete removes both, and template drift is folded into endpoint drift.
type EndpointParameters struct {
	// Container image the serverless workers run, e.g. runpod/worker-v1-vllm:stable.
	ImageName string `json:"imageName"`

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
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       EndpointParameters `json:"forProvider"`
}

// EndpointStatus reflects the observed state of a RunPod serverless Endpoint.
type EndpointStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          EndpointObservation `json:"atProvider,omitempty"`
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
func (e *Endpoint) SetConditions(c ...xpv1.Condition) {
	e.Status.SetConditions(c...)
}

// GetCondition returns the condition of the supplied type if present.
func (e *Endpoint) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return e.Status.GetCondition(ct)
}

// SetProviderConfigReference sets the provider config reference for this Endpoint.
func (e *Endpoint) SetProviderConfigReference(r *xpv1.Reference) {
	e.Spec.ProviderConfigReference = r
}

// GetProviderConfigReference gets the provider config reference for this Endpoint.
func (e *Endpoint) GetProviderConfigReference() *xpv1.Reference {
	return e.Spec.ProviderConfigReference
}

// SetWriteConnectionSecretToReference sets the connection secret reference.
func (e *Endpoint) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	e.Spec.WriteConnectionSecretToReference = r
}

// GetWriteConnectionSecretToReference gets the connection secret reference.
func (e *Endpoint) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return e.Spec.WriteConnectionSecretToReference
}

// SetPublishConnectionDetailsTo sets the publish-connection-details target.
func (e *Endpoint) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	e.Spec.PublishConnectionDetailsTo = r
}

// GetPublishConnectionDetailsTo gets the publish-connection-details target.
func (e *Endpoint) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return e.Spec.PublishConnectionDetailsTo
}

// SetManagementPolicies sets management policies for this Endpoint.
func (e *Endpoint) SetManagementPolicies(mp xpv1.ManagementPolicies) {
	e.Spec.ManagementPolicies = mp
}

// GetManagementPolicies gets management policies for this Endpoint.
func (e *Endpoint) GetManagementPolicies() xpv1.ManagementPolicies {
	return e.Spec.ManagementPolicies
}

// SetDeletionPolicy sets the deletion policy for this Endpoint.
func (e *Endpoint) SetDeletionPolicy(dp xpv1.DeletionPolicy) {
	e.Spec.DeletionPolicy = dp
}

// GetDeletionPolicy gets the deletion policy for this Endpoint.
func (e *Endpoint) GetDeletionPolicy() xpv1.DeletionPolicy {
	return e.Spec.DeletionPolicy
}
