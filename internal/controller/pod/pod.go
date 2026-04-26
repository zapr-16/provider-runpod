package pod

import (
	"context"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	xpresource "github.com/crossplane/crossplane-runtime/pkg/resource"
	managed "github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	errNotPod                = "managed resource is not a Pod"
	errMissingProviderConfig = "pod is missing providerConfigRef"
	errGetProviderConfig     = "cannot get ProviderConfig"
	errCreateClient          = "cannot create RunPod client from ProviderConfig"
)

type connector struct {
	kube client.Client
	log  logr.Logger
}

func (c *connector) Connect(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return nil, errors.New(errNotPod)
	}

	ref := pod.GetProviderConfigReference()
	if ref == nil || ref.Name == "" {
		return nil, errors.New(errMissingProviderConfig)
	}

	pc := &v1beta1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetProviderConfig)
	}

	rc, err := runpodclient.ClientFromProviderConfig(ctx, c.kube, pc)
	if err != nil {
		return nil, errors.Wrap(err, errCreateClient)
	}

	return &external{
		client: rc,
		log:    c.log.WithValues("pod", pod.GetName()),
	}, nil
}

func Setup(mgr ctrl.Manager, log logr.Logger) error {
	name := xpresource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind("Pod"))
	r := managed.NewReconciler(
		mgr,
		name,
		managed.WithExternalConnecter(&connector{
			kube: mgr.GetClient(),
			log:  log,
		}),
		managed.WithLogger(logging.NewLogrLogger(log)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Pod{}).
		Complete(r)
}
