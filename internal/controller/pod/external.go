package pod

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errGetPod         = "cannot get pod from RunPod API"
	errParseStartedAt = "cannot parse pod lastStartedAt timestamp"
	errCreatePod      = "cannot create pod via RunPod API"
	errDeletePod      = "cannot delete pod via RunPod API"
	errUpdatePod      = "cannot update pod via RunPod API"
	errStartPod       = "cannot start pod via RunPod API"
	errStopPod        = "cannot stop pod via RunPod API"

	// logKeyExternalName is the structured-logging key used to annotate log
	// lines with the RunPod external-name (pod ID).
	logKeyExternalName = "external-name"
)

type external struct {
	client *runpodclient.Client
	log    logr.Logger
	// probeHTTP reports whether an HTTP(S) endpoint is answering. The
	// RunPod proxy URL resolves from the pod ID alone and returns 502
	// until the workload listens, so existence of the URL is not enough
	// to mark the pod Available.
	probeHTTP func(ctx context.Context, url string) bool
}

// proxyProbeClient deliberately uses a shorter timeout than the API client:
// the probe runs inside Observe on every poll and a hung proxy should not
// stall reconciliation.
var proxyProbeClient = &http.Client{Timeout: 5 * time.Second}

func defaultHTTPProbe(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := proxyProbeClient.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	// Anything below 500 (including 404/401 from the workload itself)
	// proves the container is listening; 502/503/504 come from the proxy
	// while the backend is still down.
	return resp.StatusCode < http.StatusInternalServerError
}

func (e *external) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotPod)
	}

	externalName := meta.GetExternalName(pod)
	if externalName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	response, found, err := e.client.GetPod(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetPod)
	}
	if !found {
		e.log.Info("Pod not found in RunPod API", logKeyExternalName, externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	endpoint, resolvedPort := resolveConnectionTarget(pod.Spec.ForProvider.Ports, response.ID, response.PublicIP, response.PortMappings)
	networkingReady := endpoint != "" || (response.PublicIP != "" && response.PortMappings != nil)
	if endpoint != "" && response.DesiredStatus == "RUNNING" && e.probeHTTP != nil {
		networkingReady = e.probeHTTP(ctx, endpoint)
	}
	gpuDisplayName := response.Machine.GPUDisplayName
	if gpuDisplayName == "" {
		gpuDisplayName = response.GPU.DisplayName
	}
	if gpuDisplayName == "" {
		// The REST API reports the GPU under machine.gpuTypeId only.
		gpuDisplayName = response.Machine.GPUTypeID
	}

	atProvider := v1alpha1.PodObservation{
		PodID:           response.ID,
		DesiredStatus:   response.DesiredStatus,
		PublicIP:        response.PublicIP,
		PortMappings:    clonePortMappings(response.PortMappings),
		RuntimeEndpoint: endpoint,
		CostPerHr:       response.CostPerHr,
		GPUDisplayName:  gpuDisplayName,
		NetworkingReady: networkingReady,
	}

	if response.LastStartedAt != "" {
		parsed, err := parsePodStartedAt(response.LastStartedAt)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errParseStartedAt)
		}
		startedAt := metav1.NewTime(parsed)
		atProvider.LastStartedAt = &startedAt
	}

	pod.Status.AtProvider = atProvider

	switch response.DesiredStatus {
	case "RUNNING":
		if networkingReady {
			pod.SetConditions(xpv2.Available())
		} else {
			pod.SetConditions(xpv2.Creating())
		}
	case "EXITED", "TERMINATED":
		if response.DesiredStatus == "EXITED" && desiredStateOrDefault(pod.Spec.ForProvider) == "EXITED" {
			// Stopped on purpose IS the desired state.
			pod.SetConditions(xpv2.Available())
			break
		}
		pod.SetConditions(xpv2.Unavailable())
		// Spot reclaim / OOM / manual console delete all leave the pod
		// stuck here forever unless we explicitly tell Crossplane the
		// resource is gone — which causes the next reconcile to call
		// Create() and provision a fresh pod with the same spec.
		// Opt-in via spec.forProvider.recreateOnTerminate.
		if pod.Spec.ForProvider.RecreateOnTerminate != nil && *pod.Spec.ForProvider.RecreateOnTerminate {
			// Terminated pods retain their disk and keep billing storage,
			// and the external-name is the only record of the old ID — so
			// the old pod must be deleted before the ID is dropped. On
			// failure the ID is kept and the next reconcile retries.
			if err := e.client.DeletePod(ctx, externalName); err != nil {
				return managed.ExternalObservation{}, errors.Wrap(err, errDeletePod)
			}
			e.log.Info("Pod terminated; deleted old pod and clearing external-name to trigger auto-recreate",
				logKeyExternalName, externalName, "status", response.DesiredStatus)
			if anns := pod.GetAnnotations(); anns != nil {
				delete(anns, meta.AnnotationKeyExternalName)
			}
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
	default:
		e.log.Info("RunPod returned unknown desiredStatus", "status", response.DesiredStatus, logKeyExternalName, externalName)
		pod.SetConditions(xpv2.Unavailable())
	}

	// Immutable spec fields can never be reconciled in place; surface their
	// drift via status only. Mutable-field drift and lifecycle drift are
	// reconciled by Update() via PATCH + start/stop.
	pod.Status.AtProvider.DriftDetected = hasImmutableDrift(pod.Spec.ForProvider, response)
	upToDate := !hasMutableDrift(pod.Spec.ForProvider, response) &&
		!hasLifecycleDrift(pod.Spec.ForProvider, response.DesiredStatus)

	connectionDetails := managed.ConnectionDetails{
		"podId": []byte(externalName),
	}
	if endpoint != "" {
		connectionDetails[xpv2.CredentialsSecretEndpointKey] = []byte(endpoint)
	}
	if resolvedPort != "" {
		connectionDetails[xpv2.CredentialsSecretPortKey] = []byte(resolvedPort)
	}

	// Never report ResourceLateInitialized: this provider does not
	// late-initialize spec fields, and reporting it makes the reconciler
	// issue a spec update that resets pending status changes — atProvider
	// would never persist.
	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  upToDate,
		ConnectionDetails: connectionDetails,
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotPod)
	}

	spec := pod.Spec.ForProvider
	name := pod.GetName()
	req := runpodclient.CreatePodRequest{
		Name:                    &name,
		ImageName:               spec.ImageName,
		GPUTypeIDs:              cloneStrings(spec.GPUTypeIDs),
		GPUCount:                spec.GPUCount,
		SupportPublicIP:         spec.SupportPublicIP,
		ContainerDiskInGb:       spec.ContainerDiskInGb,
		VolumeInGb:              spec.VolumeInGb,
		VolumeMountPath:         spec.VolumeMountPath,
		Env:                     buildEnvMap(spec.Env),
		Ports:                   buildPortTokens(spec.Ports),
		DockerStartCmd:          cloneStrings(spec.DockerStartCmd),
		DockerEntrypoint:        cloneStrings(spec.DockerEntrypoint),
		ComputeType:             spec.ComputeType,
		VCPUCount:               spec.VCPUCount,
		CPUFlavorIDs:            cloneStrings(spec.CPUFlavorIDs),
		CPUFlavorPriority:       spec.CPUFlavorPriority,
		DataCenterIDs:           cloneStrings(spec.DataCenterIDs),
		DataCenterPriority:      spec.DataCenterPriority,
		GPUTypePriority:         spec.GPUTypePriority,
		CountryCodes:            cloneStrings(spec.CountryCodes),
		Interruptible:           spec.Interruptible,
		Locked:                  spec.Locked,
		GlobalNetworking:        spec.GlobalNetworking,
		VolumeEncrypted:         spec.VolumeEncrypted,
		AllowedCudaVersions:     cloneStrings(spec.AllowedCudaVersions),
		MinRAMPerGPU:            spec.MinRAMPerGPU,
		MinVCPUPerGPU:           spec.MinVCPUPerGPU,
		MinDiskBandwidthMBps:    spec.MinDiskBandwidthMBps,
		MinDownloadMbps:         spec.MinDownloadMbps,
		MinUploadMbps:           spec.MinUploadMbps,
		TemplateID:              spec.TemplateID,
		NetworkVolumeID:         spec.NetworkVolumeID,
		ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
	}
	if spec.CloudType != nil {
		cloudType := string(*spec.CloudType)
		req.CloudType = &cloudType
	}

	podID, err := e.client.CreatePod(ctx, req)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreatePod)
	}

	meta.SetExternalName(pod, podID)

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{
			"podId": []byte(podID),
		},
	}, nil
}

func (e *external) Update(ctx context.Context, mg xpresource.Managed) (managed.ExternalUpdate, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotPod)
	}
	externalName := meta.GetExternalName(pod)
	if externalName == "" {
		return managed.ExternalUpdate{}, nil
	}

	spec := pod.Spec.ForProvider
	response, found, err := e.client.GetPod(ctx, externalName)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errGetPod)
	}
	if !found {
		return managed.ExternalUpdate{}, nil
	}

	if hasMutableDrift(spec, response) {
		if err := e.client.UpdatePod(ctx, externalName, runpodclient.UpdatePodRequest{
			ImageName:               spec.ImageName,
			ContainerDiskInGb:       spec.ContainerDiskInGb,
			VolumeInGb:              spec.VolumeInGb,
			VolumeMountPath:         spec.VolumeMountPath,
			Env:                     buildEnvMap(spec.Env),
			Ports:                   buildPortTokens(spec.Ports),
			DockerStartCmd:          cloneStrings(spec.DockerStartCmd),
			DockerEntrypoint:        cloneStrings(spec.DockerEntrypoint),
			Locked:                  spec.Locked,
			GlobalNetworking:        spec.GlobalNetworking,
			ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
		}); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errUpdatePod)
		}
	}

	if hasLifecycleDrift(spec, response.DesiredStatus) {
		if desiredStateOrDefault(spec) == "EXITED" {
			if err := e.client.StopPod(ctx, externalName); err != nil {
				return managed.ExternalUpdate{}, errors.Wrap(err, errStopPod)
			}
		} else {
			if err := e.client.StartPod(ctx, externalName); err != nil {
				return managed.ExternalUpdate{}, errors.Wrap(err, errStartPod)
			}
		}
	}

	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg xpresource.Managed) (managed.ExternalDelete, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotPod)
	}

	externalName := meta.GetExternalName(pod)
	if externalName == "" {
		return managed.ExternalDelete{}, nil
	}

	if err := e.client.DeletePod(ctx, externalName); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDeletePod)
	}

	return managed.ExternalDelete{}, nil
}

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

// hasEnvDrift reports whether declared env vars diverge from the running
// pod. Nil and empty both mean "unmanaged": an empty value could never be
// pushed to the API anyway (payloads use omitempty).
func hasEnvDrift(desired []v1alpha1.EnvVar, observed map[string]string) bool {
	if len(desired) == 0 {
		return false
	}

	want := map[string]string{}
	for _, env := range desired {
		want[env.Name] = env.Value
	}

	return !stringMapsEqual(want, observed)
}

// hasPortsDrift reports whether declared ports diverge from the running
// pod. Nil and empty both mean "unmanaged": an empty value could never be
// pushed to the API anyway (payloads use omitempty).
func hasPortsDrift(desired []v1alpha1.Port, observed []string) bool {
	if len(desired) == 0 {
		return false
	}

	want := map[string]struct{}{}
	for _, port := range desired {
		want[normalizePortToken(port.Number, port.Protocol)] = struct{}{}
	}

	got := map[string]struct{}{}
	for _, port := range observed {
		got[normalizeObservedToken(port)] = struct{}{}
	}

	return !stringSetEqual(want, got)
}

// resolveConnectionTarget derives the primary connection endpoint for a pod.
// HTTP ports are served through RunPod's TLS proxy at
// https://{podID}-{containerPort}.proxy.runpod.net — COMMUNITY pods with
// http-only ports never receive a public IP, so the proxy is the only
// route to them. TCP ports fall back to publicIP:externalPort mappings.
func resolveConnectionTarget(ports []v1alpha1.Port, podID, publicIP string, mappings map[string]int32) (string, string) {
	if podID != "" {
		for _, port := range ports {
			if normalizeProtocol(port.Protocol) != "http" {
				continue
			}
			portString := strconv.Itoa(int(port.Number))
			return fmt.Sprintf("https://%s-%s.proxy.runpod.net", podID, portString), portString
		}
	}

	if len(ports) == 0 || publicIP == "" || mappings == nil {
		return "", ""
	}

	var fallback string
	for _, port := range ports {
		token := normalizePortToken(port.Number, port.Protocol)
		externalPort, ok := mappings[token]
		if !ok {
			continue
		}
		if fallback == "" {
			fallback = strconv.Itoa(int(externalPort))
		}
	}

	return "", fallback
}

// normalizePortToken builds a "<port>/<protocol>" token from spec fields;
// the protocol is always appended, defaulting to tcp when unset.
func normalizePortToken(number int32, protocol string) string {
	return fmt.Sprintf("%d/%s", number, normalizeProtocol(protocol))
}

// normalizeObservedToken parses a RunPod "<port>/<protocol>" token; a
// missing protocol segment defaults to tcp.
func normalizeObservedToken(token string) string {
	parts := strings.SplitN(strings.ToLower(token), "/", 2)
	if len(parts) == 1 {
		return fmt.Sprintf("%s/%s", parts[0], normalizeProtocol(""))
	}
	return fmt.Sprintf("%s/%s", parts[0], normalizeProtocol(parts[1]))
}

// normalizeProtocol lowercases the protocol, defaulting empty to tcp.
func normalizeProtocol(protocol string) string {
	if protocol == "" {
		return "tcp"
	}
	return strings.ToLower(protocol)
}

func parsePodStartedAt(value string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	}

	var lastErr error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, nil
		}
		lastErr = err
	}

	return time.Time{}, lastErr
}

// desiredStateOrDefault returns the lifecycle target; unset means RUNNING.
func desiredStateOrDefault(spec v1alpha1.PodParameters) string {
	if spec.DesiredState != nil {
		return *spec.DesiredState
	}
	return "RUNNING"
}

// hasMutableDrift reports whether any PATCHable spec field diverges from
// the observation. Nil/empty spec fields never drift (unmanaged).
func hasMutableDrift(spec v1alpha1.PodParameters, r *runpodclient.PodResponse) bool {
	if spec.ImageName != nil && *spec.ImageName != r.Image {
		return true
	}
	if spec.ContainerDiskInGb != nil && *spec.ContainerDiskInGb != r.ContainerDiskInGb {
		return true
	}
	if spec.VolumeInGb != nil && *spec.VolumeInGb != r.VolumeInGb {
		return true
	}
	if spec.VolumeMountPath != nil && *spec.VolumeMountPath != r.VolumeMountPath {
		return true
	}
	if spec.Locked != nil && *spec.Locked != r.Locked {
		return true
	}
	if spec.ContainerRegistryAuthID != nil && *spec.ContainerRegistryAuthID != r.ContainerRegistryAuthID {
		return true
	}
	if len(spec.DockerStartCmd) > 0 && !stringSlicesEqual(spec.DockerStartCmd, r.DockerStartCmd) {
		return true
	}
	if len(spec.DockerEntrypoint) > 0 && !stringSlicesEqual(spec.DockerEntrypoint, r.DockerEntrypoint) {
		return true
	}
	// globalNetworking is write-only (never echoed) and excluded, like
	// dataCenterIds on Endpoint.
	return hasEnvDrift(spec.Env, r.Env) || hasPortsDrift(spec.Ports, r.Ports)
}

// hasImmutableDrift reports whether immutable spec fields diverge from the
// running pod. Immutable drift can only come from editing the spec after
// creation; it can never be reconciled in place, so it is surfaced via
// status.atProvider.driftDetected instead of blocking the Synced condition.
// Only fields the GET response reliably echoes participate: the observed
// GPU type must be one of the acceptable spec types (the list is a
// priority order, so membership — not equality — is the correct check),
// and the Spot/interruptible flag must match.
func hasImmutableDrift(spec v1alpha1.PodParameters, r *runpodclient.PodResponse) bool {
	if spec.Interruptible != nil && *spec.Interruptible != r.Interruptible {
		return true
	}
	if len(spec.GPUTypeIDs) > 0 && r.Machine.GPUTypeID != "" {
		for _, id := range spec.GPUTypeIDs {
			if id == r.Machine.GPUTypeID {
				return false
			}
		}
		return true
	}
	return false
}

// hasLifecycleDrift reports whether the observed lifecycle status diverges
// from spec.desiredState. Only RUNNING/EXITED participate; TERMINATED is
// handled by the recreateOnTerminate path.
func hasLifecycleDrift(spec v1alpha1.PodParameters, observedStatus string) bool {
	if observedStatus != "RUNNING" && observedStatus != "EXITED" {
		return false
	}
	return desiredStateOrDefault(spec) != observedStatus
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

func stringSetEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func clonePortMappings(in map[string]int32) map[string]int32 {
	if in == nil {
		return nil
	}
	out := make(map[string]int32, len(in))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = in[k]
	}
	return out
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

func buildPortTokens(in []v1alpha1.Port) []string {
	if len(in) == 0 {
		return nil
	}

	out := make([]string, 0, len(in))
	for _, port := range in {
		out = append(out, normalizePortToken(port.Number, port.Protocol))
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
