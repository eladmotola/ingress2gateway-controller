package main

import (
	"flag"
	"fmt"
	"os"

	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/eladm/ingress2gateway-controller/pkg/config"
	"github.com/eladm/ingress2gateway-controller/pkg/controller"
	"github.com/eladm/ingress2gateway-controller/pkg/converter"
	_ "github.com/eladm/ingress2gateway-controller/pkg/metrics" // Initialize metrics
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// Version information (injected via ldflags)
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var defaultProvider string
	var defaultEmitter string
	ingressClassToGatewayClassMapping := make(mapFlag)
	var versionFlag bool

	flag.BoolVar(&versionFlag, "version", false, "Print version and exit")

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&defaultProvider, "source-provider", "", "The default source Ingress provider (e.g., nginx, istio). Can also be set via INGRESS_PROVIDER env var.")
	flag.StringVar(&defaultEmitter, "emitter", config.DefaultEmitterName, "The ingress2gateway emitter to use (e.g., standard). Can be overridden per-Ingress via the ingress2gateway.io/emitter annotation.")
	flag.Var(&ingressClassToGatewayClassMapping, "ingress-class-to-gateway-class-mapping", "An IngressClass=GatewayClass mapping. Can be specified multiple times.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if versionFlag {
		fmt.Printf("Version: %s\nCommit: %s\nBuildDate: %s\n", version, commit, date)
		os.Exit(0)
	}

	if defaultProvider == "" {
		defaultProvider = os.Getenv("INGRESS_PROVIDER")
	}

	if defaultProvider != "" && !config.IsProviderSupported(defaultProvider) {
		setupLog.Error(fmt.Errorf("unsupported default provider: %s", defaultProvider),
			"supported providers", "providers", config.GetSupportedProviders())
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "ingress2gateway-controller-leader",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create converter
	conv := converter.New(mgr.GetClient(), scheme)

	// Setup reconciler
	if err = (&controller.IngressReconciler{
		Client:                            mgr.GetClient(),
		Scheme:                            mgr.GetScheme(),
		Converter:                         conv,
		DefaultProvider:                   defaultProvider,
		DefaultEmitter:                    defaultEmitter,
		IngressClassToGatewayClassMapping: ingressClassToGatewayClassMapping,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Ingress")
		os.Exit(1)
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager",
		"version", version,
		"commit", commit,
		"date", date,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type mapFlag map[string]string

func (m mapFlag) String() string {
	return fmt.Sprintf("%v", map[string]string(m))
}

func (m mapFlag) Set(value string) error {
	kv := strings.SplitN(value, "=", 2)
	if len(kv) != 2 {
		return fmt.Errorf("invalid format, use key=value")
	}
	m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	return nil
}
