package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	stdlog "log"
	"net/http"
	"strings"
	"time"

	xpresource "github.com/crossplane/crossplane-runtime/pkg/resource"
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
)

// Client wraps an HTTP client configured for the RunPod REST API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// CreatePodRequest mirrors the RunPod pod create payload used by the provider.
type CreatePodRequest struct {
	ImageName         *string           `json:"imageName,omitempty"`
	GPUTypeIDs        []string          `json:"gpuTypeIds,omitempty"`
	GPUCount          *int32            `json:"gpuCount,omitempty"`
	CloudType         *string           `json:"cloudType,omitempty"`
	SupportPublicIP   *bool             `json:"supportPublicIp,omitempty"`
	ContainerDiskInGb *int32            `json:"containerDiskInGb,omitempty"`
	VolumeInGb        *int32            `json:"volumeInGb,omitempty"`
	VolumeMountPath   *string           `json:"volumeMountPath,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	Ports             []string          `json:"ports,omitempty"`
	DockerStartCmd    []string          `json:"dockerStartCmd,omitempty"`
}

// PodResponse mirrors the subset of the RunPod Pod GET response needed by Observe().
type PodResponse struct {
	ID            string            `json:"id"`
	DesiredStatus string            `json:"desiredStatus"`
	PublicIP      string            `json:"publicIp"`
	PortMappings  map[string]int32  `json:"portMappings"`
	CostPerHr     float64           `json:"costPerHr"`
	LastStartedAt string            `json:"lastStartedAt"`
	Env           map[string]string `json:"env"`
	Ports         []string          `json:"ports"`
	GPU           struct {
		DisplayName string `json:"displayName"`
	} `json:"gpu"`
	Machine struct {
		GPUDisplayName string `json:"gpuDisplayName"`
	} `json:"machine"`
}

// NewClient returns a RunPod client with the default REST base URL.
func NewClient(apiKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		apiKey:     apiKey,
	}
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

// ClientFromProviderConfig builds an authenticated RunPod client from a ProviderConfig.
func ClientFromProviderConfig(ctx context.Context, kube client.Client, pc *v1beta1.ProviderConfig) (*Client, error) {
	apiKey, err := xpresource.ExtractSecret(ctx, kube, pc.Spec.Credentials)
	if err != nil {
		return nil, errors.Wrap(err, errExtractCredentials)
	}

	if strings.TrimSpace(string(apiKey)) == "" {
		return nil, errors.New(errEmptyCredentials)
	}

	return NewClient(string(apiKey)), nil
}

// GetPod retrieves a pod observation payload from the RunPod API.
func (c *Client) GetPod(ctx context.Context, podID string) (*PodResponse, bool, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, "/pods/"+podID+"?includeMachine=true", nil)
	if err != nil {
		return nil, false, errors.Wrap(err, errCreateRequest)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, false, errors.Wrap(err, errDoRequest)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		stdlog.Printf("RunPod GET /pods/%s returned status %d; treating as not found; body=%s", podID, resp.StatusCode, readErrorBody(resp.Body))
		return nil, false, nil
	}

	var out PodResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}

	return &out, true, nil
}

// CreatePod creates a new RunPod pod and returns its pod ID.
func (c *Client) CreatePod(ctx context.Context, payload CreatePodRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", errors.Wrap(err, errCreateRequest)
	}

	req, err := c.NewRequest(ctx, http.MethodPost, "/pods", bytes.NewReader(body))
	if err != nil {
		return "", errors.Wrap(err, errCreateRequest)
	}

	resp, err := c.Do(req)
	if err != nil {
		return "", errors.Wrap(err, errDoRequest)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", errors.Errorf("RunPod POST /pods returned status %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	var out PodResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", errors.Wrap(err, errDecodeResponse)
	}

	return out.ID, nil
}

// DeletePod deletes a RunPod pod and tolerates undocumented already-gone semantics.
func (c *Client) DeletePod(ctx context.Context, podID string) error {
	req, err := c.NewRequest(ctx, http.MethodDelete, "/pods/"+podID, nil)
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
		stdlog.Printf("RunPod DELETE /pods/%s returned status %d; treating as success; body=%s", podID, resp.StatusCode, readErrorBody(resp.Body))
	}

	return nil
}

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
