package clients

import (
	"context"
	"net/http"
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
	var out ContainerRegistryAuthResponse
	found, err := c.getStrict(ctx, containerRegistryAuthPathPrefix+id, &out)
	if err != nil || !found {
		return nil, false, err
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
