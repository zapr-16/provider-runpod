package clients

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pkg/errors"
)

const (
	networkVolumesPath       = "/networkvolumes"
	networkVolumesPathPrefix = networkVolumesPath + "/"
)

// CreateNetworkVolumeRequest mirrors POST /networkvolumes; all fields are
// required by the API.
type CreateNetworkVolumeRequest struct {
	Name         string `json:"name"`
	Size         int32  `json:"size"`
	DataCenterID string `json:"dataCenterId"`
}

// UpdateNetworkVolumeRequest mirrors PATCH /networkvolumes/{id}. RunPod only
// allows growing size; shrink attempts surface as API errors.
type UpdateNetworkVolumeRequest struct {
	Name *string `json:"name,omitempty"`
	Size *int32  `json:"size,omitempty"`
}

// NetworkVolumeResponse mirrors the network volume observation.
type NetworkVolumeResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Size         int32  `json:"size"`
	DataCenterID string `json:"dataCenterId"`
}

// CreateNetworkVolume creates a RunPod network volume and returns its ID.
func (c *Client) CreateNetworkVolume(ctx context.Context, payload CreateNetworkVolumeRequest) (string, error) {
	var out NetworkVolumeResponse
	if err := c.doJSON(ctx, http.MethodPost, networkVolumesPath, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// GetNetworkVolume retrieves a network volume; only 404 means "not found".
func (c *Client) GetNetworkVolume(ctx context.Context, id string) (*NetworkVolumeResponse, bool, error) {
	if err := validateResourceID(id); err != nil {
		return nil, false, err
	}
	req, err := c.NewRequest(ctx, http.MethodGet, networkVolumesPathPrefix+id, nil)
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
		return nil, false, errors.Errorf("RunPod GET /networkvolumes/%s returned status %d: %s", id, resp.StatusCode, readErrorBody(resp.Body))
	}
	var out NetworkVolumeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}
	return &out, true, nil
}

// UpdateNetworkVolume patches a network volume's name and/or size.
func (c *Client) UpdateNetworkVolume(ctx context.Context, id string, payload UpdateNetworkVolumeRequest) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPatch, networkVolumesPathPrefix+id, payload, nil)
}

// DeleteNetworkVolume deletes a network volume; 404/410 count as success.
func (c *Client) DeleteNetworkVolume(ctx context.Context, id string) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	return c.deleteStrict(ctx, networkVolumesPathPrefix+id)
}
