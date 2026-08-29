package main

import (
	"flag"
	"os"
	"time"

	xpcontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"
	"github.com/crossplane/crossplane-runtime/v2/pkg/gate"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/customresourcesgate"
	managed "github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/statemetrics"
	"github.com/go-logr/zapr"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
	v1beta1 "github.com/zapr-16/provider-runpod/apis/v1beta1"
	containerregistryauthcontroller "github.com/zapr-16/provider-runpod/internal/controller/containerregistryauth"
	endpointcontroller "github.com/zapr-16/provider-runpod/internal/controller/endpoint"
	networkvolumecontroller "github.com/zapr-16/provider-runpod/internal/controller/networkvolume"
	podcontroller "github.com/zapr-16/provider-runpod/internal/controller/pod"
	providerconfigcontroller "github.com/zapr-16/provider-runpod/internal/controller/providerconfig"
	templatecontroller "github.com/zapr-16/provider-runpod/internal/controller/template"
)

const errCreateManager = "cannot create controller manager"

func main() {
	var (
		debug                    = flag.Bool("debug", false, "Run with debug logging (development zap config, human-readable, more verbose).")
		pollInterval             = flag.Duration("poll-interval", time.Minute, "How often individual resources will be polled for drift from the desired state.")
		maxReconcileRate         = flag.Int("max-reconcile-rate", 10, "The global maximum rate, in reconciles per second, at which resources may be checked for drift from the desired state.")
		leaderElection           = flag.Bool("leader-election", true, "Use leader election for the controller manager.")
		syncInterval             = flag.Duration("sync-interval", time.Hour, "How often the manager's cache resyncs against the API server, independent of the poll interval above.")
		enableManagementPolicies = flag.Bool("enable-management-policies", true, "Enable support for Management Policies.")
	)
	flag.Parse()

	zapLogger, err := newZapLogger(*debug)
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

	// RunPod create APIs are not idempotent: two reconcilers racing during a
	// leadership handoff would provision duplicate GPU pods, and each pod
	// bills real GPU time until someone notices and deletes it. A cheap,
	// flappy handoff is far more expensive here than a slow one, so leader
	// election is tuned toward stability over fast failover: a long lease
	// with a renew deadline close to it means a healthy leader almost never
	// loses the lease to a transient hiccup, and the wider retry period
	// avoids a thundering herd of candidates re-acquiring it.
	leaseDuration := 60 * time.Second
	renewDeadline := 50 * time.Second
	retryPeriod := 15 * time.Second

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 s,
		Logger:                 logger,
		LeaderElection:         *leaderElection,
		LeaderElectionID:       "crossplane-leader-election-provider-runpod",
		LeaseDuration:          &leaseDuration,
		RenewDeadline:          &renewDeadline,
		RetryPeriod:            &retryPeriod,
		HealthProbeBindAddress: ":8081",
		Cache: cache.Options{
			SyncPeriod: syncInterval,
		},
	})
	if err != nil {
		logger.Error(errors.Wrap(err, errCreateManager), "manager setup failed")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "cannot add health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "cannot add readiness check")
		os.Exit(1)
	}

	features := &feature.Flags{}
	if *enableManagementPolicies {
		features.Enable(feature.EnableBetaManagementPolicies)
	}

	mrMetrics := managed.NewMRMetricRecorder()
	mrStateMetrics := statemetrics.NewMRStateMetrics()
	ctrlmetrics.Registry.MustRegister(mrMetrics, mrStateMetrics)

	// The CRD gate lets every controller below be registered before its
	// CustomResourceDefinition exists in the cluster: registration is
	// deferred until the gate observes that CRD as Established, instead of
	// the controller crash-looping (or repeatedly failing to list/watch)
	// against a kind that hasn't been installed yet.
	crdGate := &gate.Gate[schema.GroupVersionKind]{}

	o := xpcontroller.Options{
		Logger:                  logging.NewLogrLogger(logger),
		GlobalRateLimiter:       ratelimiter.NewGlobal(*maxReconcileRate),
		PollInterval:            *pollInterval,
		MaxConcurrentReconciles: 1,
		Features:                features,
		MetricOptions: &xpcontroller.MetricOptions{
			PollStateMetricInterval: *pollInterval,
			MRMetrics:               mrMetrics,
			MRStateMetrics:          mrStateMetrics,
		},
		Gate: crdGate,
	}

	if err := customresourcesgate.Setup(mgr, o); err != nil {
		logger.Error(err, "cannot set up CRD gate controller")
		os.Exit(1)
	}

	// ProviderConfig, ClusterProviderConfig, and ProviderConfigUsage are not
	// gated: unlike the managed-resource controllers below, reconciling them
	// before their CRD is Established makes no RunPod API calls and has no
	// billing side effect, so the only cost of leaving them ungated is
	// benign controller-runtime retry logging until the CRD appears.
	if err := providerconfigcontroller.SetupWithManager(mgr, zapLogger.Named("providerconfig")); err != nil {
		logger.Error(err, "cannot set up ProviderConfig controller")
		os.Exit(1)
	}

	if err := providerconfigcontroller.SetupClusterWithManager(mgr, zapLogger.Named("clusterproviderconfig")); err != nil {
		logger.Error(err, "cannot set up ClusterProviderConfig controller")
		os.Exit(1)
	}

	if err := providerconfigcontroller.SetupUsageTracking(mgr, logger.WithName("providerconfig-usage")); err != nil {
		logger.Error(err, "cannot set up ProviderConfig usage controller")
		os.Exit(1)
	}

	if err := podcontroller.Setup(mgr, logger.WithName("pod"), o); err != nil {
		logger.Error(err, "cannot set up Pod controller")
		os.Exit(1)
	}

	if err := endpointcontroller.Setup(mgr, logger.WithName("endpoint"), o); err != nil {
		logger.Error(err, "cannot set up Endpoint controller")
		os.Exit(1)
	}

	if err := networkvolumecontroller.Setup(mgr, logger.WithName("networkvolume"), o); err != nil {
		logger.Error(err, "cannot set up NetworkVolume controller")
		os.Exit(1)
	}

	if err := containerregistryauthcontroller.Setup(mgr, logger.WithName("containerregistryauth"), o); err != nil {
		logger.Error(err, "cannot set up ContainerRegistryAuth controller")
		os.Exit(1)
	}

	if err := templatecontroller.Setup(mgr, logger.WithName("template"), o); err != nil {
		logger.Error(err, "cannot set up Template controller")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "manager exited with error")
		os.Exit(1)
	}
}

// newZapLogger builds the zap.Logger used for both the manager's logr
// adapter and the ProviderConfig controllers' native zap logging. debug
// switches to zap's development config (human-readable console encoding,
// DebugLevel, stack traces on warnings) instead of the default production
// JSON config.
func newZapLogger(debug bool) (*zap.Logger, error) {
	if debug {
		cfg := zap.NewDevelopmentConfig()
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
		return cfg.Build()
	}
	return zap.NewProduction()
}
