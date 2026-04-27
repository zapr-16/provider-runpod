package main

import (
	"os"

	"github.com/go-logr/zapr"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	podcontroller "github.com/zapr-16/provider-runpod/internal/controller/pod"
	providerconfigcontroller "github.com/zapr-16/provider-runpod/internal/controller/providerconfig"
)

const errCreateManager = "cannot create controller manager"

func main() {
	zapLogger, err := zap.NewProduction()
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	defer func() {
		_ = zapLogger.Sync()
	}()

	logger := zapr.NewLogger(zapLogger)
	ctrl.SetLogger(logger)

	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(v1beta1.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: s,
		Logger: logger,
	})
	if err != nil {
		logger.Error(errors.Wrap(err, errCreateManager), "manager setup failed")
		os.Exit(1)
	}

	if err := providerconfigcontroller.SetupWithManager(mgr, zapLogger.Named("providerconfig")); err != nil {
		logger.Error(err, "cannot set up ProviderConfig controller")
		os.Exit(1)
	}

	if err := podcontroller.Setup(mgr, logger.WithName("pod")); err != nil {
		logger.Error(err, "cannot set up Pod controller")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
