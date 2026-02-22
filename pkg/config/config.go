package config

import (
	"fmt"
	"time"
)

// OperatorConfig holds the operator configuration
type OperatorConfig struct {
	// MetricsAddr is the address for the metrics endpoint
	MetricsAddr string

	// ProbeAddr is the address for health probes
	ProbeAddr string

	// EnableLeaderElection enables leader election
	EnableLeaderElection bool

	// LeaderElectionID is the ID for leader election
	LeaderElectionID string

	// MaxConcurrentReconciles is the maximum number of concurrent reconciles
	MaxConcurrentReconciles int

	// ReconcileTimeout is the timeout for reconcile operations
	ReconcileTimeout time.Duration

	// DDNSNamespace is the namespace where ddns-updater is deployed
	DDNSNamespace string

	// DDNSConfigMapName is the name of the ddns-updater ConfigMap
	DDNSConfigMapName string

	// IPv6Enabled enables IPv6 auto-detection and ConfigMap maintenance
	IPv6Enabled bool

	// IPv6ConfigMapName is the target ConfigMap name for IPv6 data
	IPv6ConfigMapName string

	// IPv6ConfigMapNamespace is the namespace of the IPv6 ConfigMap
	IPv6ConfigMapNamespace string

	// IPv6SyncInterval is how often to check for IPv6 prefix changes
	IPv6SyncInterval time.Duration

	// IPv6Resolvers are external DNS resolvers to bypass CoreDNS split-horizon
	IPv6Resolvers []string

	// IPv6Overrides are manual overrides keyed by ConfigMap key name
	IPv6Overrides map[string]IPv6Override
}

// IPv6Override allows manual specification of a server's domain and suffix
type IPv6Override struct {
	Domain string `json:"domain"`
	Suffix string `json:"suffix"`
}

// NewDefaultConfig creates a default configuration
func NewDefaultConfig() *OperatorConfig {
	return &OperatorConfig{
		MetricsAddr:             ":8080",
		ProbeAddr:               ":8081",
		EnableLeaderElection:    false,
		LeaderElectionID:        "ddns-updater-operator",
		MaxConcurrentReconciles: 3,
		ReconcileTimeout:        5 * time.Minute,
		DDNSNamespace:           "ddns-updater",
		DDNSConfigMapName:       "ddns-updater-config",
		IPv6ConfigMapName:       "cluster-config",
		IPv6ConfigMapNamespace:  "flux-system",
		IPv6SyncInterval:        5 * time.Minute,
		IPv6Resolvers:           []string{"1.1.1.1:53", "9.9.9.9:53"},
	}
}

// Validate validates the configuration
func (c *OperatorConfig) Validate() error {
	if c.MaxConcurrentReconciles < 1 {
		return fmt.Errorf("maxConcurrentReconciles must be at least 1")
	}
	if c.ReconcileTimeout < time.Second {
		return fmt.Errorf("reconcileTimeout must be at least 1 second")
	}
	if c.DDNSNamespace == "" {
		return fmt.Errorf("ddnsNamespace is required")
	}
	return nil
}
