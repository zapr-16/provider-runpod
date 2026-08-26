package networkvolume

import (
	"context"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errGetNetworkVolume    = "cannot get network volume from RunPod API"
	errCreateNetworkVolume = "cannot create network volume via RunPod API"
	errUpdateNetworkVolume = "cannot update network volume via RunPod API"
	errDeleteNetworkVolume = "cannot delete network volume via RunPod API"
)

type external struct {
	client *runpodclient.Client
	log    logr.Logger
}

func (e *external) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotNetworkVolume)
	}
	externalName := meta.GetExternalName(nv)
	if externalName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	response, found, err := e.client.GetNetworkVolume(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetNetworkVolume)
	}
	if !found {
		e.log.Info("NetworkVolume not found in RunPod API", "external-name", externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	nv.Status.AtProvider = v1alpha1.NetworkVolumeObservation{
		NetworkVolumeID: response.ID,
		Name:            response.Name,
		Size:            response.Size,
		DataCenterID:    response.DataCenterID,
	}
	// The RunPod API exposes no intermediate provisioning state for network
	// volumes, so found == Available.
	nv.SetConditions(xpv2.Available())
	upToDate := response.Size == nv.Spec.ForProvider.Size &&
		(nv.Spec.ForProvider.Name == nil || *nv.Spec.ForProvider.Name == response.Name)
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
		ConnectionDetails: managed.ConnectionDetails{
			"networkVolumeId": []byte(response.ID),
		},
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotNetworkVolume)
	}
	name := nv.GetName()
	if nv.Spec.ForProvider.Name != nil {
		name = *nv.Spec.ForProvider.Name
	}
	id, err := e.client.CreateNetworkVolume(ctx, runpodclient.CreateNetworkVolumeRequest{
		Name:         name,
		Size:         nv.Spec.ForProvider.Size,
		DataCenterID: nv.Spec.ForProvider.DataCenterID,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateNetworkVolume)
	}
	meta.SetExternalName(nv, id)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{"networkVolumeId": []byte(id)},
	}, nil
}

func (e *external) Update(ctx context.Context, mg xpresource.Managed) (managed.ExternalUpdate, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotNetworkVolume)
	}
	externalName := meta.GetExternalName(nv)
	if externalName == "" {
		return managed.ExternalUpdate{}, nil
	}
	size := nv.Spec.ForProvider.Size
	payload := runpodclient.UpdateNetworkVolumeRequest{Size: &size, Name: nv.Spec.ForProvider.Name}
	if err := e.client.UpdateNetworkVolume(ctx, externalName, payload); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateNetworkVolume)
	}
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg xpresource.Managed) (managed.ExternalDelete, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotNetworkVolume)
	}
	externalName := meta.GetExternalName(nv)
	if externalName == "" {
		return managed.ExternalDelete{}, nil
	}
	return managed.ExternalDelete{}, errors.Wrap(e.client.DeleteNetworkVolume(ctx, externalName), errDeleteNetworkVolume)
}

func (e *external) Disconnect(_ context.Context) error { return nil }
