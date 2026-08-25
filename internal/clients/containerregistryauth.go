package clients

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pkg/errors"
)

const (
	containerRegistryAuthPath       = "/containerregistryauth"
	containerRegistryAuthPathPrefix = containerRegistryAuthPath + "/"
)

// CreateContainerRegistryAuthRequest mirrors POST /containerregistryauth;
// all fields are required by the API.
type CreateContainerRegistryAuthRequest struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ContainerRegistryAuthResponse mirrors the container registry auth
// observation. The API never returns credentials.
type ContainerRegistryAuthResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateContainerRegistryAuth creates a RunPod container registry
// credential and returns its ID.
func (c *Client) CreateContainerRegistryAuth(ctx context.Context, payload CreateContainerRegistryAuthRequest) (string, error) {
	var out ContainerRegistryAuthResponse
	if err := c.doJSON(ctx, http.MethodPost, containerRegistryAuthPath, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// GetContainerRegistryAuth retrieves a container registry auth; only 404
// means "not found".
func (c *Client) GetContainerRegistryAuth(ctx context.Context, id string) (*ContainerRegistryAuthResponse, bool, error) {
	if err := validateResourceID(id); err != nil {
		return nil, false, err
	}
	req, err := c.NewRequest(ctx, http.MethodGet, containerRegistryAuthPathPrefix+id, nil)
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
		return nil, false, errors.Errorf("RunPod GET /containerregistryauth/%s returned status %d: %s", id, resp.StatusCode, readErrorBody(resp.Body))
	}
	var out ContainerRegistryAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}
	return &out, true, nil
}

// DeleteContainerRegistryAuth deletes a container registry auth; 404/410
// count as success.
func (c *Client) DeleteContainerRegistryAuth(ctx context.Context, id string) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	return c.deleteStrict(ctx, containerRegistryAuthPathPrefix+id)
}
