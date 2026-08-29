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
	"github.com/zapr-16/provider-runpod/internal/controller/fieldcmp"
)

const (
	errGetEndpoint     = "cannot get endpoint from RunPod API"
	errGetTemplate     = "cannot get template from RunPod API"
	errCreateTemplate  = "cannot create template via RunPod API"
	errCreateEndpoint  = "cannot create endpoint via RunPod API"
	errUpdateEndpoint  = "cannot update endpoint via RunPod API"
	errUpdateTemplate  = "cannot update template via RunPod API"
	errDeleteEndpoint  = "cannot delete endpoint via RunPod API"
	errNoImageName     = "imageName must be set when templateId is not set"
	errRecycleWorkers  = "cannot recycle endpoint workers after template change"
	errListEndpoints   = "cannot list endpoints from RunPod API"
	errAmbiguousCreate = "cannot recover from an ambiguous endpoint create"

	// logKeyTemplateID is the structured-logging key used to annotate log
	// lines with the RunPod template ID backing an Endpoint.
	logKeyTemplateID = "template-id"

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
		if !meta.ExternalCreateIncomplete(ep) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		// The managed reconciler refuses to call Create again while a prior
		// create's outcome is unknown, unless the external name is
		// deterministic (see managed.WithDeterministicExternalName, wired in
		// this controller's Setup). Since it is, recover instead of leaving
		// the resource stuck.
		return e.adoptIncompleteCreate(ctx, ep)
	}

	response, found, err := e.client.GetEndpoint(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetEndpoint)
	}
	if !found {
		e.log.Info("Endpoint not found in RunPod API", "external-name", externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	return e.observeResponse(ctx, ep, response, false)
}

// adoptIncompleteCreate handles Observe() when the external-name annotation
// is empty and the last recorded create attempt never confirmed success
// (meta.ExternalCreateIncomplete): the process may have crashed between the
// RunPod POST and persisting the external name, so a plain retry could
// orphan an endpoint that is already running and billing. Because Create()
// sends a deterministic name (fieldcmp.DerivedName), listing endpoints and
// matching on that exact name recovers the situation without guessing: at
// most one endpoint should ever carry it.
func (e *external) adoptIncompleteCreate(ctx context.Context, ep *v1alpha1.Endpoint) (managed.ExternalObservation, error) {
	derivedName := fieldcmp.DerivedName(ep.GetName(), string(ep.GetUID()))

	endpoints, err := e.client.ListEndpoints(ctx)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errListEndpoints)
	}

	var match *runpodclient.EndpointResponse
	for i := range endpoints {
		if endpoints[i].Name != derivedName {
			continue
		}
		if match != nil {
			return managed.ExternalObservation{}, errors.Errorf("%s: name %q matches more than one endpoint (at least %q and %q)", errAmbiguousCreate, derivedName, match.ID, endpoints[i].ID)
		}
		match = &endpoints[i]
	}

	if match == nil {
		// Nothing was ever created under this name (or RunPod never received
		// the request): a fresh Create is safe.
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	identityMatches, err := e.endpointIdentityMatches(ctx, ep.Spec.ForProvider, match)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetTemplate)
	}
	if !identityMatches {
		return managed.ExternalObservation{}, errors.Errorf("%s: endpoint %q has name %q but its template/image does not match spec.forProvider", errAmbiguousCreate, match.ID, derivedName)
	}

	e.log.Info("Recovered external-name for endpoint after an ambiguous create", "external-name", match.ID, "derived-name", derivedName)
	meta.SetExternalName(ep, match.ID)

	// ResourceLateInitialized here persists only the external-name
	// annotation recovered above; it is not spec late-init.
	return e.observeResponse(ctx, ep, match, true)
}

// endpointIdentityMatches reports whether r plausibly is the endpoint spec
// describes, used only to guard adoption after an ambiguous create: a name
// match alone is not proof, since a name collision (however unlikely with
// the uid8 suffix) must never cause the wrong billed resource to be
// adopted. In templateId mode identity is just the referenced template ID;
// in imageName mode the implicit template backing r must carry the same
// image.
func (e *external) endpointIdentityMatches(ctx context.Context, spec v1alpha1.EndpointParameters, r *runpodclient.EndpointResponse) (bool, error) {
	if spec.TemplateID != nil {
		return *spec.TemplateID == r.TemplateID, nil
	}
	if spec.ImageName == nil || r.TemplateID == "" {
		return false, nil
	}

	template, found, err := e.client.GetTemplate(ctx, r.TemplateID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	return template.ImageName == *spec.ImageName, nil
}

// observeResponse populates ep's observation and conditions from a fetched
// or adopted RunPod response and returns the resulting ExternalObservation.
// lateInit is true only when called from adoptIncompleteCreate, to persist
// the external-name annotation recovered there.
func (e *external) observeResponse(ctx context.Context, ep *v1alpha1.Endpoint, response *runpodclient.EndpointResponse, lateInit bool) (managed.ExternalObservation, error) {
	runtimeEndpoint := runtimeEndpointURL(response.ID)
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
	if templateID := ep.Spec.ForProvider.TemplateID; templateID != nil {
		// templateId mode: the referenced template is owned elsewhere, so
		// drift is just "does the endpoint point at the right template".
		upToDate = upToDate && response.TemplateID == *templateID
	} else if upToDate && response.TemplateID != "" {
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

	// lateInit is only ever true when this call came from
	// adoptIncompleteCreate, to persist the external-name annotation it just
	// recovered. This provider otherwise never late-initializes spec fields.
	return managed.ExternalObservation{
		ResourceExists:          true,
		ResourceUpToDate:        upToDate,
		ResourceLateInitialized: lateInit,
		ConnectionDetails:       connectionDetails(response.ID),
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	ep, ok := mg.(*v1alpha1.Endpoint)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotEndpoint)
	}

	// A deterministic name (base name plus a fragment of the resource UID)
	// lets Observe() recover this endpoint by listing and matching on it if
	// the controller crashes between this Create call and persisting the
	// external-name annotation, instead of leaking a billed endpoint or
	// refusing to proceed forever. See fieldcmp.DerivedName. The same name
	// is used for the implicit template created below; only the endpoint
	// name participates in adoption matching.
	name := fieldcmp.DerivedName(ep.GetName(), string(ep.GetUID()))

	if ep.Spec.ForProvider.TemplateID != nil {
		// templateId mode: the referenced template is owned elsewhere, so
		// Create never provisions a template.
		endpointID, err := e.client.CreateEndpoint(ctx, buildCreateEndpointRequest(&name, *ep.Spec.ForProvider.TemplateID, ep.Spec.ForProvider))
		if err != nil {
			return managed.ExternalCreation{}, errors.Wrap(err, errCreateEndpoint)
		}

		meta.SetExternalName(ep, endpointID)

		return managed.ExternalCreation{
			ConnectionDetails: connectionDetails(endpointID),
		}, nil
	}

	if ep.Spec.ForProvider.ImageName == nil {
		// Defensive: CEL enforces exactly one of imageName/templateId, so
		// this should be unreachable in practice.
		return managed.ExternalCreation{}, errors.New(errNoImageName)
	}

	templateID, err := e.client.CreateTemplate(ctx, runpodclient.CreateTemplateRequest{
		Name:                    name,
		ImageName:               *ep.Spec.ForProvider.ImageName,
		IsServerless:            true,
		Env:                     fieldcmp.BuildEnvMap(ep.Spec.ForProvider.Env),
		ContainerDiskInGb:       ep.Spec.ForProvider.ContainerDiskInGb,
		DockerStartCmd:          fieldcmp.CloneStrings(ep.Spec.ForProvider.DockerStartCmd),
		DockerEntrypoint:        fieldcmp.CloneStrings(ep.Spec.ForProvider.DockerEntrypoint),
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
			e.log.Info("could not clean up template after failed endpoint create", logKeyTemplateID, templateID, "error", derr)
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
	endpointPatch := runpodclient.UpdateEndpointRequest{
		GPUTypeIDs:          fieldcmp.CloneStrings(spec.GPUTypeIDs),
		GPUCount:            spec.GPUCount,
		WorkersMin:          spec.WorkersMin,
		WorkersMax:          spec.WorkersMax,
		IdleTimeout:         spec.IdleTimeout,
		FlashBoot:           spec.FlashBoot,
		ScalerType:          scalerTypeString(spec.ScalerType),
		ScalerValue:         spec.ScalerValue,
		NetworkVolumeID:     spec.NetworkVolumeID,
		DataCenterIDs:       fieldcmp.CloneStrings(spec.DataCenterIDs),
		ExecutionTimeoutMs:  spec.ExecutionTimeoutMs,
		VCPUCount:           spec.VCPUCount,
		CPUFlavorIDs:        fieldcmp.CloneStrings(spec.CPUFlavorIDs),
		AllowedCudaVersions: fieldcmp.CloneStrings(spec.AllowedCudaVersions),
		MinCudaVersion:      spec.MinCudaVersion,
		NetworkVolumeIDs:    fieldcmp.CloneStrings(spec.NetworkVolumeIDs),
	}
	if spec.TemplateID != nil {
		endpointPatch.TemplateID = spec.TemplateID
	}
	if err := e.client.UpdateEndpoint(ctx, externalName, endpointPatch); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateEndpoint)
	}
	if spec.TemplateID != nil {
		// Referenced template is owned elsewhere; nothing more to do.
		return managed.ExternalUpdate{}, nil
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

	// Detect real template drift BEFORE patching so workers are only
	// recycled when the template actually changed (runpod-rz0). A failure
	// here must propagate (retry semantics) rather than be silently
	// treated as "no drift", consistent with Observe's handling of the
	// same call.
	current, found, err := e.client.GetTemplate(ctx, templateID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errGetTemplate)
	}
	if !found {
		e.log.Info("template backing endpoint not found during update; skipping template patch", logKeyTemplateID, templateID)
		return managed.ExternalUpdate{}, nil
	}
	if !hasTemplateDrift(spec, *current) {
		return managed.ExternalUpdate{}, nil
	}

	if err := e.client.UpdateTemplate(ctx, templateID, runpodclient.UpdateTemplateRequest{
		ImageName:               spec.ImageName,
		Env:                     fieldcmp.BuildEnvMap(spec.Env),
		ContainerDiskInGb:       spec.ContainerDiskInGb,
		DockerStartCmd:          fieldcmp.CloneStrings(spec.DockerStartCmd),
		DockerEntrypoint:        fieldcmp.CloneStrings(spec.DockerEntrypoint),
		ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateTemplate)
	}

	return managed.ExternalUpdate{}, e.recycleWorkers(ctx, externalName, spec)
}

// recycleWorkers cycles workersMax through 0 after an implicit template
// change so live/idle workers (FlashBoot standby included) pick up the new
// template; they never do so on their own. No-op when recycling is
// explicitly disabled. Only runs when spec.WorkersMax is set: that value is
// what gets restored, and it is also what the next reconcile's endpoint
// PATCH carries, so a transient restore failure self-heals on retry. There
// is deliberately no GET-fallback to discover a restore value — if the
// restore PATCH below then failed, a later reconcile would have no way to
// tell "0 because we're mid-recycle" from "0 because that's the desired
// state", and could permanently pin the endpoint at workersMax:0.
func (e *external) recycleWorkers(ctx context.Context, externalName string, spec v1alpha1.EndpointParameters) error {
	if spec.RecycleWorkersOnTemplateChange != nil && !*spec.RecycleWorkersOnTemplateChange {
		return nil
	}

	if spec.WorkersMax == nil {
		e.log.Info("skipping worker recycle: spec.workersMax not set; set it to enable recycling", "endpoint-id", externalName)
		return nil
	}

	zero := int32(0)
	if err := e.client.UpdateEndpoint(ctx, externalName, runpodclient.UpdateEndpointRequest{WorkersMax: &zero}); err != nil {
		return errors.Wrap(err, errRecycleWorkers)
	}
	if err := e.client.UpdateEndpoint(ctx, externalName, runpodclient.UpdateEndpointRequest{WorkersMax: spec.WorkersMax}); err != nil {
		return errors.Wrap(err, errRecycleWorkers)
	}
	return nil
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
	// endpoint observation is the only durable record of its ID. Skipped in
	// templateId mode: the referenced template is owned elsewhere and must
	// never be deleted by this controller.
	var templateID string
	if ep.Spec.ForProvider.TemplateID == nil {
		templateID = ep.Status.AtProvider.TemplateID
		if templateID == "" {
			if response, found, err := e.client.GetEndpoint(ctx, externalName); err == nil && found {
				templateID = response.TemplateID
			}
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
			e.log.Info("could not delete template backing endpoint", logKeyTemplateID, templateID, "error", err)
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
		GPUTypeIDs:          fieldcmp.CloneStrings(spec.GPUTypeIDs),
		GPUCount:            spec.GPUCount,
		WorkersMin:          spec.WorkersMin,
		WorkersMax:          spec.WorkersMax,
		IdleTimeout:         spec.IdleTimeout,
		FlashBoot:           spec.FlashBoot,
		ScalerType:          scalerTypeString(spec.ScalerType),
		ScalerValue:         spec.ScalerValue,
		NetworkVolumeID:     spec.NetworkVolumeID,
		DataCenterIDs:       fieldcmp.CloneStrings(spec.DataCenterIDs),
		ExecutionTimeoutMs:  spec.ExecutionTimeoutMs,
		ComputeType:         spec.ComputeType,
		VCPUCount:           spec.VCPUCount,
		CPUFlavorIDs:        fieldcmp.CloneStrings(spec.CPUFlavorIDs),
		AllowedCudaVersions: fieldcmp.CloneStrings(spec.AllowedCudaVersions),
		MinCudaVersion:      spec.MinCudaVersion,
		NetworkVolumeIDs:    fieldcmp.CloneStrings(spec.NetworkVolumeIDs),
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
	if len(spec.GPUTypeIDs) > 0 && !fieldcmp.StringSlicesEqual(spec.GPUTypeIDs, observed.GPUTypeIDs) {
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
	if spec.ImageName != nil && *spec.ImageName != observed.ImageName {
		return true
	}
	if spec.ContainerDiskInGb != nil && *spec.ContainerDiskInGb != observed.ContainerDiskInGb {
		return true
	}
	// Nil and empty both mean "unmanaged" (see hasEndpointDrift).
	if len(spec.Env) > 0 && !fieldcmp.StringMapsEqual(fieldcmp.BuildEnvMap(spec.Env), observed.Env) {
		return true
	}
	// GET /templates echoes these, unlike the endpoint-level fields above, so
	// they get real drift detection. Command arrays are order-sensitive.
	if len(spec.DockerStartCmd) > 0 && !fieldcmp.StringSlicesEqual(spec.DockerStartCmd, observed.DockerStartCmd) {
		return true
	}
	if len(spec.DockerEntrypoint) > 0 && !fieldcmp.StringSlicesEqual(spec.DockerEntrypoint, observed.DockerEntrypoint) {
		return true
	}
	if stringPtrDrifts(spec.ContainerRegistryAuthID, observed.ContainerRegistryAuthID) {
		return true
	}
	return false
}

// countRunningWorkers counts workers with desiredStatus RUNNING: the workers
// array is the only place RunPod returns per-worker status, and
// desiredStatus is the only status field it carries.
func countRunningWorkers(workers []runpodclient.EndpointWorker) int32 {
	var n int32
	for _, w := range workers {
		if w.DesiredStatus == "RUNNING" {
			n++
		}
	}
	return n
}

// runtimeEndpointURL builds the serverless data-plane base URL for an
// endpoint ID, shared by Observe() and connectionDetails() so both compute
// it identically.
func runtimeEndpointURL(id string) string {
	return fmt.Sprintf("%s/%s", dataPlaneBaseURL, id)
}

func connectionDetails(endpointID string) managed.ConnectionDetails {
	base := runtimeEndpointURL(endpointID)
	return managed.ConnectionDetails{
		"endpointId":                      []byte(endpointID),
		xpv2.CredentialsSecretEndpointKey: []byte(base),
		"openaiUrl":                       []byte(base + "/openai/v1"),
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
