package clients

import (
	"bytes"
	"context"
	"encoding/json"
	stdlog "log"
	"net/http"

	"github.com/pkg/errors"
)

// CreateTemplateRequest mirrors the RunPod template create payload used for
// the implicit template backing a serverless Endpoint.
type CreateTemplateRequest struct {
	Name              string            `json:"name"`
	ImageName         string            `json:"imageName"`
	IsServerless      bool              `json:"isServerless"`
	Env               map[string]string `json:"env,omitempty"`
	ContainerDiskInGb *int32            `json:"containerDiskInGb,omitempty"`
}

// UpdateTemplateRequest mirrors the RunPod template PATCH payload.
type UpdateTemplateRequest struct {
	ImageName         *string           `json:"imageName,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	ContainerDiskInGb *int32            `json:"containerDiskInGb,omitempty"`
}

// TemplateResponse mirrors the subset of the RunPod template response the provider uses.
type TemplateResponse struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	ImageName         string            `json:"imageName"`
	IsServerless      bool              `json:"isServerless"`
	Env               map[string]string `json:"env"`
	ContainerDiskInGb int32             `json:"containerDiskInGb"`
}

// CreateEndpointRequest mirrors the RunPod serverless endpoint create payload.
type CreateEndpointRequest struct {
	Name               *string  `json:"name,omitempty"`
	TemplateID         string   `json:"templateId"`
	GPUTypeIDs         []string `json:"gpuTypeIds,omitempty"`
	GPUCount           *int32   `json:"gpuCount,omitempty"`
	WorkersMin         *int32   `json:"workersMin,omitempty"`
	WorkersMax         *int32   `json:"workersMax,omitempty"`
	IdleTimeout        *int32   `json:"idleTimeout,omitempty"`
	FlashBoot          *bool    `json:"flashboot,omitempty"`
	ScalerType         *string  `json:"scalerType,omitempty"`
	ScalerValue        *int32   `json:"scalerValue,omitempty"`
	NetworkVolumeID    *string  `json:"networkVolumeId,omitempty"`
	DataCenterIDs      []string `json:"dataCenterIds,omitempty"`
	ExecutionTimeoutMs *int32   `json:"executionTimeoutMs,omitempty"`
}

// UpdateEndpointRequest mirrors the RunPod serverless endpoint PATCH payload.
type UpdateEndpointRequest struct {
	GPUTypeIDs         []string `json:"gpuTypeIds,omitempty"`
	GPUCount           *int32   `json:"gpuCount,omitempty"`
	WorkersMin         *int32   `json:"workersMin,omitempty"`
	WorkersMax         *int32   `json:"workersMax,omitempty"`
	IdleTimeout        *int32   `json:"idleTimeout,omitempty"`
	FlashBoot          *bool    `json:"flashboot,omitempty"`
	ScalerType         *string  `json:"scalerType,omitempty"`
	ScalerValue        *int32   `json:"scalerValue,omitempty"`
	NetworkVolumeID    *string  `json:"networkVolumeId,omitempty"`
	DataCenterIDs      []string `json:"dataCenterIds,omitempty"`
	ExecutionTimeoutMs *int32   `json:"executionTimeoutMs,omitempty"`
}

// EndpointWorker is a worker entry in the endpoint observation; RunPod
// reports workers as pod objects.
type EndpointWorker struct {
	ID            string `json:"id"`
	DesiredStatus string `json:"desiredStatus"`
}

// EndpointResponse mirrors the subset of the RunPod endpoint GET response
// needed by Observe(). Workers require ?includeWorkers=true. The embedded
// template (?includeTemplate=true) omits imageName, so the provider reads
// the template through GET /templates/{id} instead. dataCenterIds is
// accepted on create/update but never echoed back.
type EndpointResponse struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	TemplateID         string           `json:"templateId"`
	GPUTypeIDs         []string         `json:"gpuTypeIds"`
	GPUCount           int32            `json:"gpuCount"`
	WorkersMin         int32            `json:"workersMin"`
	WorkersMax         int32            `json:"workersMax"`
	IdleTimeout        int32            `json:"idleTimeout"`
	FlashBoot          bool             `json:"flashboot"`
	ScalerType         string           `json:"scalerType"`
	ScalerValue        int32            `json:"scalerValue"`
	NetworkVolumeID    string           `json:"networkVolumeId"`
	ExecutionTimeoutMs int32            `json:"executionTimeoutMs"`
	Workers            []EndpointWorker `json:"workers"`
}

// CreateTemplate creates a RunPod template and returns its ID.
func (c *Client) CreateTemplate(ctx context.Context, payload CreateTemplateRequest) (string, error) {
	var out TemplateResponse
	if err := c.doJSON(ctx, http.MethodPost, "/templates", payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateTemplate patches a RunPod template.
func (c *Client) UpdateTemplate(ctx context.Context, templateID string, payload UpdateTemplateRequest) error {
	return c.doJSON(ctx, http.MethodPatch, "/templates/"+templateID, payload, nil)
}

// DeleteTemplate deletes a RunPod template, tolerating already-gone semantics.
func (c *Client) DeleteTemplate(ctx context.Context, templateID string) error {
	return c.deleteTolerant(ctx, "/templates/"+templateID)
}

// CreateEndpoint creates a RunPod serverless endpoint and returns its ID.
func (c *Client) CreateEndpoint(ctx context.Context, payload CreateEndpointRequest) (string, error) {
	var out EndpointResponse
	if err := c.doJSON(ctx, http.MethodPost, "/endpoints", payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// GetTemplate retrieves a template from the RunPod API. Unlike the template
// embedded in the endpoint response, this includes imageName.
func (c *Client) GetTemplate(ctx context.Context, templateID string) (*TemplateResponse, bool, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, "/templates/"+templateID, nil)
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
		stdlog.Printf("RunPod GET /templates/%s returned status %d; treating as not found; body=%s", templateID, resp.StatusCode, readErrorBody(resp.Body))
		return nil, false, nil
	}

	var out TemplateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}

	return &out, true, nil
}

// GetEndpoint retrieves a serverless endpoint observation from the RunPod API.
func (c *Client) GetEndpoint(ctx context.Context, endpointID string) (*EndpointResponse, bool, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, "/endpoints/"+endpointID+"?includeWorkers=true", nil)
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
		stdlog.Printf("RunPod GET /endpoints/%s returned status %d; treating as not found; body=%s", endpointID, resp.StatusCode, readErrorBody(resp.Body))
		return nil, false, nil
	}

	var out EndpointResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}

	return &out, true, nil
}

// UpdateEndpoint patches mutable fields of a RunPod serverless endpoint.
func (c *Client) UpdateEndpoint(ctx context.Context, endpointID string, payload UpdateEndpointRequest) error {
	return c.doJSON(ctx, http.MethodPatch, "/endpoints/"+endpointID, payload, nil)
}

// DeleteEndpoint deletes a RunPod serverless endpoint, tolerating already-gone semantics.
func (c *Client) DeleteEndpoint(ctx context.Context, endpointID string) error {
	return c.deleteTolerant(ctx, "/endpoints/"+endpointID)
}

// doJSON executes a JSON request against the RunPod API and decodes the
// response into out when out is non-nil. Non-2xx statuses are errors.
func (c *Client) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, errCreateRequest)
	}

	req, err := c.NewRequest(ctx, method, path, bytes.NewReader(body))
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
		return errors.Errorf("RunPod %s %s returned status %d: %s", method, path, resp.StatusCode, readErrorBody(resp.Body))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return errors.Wrap(err, errDecodeResponse)
	}
	return nil
}

// deleteTolerant issues a DELETE and treats non-2xx statuses as success,
// matching the pod delete semantics.
func (c *Client) deleteTolerant(ctx context.Context, path string) error {
	req, err := c.NewRequest(ctx, http.MethodDelete, path, nil)
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
		stdlog.Printf("RunPod DELETE %s returned status %d; treating as success; body=%s", path, resp.StatusCode, readErrorBody(resp.Body))
	}

	return nil
}
