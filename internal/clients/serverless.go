package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/pkg/errors"
)

const (
	templatesPath       = "/templates"
	templatesPathPrefix = templatesPath + "/"
	endpointsPath       = "/endpoints"
	endpointsPathPrefix = endpointsPath + "/"
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
	if err := c.doJSON(ctx, http.MethodPost, templatesPath, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateTemplate patches a RunPod template.
func (c *Client) UpdateTemplate(ctx context.Context, templateID string, payload UpdateTemplateRequest) error {
	if err := validateResourceID(templateID); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPatch, templatesPathPrefix+templateID, payload, nil)
}

// DeleteTemplate deletes a RunPod template. Already-gone responses count as
// success; other failures are returned as errors.
func (c *Client) DeleteTemplate(ctx context.Context, templateID string) error {
	if err := validateResourceID(templateID); err != nil {
		return err
	}
	return c.deleteStrict(ctx, templatesPathPrefix+templateID)
}

// CreateEndpoint creates a RunPod serverless endpoint and returns its ID.
func (c *Client) CreateEndpoint(ctx context.Context, payload CreateEndpointRequest) (string, error) {
	var out EndpointResponse
	if err := c.doJSON(ctx, http.MethodPost, endpointsPath, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// GetTemplate retrieves a template from the RunPod API. Unlike the template
// embedded in the endpoint response, this includes imageName.
func (c *Client) GetTemplate(ctx context.Context, templateID string) (*TemplateResponse, bool, error) {
	if err := validateResourceID(templateID); err != nil {
		return nil, false, err
	}

	req, err := c.NewRequest(ctx, http.MethodGet, templatesPathPrefix+templateID, nil)
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false, errors.Errorf("RunPod GET /templates/%s returned status %d: %s", templateID, resp.StatusCode, readErrorBody(resp.Body))
	}

	var out TemplateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}

	return &out, true, nil
}

// GetEndpoint retrieves a serverless endpoint observation from the RunPod
// API. Only a 404 means "not found"; other non-2xx statuses are errors so a
// transient failure never triggers a duplicate Create.
func (c *Client) GetEndpoint(ctx context.Context, endpointID string) (*EndpointResponse, bool, error) {
	if err := validateResourceID(endpointID); err != nil {
		return nil, false, err
	}

	req, err := c.NewRequest(ctx, http.MethodGet, endpointsPathPrefix+endpointID+"?includeWorkers=true", nil)
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false, errors.Errorf("RunPod GET /endpoints/%s returned status %d: %s", endpointID, resp.StatusCode, readErrorBody(resp.Body))
	}

	var out EndpointResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}

	return &out, true, nil
}

// UpdateEndpoint patches mutable fields of a RunPod serverless endpoint.
func (c *Client) UpdateEndpoint(ctx context.Context, endpointID string, payload UpdateEndpointRequest) error {
	if err := validateResourceID(endpointID); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPatch, endpointsPathPrefix+endpointID, payload, nil)
}

// DeleteEndpoint deletes a RunPod serverless endpoint. Already-gone responses
// count as success; other failures are returned as errors so deletion retries
// instead of dropping the finalizer while the endpoint keeps billing.
func (c *Client) DeleteEndpoint(ctx context.Context, endpointID string) error {
	if err := validateResourceID(endpointID); err != nil {
		return err
	}
	return c.deleteStrict(ctx, endpointsPathPrefix+endpointID)
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

// deleteStrict issues a DELETE, treating only already-gone statuses
// (404/410) as success; everything else non-2xx is an error, matching the
// pod delete semantics.
func (c *Client) deleteStrict(ctx context.Context, path string) error {
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

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errors.Errorf("RunPod DELETE %s returned status %d: %s", path, resp.StatusCode, readErrorBody(resp.Body))
	}

	return nil
}
