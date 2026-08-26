package containerregistryauth

import (
	"context"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errGetContainerRegistryAuth    = "cannot get container registry auth from RunPod API"
	errCreateContainerRegistryAuth = "cannot create container registry auth via RunPod API"
	errDeleteContainerRegistryAuth = "cannot delete container registry auth via RunPod API"
	errGetCredentialsSecret        = "cannot get credentials secret"
	errMissingSecretKeys           = "credentials secret is missing key %q or %q"
)

type external struct {
	client *runpodclient.Client
	kube   client.Client
	log    logr.Logger
}

func (e *external) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	cra, ok := mg.(*v1alpha1.ContainerRegistryAuth)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotContainerRegistryAuth)
	}
	externalName := meta.GetExternalName(cra)
	if externalName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	response, found, err := e.client.GetContainerRegistryAuth(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetContainerRegistryAuth)
	}
	if !found {
		e.log.Info("ContainerRegistryAuth not found in RunPod API", "external-name", externalName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	cra.Status.AtProvider = v1alpha1.ContainerRegistryAuthObservation{
		ContainerRegistryAuthID: response.ID,
		Name:                    response.Name,
	}
	// The RunPod API exposes no intermediate provisioning state for
	// container registry auths, so found == Available.
	cra.SetConditions(xpv2.Available())
	// The kind is immutable: RunPod has no update endpoint for registry
	// auths, so a found resource is always up to date.
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
		ConnectionDetails: managed.ConnectionDetails{
			"containerRegistryAuthId": []byte(response.ID),
		},
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	cra, ok := mg.(*v1alpha1.ContainerRegistryAuth)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotContainerRegistryAuth)
	}
	ref := cra.Spec.ForProvider.CredentialsSecretRef
	secret := &corev1.Secret{}
	if err := e.kube.Get(ctx, types.NamespacedName{Namespace: cra.GetNamespace(), Name: ref.Name}, secret); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errGetCredentialsSecret)
	}
	usernameKey := ref.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := ref.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}
	username, uok := secret.Data[usernameKey]
	password, pok := secret.Data[passwordKey]
	if !uok || !pok {
		return managed.ExternalCreation{}, errors.Errorf(errMissingSecretKeys, usernameKey, passwordKey)
	}
	name := cra.GetName()
	if cra.Spec.ForProvider.Name != nil {
		name = *cra.Spec.ForProvider.Name
	}
	id, err := e.client.CreateContainerRegistryAuth(ctx, runpodclient.CreateContainerRegistryAuthRequest{
		Name:     name,
		Username: string(username),
		Password: string(password),
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateContainerRegistryAuth)
	}
	meta.SetExternalName(cra, id)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{"containerRegistryAuthId": []byte(id)},
	}, nil
}

func (e *external) Update(_ context.Context, _ xpresource.Managed) (managed.ExternalUpdate, error) {
	// ContainerRegistryAuth is immutable in the RunPod API (no update
	// endpoint exists); Observe always reports up-to-date once found, so
	// this should never be called.
	e.log.V(1).Info("ContainerRegistryAuth is immutable; Update is a no-op")
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg xpresource.Managed) (managed.ExternalDelete, error) {
	cra, ok := mg.(*v1alpha1.ContainerRegistryAuth)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotContainerRegistryAuth)
	}
	externalName := meta.GetExternalName(cra)
	if externalName == "" {
		return managed.ExternalDelete{}, nil
	}
	return managed.ExternalDelete{}, errors.Wrap(e.client.DeleteContainerRegistryAuth(ctx, externalName), errDeleteContainerRegistryAuth)
}

func (e *external) Disconnect(_ context.Context) error { return nil }
