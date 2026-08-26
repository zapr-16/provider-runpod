package endpoint

import (
	"context"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errGetEndpoint    = "cannot get endpoint from RunPod API"
	errGetTemplate    = "cannot get template from RunPod API"
	errCreateTemplate = "cannot create template via RunPod API"
	errCreateEndpoint = "cannot create endpoint via RunPod API"
	errUpdateEndpoint = "cannot update endpoint via RunPod API"
	errUpdateTemplate = "cannot update template via RunPod API"
	errDeleteEndpoint = "cannot delete endpoint via RunPod API"

	// dataPlaneBaseURL is the serverless data plane, distinct from the REST
	// control plane the client talks to. Unlike pod proxy URLs, every request
	// to it requires an Authorization: Bearer <RunPod API key> header.
	dataPlaneBaseURL = "https://api.runpod.ai/v2"
)

type external struct {
	client *runpodclient.Client
	log    logr.Logger
}

func (e *external) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	ep, ok := mg.(*v1alpha1.Endpoint)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotEndpoint)
	}

	externalName := meta.GetExternalName(ep)
	if externalName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	response, found, err := e.client.GetEndpoint(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetEndpoint)
	}
	if !found {
		e.log.Info("Endpoint not found in RunPod API", "external-name", externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	runtimeEndpoint := fmt.Sprintf("%s/%s", dataPlaneBaseURL, response.ID)
	ep.Status.AtProvider = v1alpha1.EndpointObservation{
		EndpointID:      response.ID,
		TemplateID:      response.TemplateID,
		RuntimeEndpoint: runtimeEndpoint,
		OpenAIBaseURL:   runtimeEndpoint + "/openai/v1",
		WorkersReady:    countRunningWorkers(response.Workers),
		WorkersTotal:    int32(len(response.Workers)), //nolint:gosec // worker counts are tiny
	}

	// A serverless endpoint is ready as soon as it exists: zero workers is
	// the normal scale-to-zero state, and RunPod spins workers up on demand.
	ep.SetConditions(xpv2.Available())

	upToDate := !hasEndpointDrift(ep.Spec.ForProvider, response)
	if upToDate && response.TemplateID != "" {
		// The template embedded in the endpoint response omits imageName,
		// so template drift needs a dedicated GET.
		template, found, err := e.client.GetTemplate(ctx, response.TemplateID)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errGetTemplate)
		}
		if found {
			upToDate = !hasTemplateDrift(ep.Spec.ForProvider, *template)
		}
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  upToDate,
		ConnectionDetails: connectionDetails(response.ID),
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	ep, ok := mg.(*v1alpha1.Endpoint)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotEndpoint)
	}

	name := ep.GetName()
	templateID, err := e.client.CreateTemplate(ctx, runpodclient.CreateTemplateRequest{
		Name:                    name,
		ImageName:               ep.Spec.ForProvider.ImageName,
		IsServerless:            true,
		Env:                     buildEnvMap(ep.Spec.ForProvider.Env),
		ContainerDiskInGb:       ep.Spec.ForProvider.ContainerDiskInGb,
		DockerStartCmd:          cloneStrings(ep.Spec.ForProvider.DockerStartCmd),
		DockerEntrypoint:        cloneStrings(ep.Spec.ForProvider.DockerEntrypoint),
		ContainerRegistryAuthID: ep.Spec.ForProvider.ContainerRegistryAuthID,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateTemplate)
	}

	endpointID, err := e.client.CreateEndpoint(ctx, buildCreateEndpointRequest(&name, templateID, ep.Spec.ForProvider))
	if err != nil {
		// Best-effort cleanup so a failed endpoint create does not leak the
		// template we just made; a leaked template costs nothing, so a
		// cleanup failure is logged rather than returned.
		if derr := e.client.DeleteTemplate(ctx, templateID); derr != nil {
			e.log.Info("could not clean up template after failed endpoint create", "template-id", templateID, "error", derr)
		}
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateEndpoint)
	}

	meta.SetExternalName(ep, endpointID)

	return managed.ExternalCreation{
		ConnectionDetails: connectionDetails(endpointID),
	}, nil
}

func (e *external) Update(ctx context.Context, mg xpresource.Managed) (managed.ExternalUpdate, error) {
	ep, ok := mg.(*v1alpha1.Endpoint)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotEndpoint)
	}

	externalName := meta.GetExternalName(ep)
	if externalName == "" {
		return managed.ExternalUpdate{}, nil
	}

	spec := ep.Spec.ForProvider
	if err := e.client.UpdateEndpoint(ctx, externalName, runpodclient.UpdateEndpointRequest{
		GPUTypeIDs:          cloneStrings(spec.GPUTypeIDs),
		GPUCount:            spec.GPUCount,
		WorkersMin:          spec.WorkersMin,
		WorkersMax:          spec.WorkersMax,
		IdleTimeout:         spec.IdleTimeout,
		FlashBoot:           spec.FlashBoot,
		ScalerType:          scalerTypeString(spec.ScalerType),
		ScalerValue:         spec.ScalerValue,
		NetworkVolumeID:     spec.NetworkVolumeID,
		DataCenterIDs:       cloneStrings(spec.DataCenterIDs),
		ExecutionTimeoutMs:  spec.ExecutionTimeoutMs,
		VCPUCount:           spec.VCPUCount,
		CPUFlavorIDs:        cloneStrings(spec.CPUFlavorIDs),
		AllowedCudaVersions: cloneStrings(spec.AllowedCudaVersions),
		MinCudaVersion:      spec.MinCudaVersion,
		NetworkVolumeIDs:    cloneStrings(spec.NetworkVolumeIDs),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateEndpoint)
	}

	// The implicit template is patched through the ID observed on the
	// endpoint; Observe() runs before Update() in the same reconcile, so
	// atProvider is already populated. Fall back to a GET if it is not.
	templateID := ep.Status.AtProvider.TemplateID
	if templateID == "" {
		response, found, err := e.client.GetEndpoint(ctx, externalName)
		if err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errGetEndpoint)
		}
		if !found {
			return managed.ExternalUpdate{}, nil
		}
		templateID = response.TemplateID
	}
	if templateID == "" {
		return managed.ExternalUpdate{}, nil
	}

	if err := e.client.UpdateTemplate(ctx, templateID, runpodclient.UpdateTemplateRequest{
		ImageName:               &spec.ImageName,
		Env:                     buildEnvMap(spec.Env),
		ContainerDiskInGb:       spec.ContainerDiskInGb,
		DockerStartCmd:          cloneStrings(spec.DockerStartCmd),
		DockerEntrypoint:        cloneStrings(spec.DockerEntrypoint),
		ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateTemplate)
	}

	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg xpresource.Managed) (managed.ExternalDelete, error) {
	ep, ok := mg.(*v1alpha1.Endpoint)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotEndpoint)
	}

	externalName := meta.GetExternalName(ep)
	if externalName == "" {
		return managed.ExternalDelete{}, nil
	}

	// Resolve the implicit template before the endpoint disappears; the
	// endpoint observation is the only durable record of its ID.
	templateID := ep.Status.AtProvider.TemplateID
	if templateID == "" {
		if response, found, err := e.client.GetEndpoint(ctx, externalName); err == nil && found {
			templateID = response.TemplateID
		}
	}

	if err := e.client.DeleteEndpoint(ctx, externalName); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDeleteEndpoint)
	}

	if templateID != "" {
		// Best-effort: a leaked template costs nothing and is visible in
		// the RunPod console if manual cleanup is ever needed, so template
		// cleanup failures never block endpoint deletion.
		if err := e.client.DeleteTemplate(ctx, templateID); err != nil {
			e.log.Info("could not delete template backing endpoint", "template-id", templateID, "error", err)
		}
	}

	return managed.ExternalDelete{}, nil
}

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

func buildCreateEndpointRequest(name *string, templateID string, spec v1alpha1.EndpointParameters) runpodclient.CreateEndpointRequest {
	return runpodclient.CreateEndpointRequest{
		Name:                name,
		TemplateID:          templateID,
		GPUTypeIDs:          cloneStrings(spec.GPUTypeIDs),
		GPUCount:            spec.GPUCount,
		WorkersMin:          spec.WorkersMin,
		WorkersMax:          spec.WorkersMax,
		IdleTimeout:         spec.IdleTimeout,
		FlashBoot:           spec.FlashBoot,
		ScalerType:          scalerTypeString(spec.ScalerType),
		ScalerValue:         spec.ScalerValue,
		NetworkVolumeID:     spec.NetworkVolumeID,
		DataCenterIDs:       cloneStrings(spec.DataCenterIDs),
		ExecutionTimeoutMs:  spec.ExecutionTimeoutMs,
		ComputeType:         spec.ComputeType,
		VCPUCount:           spec.VCPUCount,
		CPUFlavorIDs:        cloneStrings(spec.CPUFlavorIDs),
		AllowedCudaVersions: cloneStrings(spec.AllowedCudaVersions),
		MinCudaVersion:      spec.MinCudaVersion,
		NetworkVolumeIDs:    cloneStrings(spec.NetworkVolumeIDs),
	}
}

// hasEndpointDrift reports whether any endpoint-level spec field diverges
// from the observation. Nil spec fields never drift, matching the pod
// controller's semantics.
func hasEndpointDrift(spec v1alpha1.EndpointParameters, observed *runpodclient.EndpointResponse) bool {
	if int32PtrDrifts(spec.GPUCount, observed.GPUCount) ||
		int32PtrDrifts(spec.WorkersMin, observed.WorkersMin) ||
		int32PtrDrifts(spec.WorkersMax, observed.WorkersMax) ||
		int32PtrDrifts(spec.IdleTimeout, observed.IdleTimeout) ||
		int32PtrDrifts(spec.ScalerValue, observed.ScalerValue) ||
		int32PtrDrifts(spec.ExecutionTimeoutMs, observed.ExecutionTimeoutMs) ||
		stringPtrDrifts(spec.NetworkVolumeID, observed.NetworkVolumeID) {
		return true
	}
	if spec.ScalerType != nil && string(*spec.ScalerType) != observed.ScalerType {
		return true
	}
	if spec.FlashBoot != nil && *spec.FlashBoot != observed.FlashBoot {
		return true
	}
	// GPU type order matters to RunPod (rental priority), so compare ordered.
	// dataCenterIds is not compared: the API accepts it but never echoes it.
	// Nil and empty both mean "unmanaged": the PATCH payload uses omitempty,
	// so an empty list could never be reconciled anyway.
	if len(spec.GPUTypeIDs) > 0 && !stringSlicesEqual(spec.GPUTypeIDs, observed.GPUTypeIDs) {
		return true
	}
	// computeType/vcpuCount/cpuFlavorIds/allowedCudaVersions/minCudaVersion/networkVolumeIds
	// are not verified to be echoed back (the OpenAPI response schema
	// over-promises; see dataCenterIds), so they are write-only for drift
	// purposes.
	return false
}

// hasTemplateDrift reports whether the template-carried spec fields diverge
// from the template embedded in the endpoint observation.
func hasTemplateDrift(spec v1alpha1.EndpointParameters, observed runpodclient.TemplateResponse) bool {
	if spec.ImageName != observed.ImageName {
		return true
	}
	if spec.ContainerDiskInGb != nil && *spec.ContainerDiskInGb != observed.ContainerDiskInGb {
		return true
	}
	// Nil and empty both mean "unmanaged" (see hasEndpointDrift).
	if len(spec.Env) > 0 && !stringMapsEqual(buildEnvMap(spec.Env), observed.Env) {
		return true
	}
	// GET /templates echoes these, unlike the endpoint-level fields above, so
	// they get real drift detection. Command arrays are order-sensitive.
	if len(spec.DockerStartCmd) > 0 && !stringSlicesEqual(spec.DockerStartCmd, observed.DockerStartCmd) {
		return true
	}
	if len(spec.DockerEntrypoint) > 0 && !stringSlicesEqual(spec.DockerEntrypoint, observed.DockerEntrypoint) {
		return true
	}
	if stringPtrDrifts(spec.ContainerRegistryAuthID, observed.ContainerRegistryAuthID) {
		return true
	}
	return false
}

func countRunningWorkers(workers []runpodclient.EndpointWorker) int32 {
	var n int32
	for _, w := range workers {
		if w.DesiredStatus == "RUNNING" {
			n++
		}
	}
	return n
}

func connectionDetails(endpointID string) managed.ConnectionDetails {
	base := fmt.Sprintf("%s/%s", dataPlaneBaseURL, endpointID)
	return managed.ConnectionDetails{
		"endpointId": []byte(endpointID),
		"endpoint":   []byte(base),
		"openaiUrl":  []byte(base + "/openai/v1"),
	}
}

func scalerTypeString(st *v1alpha1.ScalerType) *string {
	if st == nil {
		return nil
	}
	s := string(*st)
	return &s
}

func int32PtrDrifts(desired *int32, observed int32) bool {
	return desired != nil && *desired != observed
}

func stringPtrDrifts(desired *string, observed string) bool {
	return desired != nil && *desired != observed
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || bv != av {
			return false
		}
	}
	return true
}

func buildEnvMap(in []v1alpha1.EnvVar) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, env := range in {
		out[env.Name] = env.Value
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
