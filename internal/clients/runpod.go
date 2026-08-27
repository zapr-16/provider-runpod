package clients

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
)

const (
	defaultBaseURL        = "https://rest.runpod.io/v1"
	errExtractCredentials = "cannot extract RunPod API key from ProviderConfig"
	errEmptyCredentials   = "RunPod API key is empty"
	errCreateRequest      = "cannot create RunPod request"
	errDoRequest          = "cannot execute RunPod request"
	errDecodeResponse     = "cannot decode RunPod response"
	errInvalidResourceID  = "invalid RunPod resource identifier"

	podsPath       = "/pods"
	podsPathPrefix = podsPath + "/"
)

// resourceIDPattern matches RunPod resource identifiers (pod, endpoint, and
// template IDs). IDs are interpolated into URL paths, so anything outside
// this alphabet (path separators, dots, query/fragment markers) must be
// rejected: external-names are user-controlled via annotation and could
// otherwise redirect an authenticated request to a different API object.
var resourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// validateResourceID rejects any ID not matching resourceIDPattern.
func validateResourceID(id string) error {
	if !resourceIDPattern.MatchString(id) {
		return errors.Errorf("%s: %q", errInvalidResourceID, id)
	}
	return nil
}

// Client wraps an HTTP client configured for the RunPod REST API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// CreatePodRequest mirrors the RunPod pod create payload (POST /pods, 1:1).
type CreatePodRequest struct {
	Name                    *string           `json:"name,omitempty"`
	ImageName               *string           `json:"imageName,omitempty"`
	GPUTypeIDs              []string          `json:"gpuTypeIds,omitempty"`
	GPUCount                *int32            `json:"gpuCount,omitempty"`
	CloudType               *string           `json:"cloudType,omitempty"`
	SupportPublicIP         *bool             `json:"supportPublicIp,omitempty"`
	ContainerDiskInGb       *int32            `json:"containerDiskInGb,omitempty"`
	VolumeInGb              *int32            `json:"volumeInGb,omitempty"`
	VolumeMountPath         *string           `json:"volumeMountPath,omitempty"`
	Env                     map[string]string `json:"env,omitempty"`
	Ports                   []string          `json:"ports,omitempty"`
	DockerStartCmd          []string          `json:"dockerStartCmd,omitempty"`
	DockerEntrypoint        []string          `json:"dockerEntrypoint,omitempty"`
	ComputeType             *string           `json:"computeType,omitempty"`
	VCPUCount               *int32            `json:"vcpuCount,omitempty"`
	CPUFlavorIDs            []string          `json:"cpuFlavorIds,omitempty"`
	CPUFlavorPriority       *string           `json:"cpuFlavorPriority,omitempty"`
	DataCenterIDs           []string          `json:"dataCenterIds,omitempty"`
	DataCenterPriority      *string           `json:"dataCenterPriority,omitempty"`
	GPUTypePriority         *string           `json:"gpuTypePriority,omitempty"`
	CountryCodes            []string          `json:"countryCodes,omitempty"`
	Interruptible           *bool             `json:"interruptible,omitempty"`
	Locked                  *bool             `json:"locked,omitempty"`
	GlobalNetworking        *bool             `json:"globalNetworking,omitempty"`
	VolumeEncrypted         *bool             `json:"volumeEncrypted,omitempty"`
	AllowedCudaVersions     []string          `json:"allowedCudaVersions,omitempty"`
	MinRAMPerGPU            *int32            `json:"minRAMPerGPU,omitempty"`
	MinVCPUPerGPU           *int32            `json:"minVCPUPerGPU,omitempty"`
	MinDiskBandwidthMBps    *int32            `json:"minDiskBandwidthMBps,omitempty"`
	MinDownloadMbps         *int32            `json:"minDownloadMbps,omitempty"`
	MinUploadMbps           *int32            `json:"minUploadMbps,omitempty"`
	TemplateID              *string           `json:"templateId,omitempty"`
	NetworkVolumeID         *string           `json:"networkVolumeId,omitempty"`
	ContainerRegistryAuthID *string           `json:"containerRegistryAuthId,omitempty"`
}

// UpdatePodRequest mirrors the RunPod pod PATCH payload. RunPod applies
// container-level changes (image, env, ...) when the pod next (re)starts.
type UpdatePodRequest struct {
	Name                    *string           `json:"name,omitempty"`
	ImageName               *string           `json:"imageName,omitempty"`
	ContainerDiskInGb       *int32            `json:"containerDiskInGb,omitempty"`
	VolumeInGb              *int32            `json:"volumeInGb,omitempty"`
	VolumeMountPath         *string           `json:"volumeMountPath,omitempty"`
	Env                     map[string]string `json:"env,omitempty"`
	Ports                   []string          `json:"ports,omitempty"`
	DockerStartCmd          []string          `json:"dockerStartCmd,omitempty"`
	DockerEntrypoint        []string          `json:"dockerEntrypoint,omitempty"`
	Locked                  *bool             `json:"locked,omitempty"`
	GlobalNetworking        *bool             `json:"globalNetworking,omitempty"`
	ContainerRegistryAuthID *string           `json:"containerRegistryAuthId,omitempty"`
}

// PodResponse mirrors the subset of the RunPod Pod GET response needed by Observe().
type PodResponse struct {
	ID                      string            `json:"id"`
	DesiredStatus           string            `json:"desiredStatus"`
	PublicIP                string            `json:"publicIp"`
	PortMappings            map[string]int32  `json:"portMappings"`
	CostPerHr               float64           `json:"costPerHr"`
	LastStartedAt           string            `json:"lastStartedAt"`
	Env                     map[string]string `json:"env"`
	Ports                   []string          `json:"ports"`
	Image                   string            `json:"image"`
	DockerStartCmd          []string          `json:"dockerStartCmd"`
	DockerEntrypoint        []string          `json:"dockerEntrypoint"`
	ContainerDiskInGb       int32             `json:"containerDiskInGb"`
	VolumeInGb              int32             `json:"volumeInGb"`
	VolumeMountPath         string            `json:"volumeMountPath"`
	Locked                  bool              `json:"locked"`
	Interruptible           bool              `json:"interruptible"`
	ContainerRegistryAuthID string            `json:"containerRegistryAuthId"`
	GPU                     struct {
		DisplayName string `json:"displayName"`
	} `json:"gpu"`
	Machine struct {
		GPUDisplayName string `json:"gpuDisplayName"`
		GPUTypeID      string `json:"gpuTypeId"`
	} `json:"machine"`
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the RunPod REST base URL.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = u }
}

// WithHTTPClient overrides the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// NewClient returns a RunPod client with the default REST base URL.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		apiKey:     apiKey,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewRequest creates an authenticated RunPod API request.
func (c *Client) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// Do executes an HTTP request with the configured client.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

// ClientFromCredentials builds an authenticated RunPod client from the
// common credential selectors carried by either ProviderConfig kind. Any
// opts are forwarded to NewClient (e.g. WithBaseURL, used by tests to point
// the credential reconcilers at an httptest server).
func ClientFromCredentials(ctx context.Context, kube client.Client, creds xpv2.CommonCredentialSelectors, opts ...Option) (*Client, error) {
	apiKey, err := xpresource.ExtractSecret(ctx, kube, creds)
	if err != nil {
		return nil, errors.Wrap(err, errExtractCredentials)
	}

	if strings.TrimSpace(string(apiKey)) == "" {
		return nil, errors.New(errEmptyCredentials)
	}

	return NewClient(string(apiKey), opts...), nil
}

// ClientFromProviderConfig builds an authenticated RunPod client from a
// namespaced ProviderConfig. The credentials' secretRef, if set, has no
// namespace field, so it is always resolved in the ProviderConfig's own
// namespace (pc.Namespace) - a namespace-scoped tenant can never read a
// secret living elsewhere in the cluster.
func ClientFromProviderConfig(ctx context.Context, kube client.Client, pc *v1beta1.ProviderConfig) (*Client, error) {
	return ClientFromCredentials(ctx, kube, pc.Spec.Credentials.ToCommonCredentialSelectors(pc.Namespace))
}

// ClientFromClusterProviderConfig builds an authenticated RunPod client from
// a cluster-scoped ClusterProviderConfig.
func ClientFromClusterProviderConfig(ctx context.Context, kube client.Client, pc *v1beta1.ClusterProviderConfig) (*Client, error) {
	return ClientFromCredentials(ctx, kube, pc.Spec.Credentials)
}

// Ping performs a cheap authenticated call (GET /pods) to check that the
// configured API key is actually accepted by the RunPod API. Any 2xx status
// means the key works; 401/403 means the key is invalid or revoked; any
// other status is treated as a transient failure (rate limiting, an outage,
// ...) rather than a credentials problem.
func (c *Client) Ping(ctx context.Context) error {
	req, err := c.NewRequest(ctx, http.MethodGet, podsPath, nil)
	if err != nil {
		return errors.Wrap(err, errCreateRequest)
	}

	resp, err := c.Do(req)
	if err != nil {
		return errors.Wrap(err, errDoRequest)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	return errors.Errorf("RunPod GET %s returned status %d: %s", podsPath, resp.StatusCode, readErrorBody(resp.Body))
}

// GetPod retrieves a pod observation payload from the RunPod API. Only a
// 404 means "not found": any other non-2xx (429, 401, 5xx, ...) is an error,
// because reporting a transient failure as absence would make the reconciler
// create a duplicate pod and orphan the running, still-billing original.
func (c *Client) GetPod(ctx context.Context, podID string) (*PodResponse, bool, error) {
	if err := validateResourceID(podID); err != nil {
		return nil, false, err
	}

	var out PodResponse
	found, err := c.getStrict(ctx, podsPathPrefix+podID+"?includeMachine=true", &out)
	if err != nil || !found {
		return nil, found, err
	}
	return &out, true, nil
}

// CreatePod creates a new RunPod pod and returns its pod ID.
func (c *Client) CreatePod(ctx context.Context, payload CreatePodRequest) (string, error) {
	var out PodResponse
	if err := c.doJSON(ctx, http.MethodPost, podsPath, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// DeletePod deletes a RunPod pod. Already-gone responses (404/410) count as
// success; any other non-2xx is returned as an error so the reconciler
// retries instead of dropping the finalizer while the pod keeps billing.
func (c *Client) DeletePod(ctx context.Context, podID string) error {
	if err := validateResourceID(podID); err != nil {
		return err
	}
	return c.deleteStrict(ctx, podsPathPrefix+podID)
}

// UpdatePod patches mutable fields of a RunPod pod. RunPod applies
// container-level changes when the pod next (re)starts.
func (c *Client) UpdatePod(ctx context.Context, podID string, payload UpdatePodRequest) error {
	if err := validateResourceID(podID); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPatch, podsPathPrefix+podID, payload, nil)
}

// StartPod starts or resumes a stopped pod (POST /pods/{podId}/start).
func (c *Client) StartPod(ctx context.Context, podID string) error {
	return c.podAction(ctx, podID, "start")
}

// StopPod stops a running pod without deleting it (POST /pods/{podId}/stop).
// The pod keeps its volume and keeps billing storage while stopped.
func (c *Client) StopPod(ctx context.Context, podID string) error {
	return c.podAction(ctx, podID, "stop")
}

// podAction POSTs /pods/{podId}/{action} ("start" or "stop") with no body.
func (c *Client) podAction(ctx context.Context, podID, action string) error {
	if err := validateResourceID(podID); err != nil {
		return err
	}
	req, err := c.NewRequest(ctx, http.MethodPost, podsPathPrefix+podID+"/"+action, nil)
	if err != nil {
		return errors.Wrap(err, errCreateRequest)
	}
	resp, err := c.Do(req)
	if err != nil {
		return errors.Wrap(err, errDoRequest)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errors.Errorf("RunPod POST %s returned status %d: %s", podsPathPrefix+podID+"/"+action, resp.StatusCode, readErrorBody(resp.Body))
	}
	return nil
}

// readErrorBody reads an error response body, capped at 16KiB: the body is
// untrusted (comes from the remote API), so the read is bounded regardless
// of how large a response the server sends.
func readErrorBody(body io.Reader) string {
	if body == nil {
		return "<empty>"
	}

	payload, err := io.ReadAll(io.LimitReader(body, 16*1024))
	if err != nil {
		return "<unreadable>"
	}

	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return "<empty>"
	}

	return trimmed
}
