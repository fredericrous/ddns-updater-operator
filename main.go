package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
	"github.com/fredericrous/homelab/ddns-updater-operator/controllers"
	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/config"
	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/ipv6"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(connectivityv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          = flag.String("metrics-bind-address", ":8080", "The address the metric endpoint binds to")
		probeAddr            = flag.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to")
		enableLeaderElection = flag.Bool("leader-elect", false, "Enable leader election for controller manager")
		leaderElectionID     = flag.String("leader-election-id", "ddns-updater-operator", "Leader election ID")

		maxConcurrentReconciles = flag.Int("max-concurrent-reconciles", 3, "Maximum number of concurrent reconciles")
		reconcileTimeout        = flag.Duration("reconcile-timeout", 5*time.Minute, "Timeout for each reconcile operation")

		ddnsNamespace     = flag.String("ddns-namespace", "ddns-updater", "Namespace where ddns-updater is deployed")
		ddnsConfigMapName = flag.String("ddns-configmap", "ddns-updater-config", "Name of the ddns-updater ConfigMap")

		logLevel   = flag.String("zap-log-level", "info", "Zap log level (debug, info, warn, error)")
		logDevel   = flag.Bool("zap-devel", false, "Enable development mode logging")
		logEncoder = flag.String("zap-encoder", "json", "Zap log encoding (json or console)")

		ipv6Enabled            = flag.Bool("ipv6-enabled", false, "Enable IPv6 auto-detection")
		ipv6ConfigMapName      = flag.String("ipv6-configmap-name", "cluster-config", "Target ConfigMap name for IPv6 data")
		ipv6ConfigMapNamespace = flag.String("ipv6-configmap-namespace", "flux-system", "Target ConfigMap namespace")
		ipv6SyncInterval       = flag.Duration("ipv6-sync-interval", 5*time.Minute, "IPv6 check interval")
		ipv6Resolvers          = flag.String("ipv6-resolvers", "1.1.1.1:53,9.9.9.9:53", "Comma-separated external DNS resolvers")
		ipv6Overrides          = flag.String("ipv6-overrides", "", `JSON map of overrides: {"KEY":{"domain":"...","suffix":"..."}}`)
	)

	flag.Parse()

	// Setup logging
	opts := zap.Options{
		Development: *logDevel,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}

	switch *logLevel {
	case "debug":
		opts.Level = zapcore.DebugLevel
	case "info":
		opts.Level = zapcore.InfoLevel
	case "warn":
		opts.Level = zapcore.WarnLevel
	case "error":
		opts.Level = zapcore.ErrorLevel
	default:
		opts.Level = zapcore.InfoLevel
	}

	if *logEncoder == "console" {
		opts.Encoder = zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		})
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Parse IPv6 overrides from JSON
	var parsedIPv6Overrides map[string]config.IPv6Override
	if *ipv6Overrides != "" {
		if err := json.Unmarshal([]byte(*ipv6Overrides), &parsedIPv6Overrides); err != nil {
			setupLog.Error(err, "Failed to parse ipv6-overrides JSON")
			os.Exit(1)
		}
	}

	// Parse resolvers
	var resolverList []string
	for _, r := range strings.Split(*ipv6Resolvers, ",") {
		if trimmed := strings.TrimSpace(r); trimmed != "" {
			resolverList = append(resolverList, trimmed)
		}
	}

	// Create configuration
	cfg := &config.OperatorConfig{
		MetricsAddr:             *metricsAddr,
		ProbeAddr:               *probeAddr,
		EnableLeaderElection:    *enableLeaderElection,
		LeaderElectionID:        *leaderElectionID,
		MaxConcurrentReconciles: *maxConcurrentReconciles,
		ReconcileTimeout:        *reconcileTimeout,
		DDNSNamespace:           *ddnsNamespace,
		DDNSConfigMapName:       *ddnsConfigMapName,
		IPv6Enabled:             *ipv6Enabled,
		IPv6ConfigMapName:       *ipv6ConfigMapName,
		IPv6ConfigMapNamespace:  *ipv6ConfigMapNamespace,
		IPv6SyncInterval:        *ipv6SyncInterval,
		IPv6Resolvers:           resolverList,
		IPv6Overrides:           parsedIPv6Overrides,
	}

	if err := cfg.Validate(); err != nil {
		setupLog.Error(err, "Invalid configuration")
		os.Exit(1)
	}

	setupLog.Info("Starting ddns-updater-operator",
		"ddnsNamespace", cfg.DDNSNamespace,
		"metricsAddr", cfg.MetricsAddr,
		"probeAddr", cfg.ProbeAddr,
		"enableLeaderElection", cfg.EnableLeaderElection,
		"maxConcurrentReconciles", cfg.MaxConcurrentReconciles,
	)

	// Create manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: cfg.MetricsAddr},
		HealthProbeBindAddress: cfg.ProbeAddr,
		LeaderElection:         cfg.EnableLeaderElection,
		LeaderElectionID:       cfg.LeaderElectionID,
	})
	if err != nil {
		setupLog.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// Create the recorder for events
	recorder := mgr.GetEventRecorderFor("ddns-updater-operator")

	// Setup controller
	reconciler := &controllers.DDNSRecordReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("DDNSRecord"),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
		Config:   cfg,
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to setup controller")
		os.Exit(1)
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", func(req *http.Request) error {
		return nil
	}); err != nil {
		setupLog.Error(err, "Failed to add health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", func(req *http.Request) error {
		return nil
	}); err != nil {
		setupLog.Error(err, "Failed to add readiness check")
		os.Exit(1)
	}

	// Register IPv6 syncer as a manager.Runnable (starts after cache sync)
	if cfg.IPv6Enabled {
		setupLog.Info("IPv6 auto-detection enabled",
			"configMap", cfg.IPv6ConfigMapNamespace+"/"+cfg.IPv6ConfigMapName,
			"syncInterval", cfg.IPv6SyncInterval,
			"resolvers", cfg.IPv6Resolvers,
		)
		syncer := ipv6.NewSyncer(mgr.GetClient(), cfg, ctrl.Log)
		if err := mgr.Add(syncer); err != nil {
			setupLog.Error(err, "Failed to add IPv6 syncer")
			os.Exit(1)
		}
	}

	// Start the manager
	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
