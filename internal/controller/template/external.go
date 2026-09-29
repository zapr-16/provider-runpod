package template

import (
	"context"
	"maps"
	"slices"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
	"github.com/zapr-16/provider-runpod/internal/controller/fieldcmp"
)

const (
	errGetTemplate    = "cannot get template from RunPod API"
	errCreateTemplate = "cannot create template via RunPod API"
	errUpdateTemplate = "cannot update template via RunPod API"
	errDeleteTemplate = "cannot delete template via RunPod API"
)

type external struct {
	client *runpodclient.Client
	log    logr.Logger
}

func (e *external) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	tmpl, ok := mg.(*v1alpha1.Template)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotTemplate)
	}
	externalName := meta.GetExternalName(tmpl)
	if externalName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	response, found, err := e.client.GetTemplate(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetTemplate)
	}
	if !found {
		e.log.Info("Template not found in RunPod API", "external-name", externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	tmpl.Status.AtProvider = v1alpha1.TemplateObservation{
		TemplateID: response.ID,
		Name:       response.Name,
	}
	// The RunPod API exposes no intermediate provisioning state for
	// templates, so found == Available.
	tmpl.SetConditions(xpv2.Available())
	upToDate := !hasStandaloneTemplateDrift(tmpl.Spec.ForProvider, *response) &&
		(tmpl.Spec.ForProvider.Name == nil || *tmpl.Spec.ForProvider.Name == response.Name)
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
		ConnectionDetails: managed.ConnectionDetails{
			"templateId": []byte(response.ID),
		},
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	tmpl, ok := mg.(*v1alpha1.Template)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotTemplate)
	}
	spec := tmpl.Spec.ForProvider
	name := tmpl.GetName()
	if spec.Name != nil {
		name = *spec.Name
	}
	isServerless := false
	if spec.IsServerless != nil {
		isServerless = *spec.IsServerless
	}
	id, err := e.client.CreateTemplate(ctx, runpodclient.CreateTemplateRequest{
		Name:                    name,
		ImageName:               spec.ImageName,
		IsServerless:            isServerless,
		Env:                     fieldcmp.BuildEnvMap(spec.Env),
		ContainerDiskInGb:       spec.ContainerDiskInGb,
		DockerStartCmd:          spec.DockerStartCmd,
		DockerEntrypoint:        spec.DockerEntrypoint,
		ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
		Ports:                   fieldcmp.BuildPortTokens(spec.Ports),
		VolumeInGb:              spec.VolumeInGb,
		VolumeMountPath:         spec.VolumeMountPath,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateTemplate)
	}
	meta.SetExternalName(tmpl, id)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{"templateId": []byte(id)},
	}, nil
}

func (e *external) Update(ctx context.Context, mg xpresource.Managed) (managed.ExternalUpdate, error) {
	tmpl, ok := mg.(*v1alpha1.Template)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotTemplate)
	}
	externalName := meta.GetExternalName(tmpl)
	if externalName == "" {
		return managed.ExternalUpdate{}, nil
	}
	spec := tmpl.Spec.ForProvider
	imageName := spec.ImageName
	payload := runpodclient.UpdateTemplateRequest{
		Name:                    spec.Name,
		ImageName:               &imageName,
		Env:                     fieldcmp.BuildEnvMap(spec.Env),
		ContainerDiskInGb:       spec.ContainerDiskInGb,
		DockerStartCmd:          spec.DockerStartCmd,
		DockerEntrypoint:        spec.DockerEntrypoint,
		ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
		Ports:                   fieldcmp.BuildPortTokens(spec.Ports),
		VolumeInGb:              spec.VolumeInGb,
		VolumeMountPath:         spec.VolumeMountPath,
	}
	if err := e.client.UpdateTemplate(ctx, externalName, payload); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateTemplate)
	}
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg xpresource.Managed) (managed.ExternalDelete, error) {
	tmpl, ok := mg.(*v1alpha1.Template)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotTemplate)
	}
	externalName := meta.GetExternalName(tmpl)
	if externalName == "" {
		return managed.ExternalDelete{}, nil
	}
	return managed.ExternalDelete{}, errors.Wrap(e.client.DeleteTemplate(ctx, externalName), errDeleteTemplate)
}

func (e *external) Disconnect(_ context.Context) error { return nil }

// hasStandaloneTemplateDrift compares spec-set fields against the template
// observation; nil/empty spec fields never drift.
func hasStandaloneTemplateDrift(spec v1alpha1.TemplateParameters, r runpodclient.TemplateResponse) bool {
	if spec.ImageName != r.ImageName {
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
	if spec.ContainerRegistryAuthID != nil && *spec.ContainerRegistryAuthID != r.ContainerRegistryAuthID {
		return true
	}
	if len(spec.DockerStartCmd) > 0 && !slices.Equal(spec.DockerStartCmd, r.DockerStartCmd) {
		return true
	}
	if len(spec.DockerEntrypoint) > 0 && !slices.Equal(spec.DockerEntrypoint, r.DockerEntrypoint) {
		return true
	}
	if len(spec.Env) > 0 && !maps.Equal(fieldcmp.BuildEnvMap(spec.Env), r.Env) {
		return true
	}
	if len(spec.Ports) > 0 && !fieldcmp.PortTokensEqual(fieldcmp.BuildPortTokens(spec.Ports), r.Ports) {
		return true
	}
	return false
}
