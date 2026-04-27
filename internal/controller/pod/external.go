package pod

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/pkg/resource"
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
)

type external struct {
	client *runpodclient.Client
	log    logr.Logger
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
		e.log.Info("Pod not found in RunPod API", "external-name", externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	networkingReady := response.PublicIP != "" && response.PortMappings != nil
	endpoint, resolvedPort := resolveConnectionTarget(pod.Spec.ForProvider.Ports, response.PublicIP, response.PortMappings)
	gpuDisplayName := response.Machine.GPUDisplayName
	if gpuDisplayName == "" {
		gpuDisplayName = response.GPU.DisplayName
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

	lateInitialized := pod.Status.AtProvider.PodID == ""
	pod.Status.AtProvider = atProvider

	switch response.DesiredStatus {
	case "RUNNING":
		if networkingReady {
			pod.SetConditions(xpv1.Available())
		} else {
			pod.SetConditions(xpv1.Creating())
		}
	case "EXITED", "TERMINATED":
		pod.SetConditions(xpv1.Unavailable())
		// Spot reclaim / OOM / manual console delete all leave the pod
		// stuck here forever unless we explicitly tell Crossplane the
		// resource is gone — which causes the next reconcile to call
		// Create() and provision a fresh pod with the same spec.
		// Opt-in via spec.forProvider.recreateOnTerminate.
		if pod.Spec.ForProvider.RecreateOnTerminate != nil && *pod.Spec.ForProvider.RecreateOnTerminate {
			e.log.Info("Pod terminated; clearing external-name to trigger auto-recreate",
				"external-name", externalName, "status", response.DesiredStatus)
			if anns := pod.GetAnnotations(); anns != nil {
				delete(anns, meta.AnnotationKeyExternalName)
			}
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
	default:
		e.log.Info("RunPod returned unknown desiredStatus", "status", response.DesiredStatus, "external-name", externalName)
		pod.SetConditions(xpv1.Unavailable())
	}

	envDrift := hasEnvDrift(pod.Spec.ForProvider.Env, response.Env)
	portsDrift := hasPortsDrift(pod.Spec.ForProvider.Ports, response.Ports)

	connectionDetails := managed.ConnectionDetails{
		"podId": []byte(externalName),
	}
	if endpoint != "" {
		connectionDetails["endpoint"] = []byte(endpoint)
	}
	if resolvedPort != "" {
		connectionDetails["port"] = []byte(resolvedPort)
	}

	return managed.ExternalObservation{
		ResourceExists:          true,
		ResourceUpToDate:        !(envDrift || portsDrift),
		ResourceLateInitialized: lateInitialized,
		ConnectionDetails:       connectionDetails,
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotPod)
	}

	req := runpodclient.CreatePodRequest{
		ImageName:         pod.Spec.ForProvider.ImageName,
		GPUTypeIDs:        cloneStrings(pod.Spec.ForProvider.GPUTypeIDs),
		GPUCount:          pod.Spec.ForProvider.GPUCount,
		SupportPublicIP:   pod.Spec.ForProvider.SupportPublicIP,
		ContainerDiskInGb: pod.Spec.ForProvider.ContainerDiskInGb,
		VolumeInGb:        pod.Spec.ForProvider.VolumeInGb,
		VolumeMountPath:   pod.Spec.ForProvider.VolumeMountPath,
		Env:               buildEnvMap(pod.Spec.ForProvider.Env),
		Ports:             buildPortTokens(pod.Spec.ForProvider.Ports),
		DockerStartCmd:    cloneStrings(pod.Spec.ForProvider.DockerStartCmd),
	}
	if pod.Spec.ForProvider.CloudType != nil {
		cloudType := string(*pod.Spec.ForProvider.CloudType)
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

func (e *external) Update(_ context.Context, _ xpresource.Managed) (managed.ExternalUpdate, error) {
	e.log.V(1).Info("Pod is immutable; Update is a no-op")
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

func hasEnvDrift(desired []v1alpha1.EnvVar, observed map[string]string) bool {
	if desired == nil {
		return false
	}

	want := map[string]string{}
	for _, env := range desired {
		want[env.Name] = env.Value
	}

	return !stringMapsEqual(want, observed)
}

func hasPortsDrift(desired []v1alpha1.Port, observed []string) bool {
	if desired == nil {
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

func resolveConnectionTarget(ports []v1alpha1.Port, publicIP string, mappings map[string]int32) (string, string) {
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

		portString := strconv.Itoa(int(externalPort))
		if fallback == "" {
			fallback = portString
		}

		if strings.EqualFold(normalizeProtocol(port.Protocol), "http") {
			return fmt.Sprintf("http://%s:%s", publicIP, portString), portString
		}
	}

	return "", fallback
}

func normalizePortToken(number int32, protocol string) string {
	return fmt.Sprintf("%d/%s", number, normalizeProtocol(protocol))
}

func normalizeObservedToken(token string) string {
	parts := strings.SplitN(strings.ToLower(token), "/", 2)
	if len(parts) == 1 {
		return fmt.Sprintf("%s/%s", parts[0], normalizeProtocol(""))
	}
	return fmt.Sprintf("%s/%s", parts[0], normalizeProtocol(parts[1]))
}

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
