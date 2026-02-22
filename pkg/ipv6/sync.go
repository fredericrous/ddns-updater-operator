package ipv6

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/config"
)

const (
	// AnnotationIPv6ConfigMapKey is the annotation on DDNSRecords that marks them
	// as a reference for IPv6 auto-detection. The value is the ConfigMap key name.
	AnnotationIPv6ConfigMapKey = "ddns.homelab.io/ipv6-configmap-key"
)

// ipv6Server represents a discovered or overridden IPv6 server to resolve.
type ipv6Server struct {
	Key    string // ConfigMap key, e.g. "GATEWAY_IPV6"
	Domain string // FQDN to resolve, e.g. "home.daddyshome.fr"
	Suffix string // IPv6 suffix from DDNSRecord spec, e.g. "::166/64"
}

// Syncer implements manager.Runnable for IPv6 auto-detection.
// It periodically resolves AAAA records for annotated DDNSRecords and
// updates the cluster-config ConfigMap with the current IPv6 addresses.
type Syncer struct {
	client client.Client
	cfg    *config.OperatorConfig
	log    logr.Logger
}

// NewSyncer creates a new IPv6 Syncer. Register it with mgr.Add() so it
// starts after the informer cache is synced and is managed by the controller-runtime.
func NewSyncer(c client.Client, cfg *config.OperatorConfig, log logr.Logger) *Syncer {
	return &Syncer{
		client: c,
		cfg:    cfg,
		log:    log.WithName("ipv6-sync"),
	}
}

// Start implements manager.Runnable. It runs the IPv6 sync loop,
// blocking until ctx is cancelled.
func (s *Syncer) Start(ctx context.Context) error {
	s.log.Info("Starting IPv6 sync loop",
		"interval", s.cfg.IPv6SyncInterval,
		"configMap", fmt.Sprintf("%s/%s", s.cfg.IPv6ConfigMapNamespace, s.cfg.IPv6ConfigMapName),
		"resolvers", s.cfg.IPv6Resolvers,
	)

	// Run immediately, then on ticker
	s.syncOnce(ctx)

	ticker := time.NewTicker(s.cfg.IPv6SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("IPv6 sync loop stopped")
			return nil
		case <-ticker.C:
			s.syncOnce(ctx)
		}
	}
}

func (s *Syncer) syncOnce(ctx context.Context) {
	servers, err := s.discoverServers(ctx)
	if err != nil {
		s.log.Error(err, "Failed to discover IPv6 servers")
		return
	}

	if len(servers) == 0 {
		s.log.V(1).Info("No IPv6 servers discovered (no annotations or overrides)")
		return
	}

	resolver := buildResolver(s.cfg.IPv6Resolvers)
	data := make(map[string]string)

	for _, srv := range servers {
		ip, err := resolveAAAA(ctx, resolver, srv.Domain, s.log)
		if err != nil {
			s.log.Error(err, "Failed to resolve AAAA record", "domain", srv.Domain, "key", srv.Key)
			continue
		}

		// Extract /64 prefix from resolved address and apply suffix
		fullAddr, err := applyIPv6Suffix(ip, srv.Suffix)
		if err != nil {
			s.log.Error(err, "Failed to apply IPv6 suffix", "ip", ip, "suffix", srv.Suffix, "key", srv.Key)
			continue
		}

		data[srv.Key] = fullAddr
		data[srv.Key+"_SUFFIX"] = srv.Suffix
		s.log.V(1).Info("Resolved IPv6", "key", srv.Key, "address", fullAddr, "suffix", srv.Suffix)
	}

	if len(data) == 0 {
		s.log.Info("No IPv6 addresses resolved, skipping ConfigMap update")
		return
	}

	changed, err := s.updateConfigMap(ctx, data)
	if err != nil {
		s.log.Error(err, "Failed to update cluster-config ConfigMap")
		return
	}

	if changed {
		s.log.Info("Updated cluster-config ConfigMap with new IPv6 data", "keys", mapKeys(data))
		if err := s.annotateFluxKustomizations(ctx); err != nil {
			s.log.Error(err, "Failed to annotate Flux Kustomizations for reconciliation")
		}
	}
}

// discoverServers merges Helm overrides with annotation-discovered servers.
// Overrides take precedence over annotations.
func (s *Syncer) discoverServers(ctx context.Context) ([]ipv6Server, error) {
	seen := make(map[string]bool)
	var servers []ipv6Server

	// 1. Add Helm overrides first (they win)
	for key, override := range s.cfg.IPv6Overrides {
		servers = append(servers, ipv6Server{
			Key:    key,
			Domain: override.Domain,
			Suffix: override.Suffix,
		})
		seen[key] = true
		s.log.V(1).Info("Added IPv6 override", "key", key, "domain", override.Domain)
	}

	// 2. Discover from annotated DDNSRecords
	recordList := &connectivityv1alpha1.DDNSRecordList{}
	if err := s.client.List(ctx, recordList); err != nil {
		return nil, fmt.Errorf("failed to list DDNSRecords: %w", err)
	}

	for _, record := range recordList.Items {
		key, ok := record.Annotations[AnnotationIPv6ConfigMapKey]
		if !ok || key == "" {
			continue
		}
		if seen[key] {
			s.log.V(1).Info("Skipping annotated DDNSRecord (overridden)", "key", key, "record", record.Name)
			continue
		}

		fqdn := fmt.Sprintf("%s.%s", record.Spec.Host, record.Spec.Domain)
		if record.Spec.Host == "@" {
			fqdn = record.Spec.Domain
		}

		servers = append(servers, ipv6Server{
			Key:    key,
			Domain: fqdn,
			Suffix: record.Spec.IPv6Suffix,
		})
		seen[key] = true
		s.log.V(1).Info("Discovered IPv6 server from DDNSRecord annotation",
			"key", key, "domain", fqdn, "record", record.Name)
	}

	return servers, nil
}

// buildResolver creates a DNS resolver that uses the specified external resolvers,
// bypassing CoreDNS split-horizon.
func buildResolver(resolvers []string) *net.Resolver {
	idx := 0
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Round-robin through configured resolvers
			server := resolvers[idx%len(resolvers)]
			idx++
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", server)
		},
	}
}

// resolveAAAA resolves a domain's AAAA record using the given resolver.
func resolveAAAA(ctx context.Context, resolver *net.Resolver, domain string, log logr.Logger) (net.IP, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ips, err := resolver.LookupIP(ctx, "ip6", domain)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed for %s: %w", domain, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no AAAA records found for %s", domain)
	}

	log.V(1).Info("DNS resolved", "domain", domain, "ip", ips[0].String())
	return ips[0], nil
}

// applyIPv6Suffix extracts the /64 prefix from a resolved IPv6 address
// and applies the given suffix.
//
// Example: resolved=2a01:cb04:6a8:5100::166, suffix="::166/64"
//
//	-> prefix = 2a01:cb04:6a8:5100, result = 2a01:cb04:6a8:5100::166
func applyIPv6Suffix(resolved net.IP, suffix string) (string, error) {
	if suffix == "" {
		return resolved.String(), nil
	}

	// Parse the suffix to extract the interface identifier
	// Suffix format: "::166/64" or "0:0:0:0:0:0:0:166/64"
	suffixAddr := strings.TrimSuffix(suffix, "/64")
	suffixAddr = strings.TrimSuffix(suffixAddr, "/128")

	parsedSuffix := net.ParseIP(suffixAddr)
	if parsedSuffix == nil {
		// Try prepending :: for shorthand like "::166"
		if !strings.Contains(suffixAddr, "::") {
			suffixAddr = "::" + suffixAddr
		}
		parsedSuffix = net.ParseIP(suffixAddr)
		if parsedSuffix == nil {
			return "", fmt.Errorf("invalid IPv6 suffix: %s", suffix)
		}
	}

	// Get the /64 prefix from the resolved address (first 8 bytes)
	resolved16 := resolved.To16()
	if resolved16 == nil {
		return "", fmt.Errorf("not a valid IPv6 address: %s", resolved)
	}
	suffix16 := parsedSuffix.To16()
	if suffix16 == nil {
		return "", fmt.Errorf("not a valid IPv6 suffix: %s", suffixAddr)
	}

	// Combine: first 8 bytes from resolved (prefix) + last 8 bytes from suffix
	var result [16]byte
	copy(result[:8], resolved16[:8])
	copy(result[8:], suffix16[8:])

	return net.IP(result[:]).String(), nil
}

// updateConfigMap creates or updates the cluster-config ConfigMap.
// It merges new data into the existing ConfigMap, preserving keys not managed by this syncer.
// Returns true if the ConfigMap was changed.
func (s *Syncer) updateConfigMap(ctx context.Context, data map[string]string) (bool, error) {
	cmKey := types.NamespacedName{
		Name:      s.cfg.IPv6ConfigMapName,
		Namespace: s.cfg.IPv6ConfigMapNamespace,
	}

	existing := &corev1.ConfigMap{}
	err := s.client.Get(ctx, cmKey, existing)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, fmt.Errorf("failed to get ConfigMap %s: %w", cmKey, err)
		}

		// Create new ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.cfg.IPv6ConfigMapName,
				Namespace: s.cfg.IPv6ConfigMapNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "ddns-updater-operator",
				},
			},
			Data: data,
		}
		s.log.Info("Creating cluster-config ConfigMap", "name", cmKey)
		return true, s.client.Create(ctx, cm)
	}

	// Check if our managed keys changed (only compare the keys we're about to write)
	if managedKeysMatch(existing.Data, data) {
		s.log.V(1).Info("cluster-config ConfigMap unchanged")
		return false, nil
	}

	// Merge new data into existing (preserve non-IPv6 keys)
	if existing.Data == nil {
		existing.Data = make(map[string]string)
	}
	for k, v := range data {
		existing.Data[k] = v
	}
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	existing.Labels["app.kubernetes.io/managed-by"] = "ddns-updater-operator"

	s.log.Info("Updating cluster-config ConfigMap", "name", cmKey)
	return true, s.client.Update(ctx, existing)
}

// annotateFluxKustomizations annotates all Flux Kustomizations in the given namespace
// to trigger immediate reconciliation.
func (s *Syncer) annotateFluxKustomizations(ctx context.Context) error {
	// Use unstructured client to avoid importing Flux types
	kustomizationList := &metav1.PartialObjectMetadataList{}
	kustomizationList.SetGroupVersionKind(fluxKustomizationListGVK)

	if err := s.client.List(ctx, kustomizationList, client.InNamespace(s.cfg.IPv6ConfigMapNamespace)); err != nil {
		return fmt.Errorf("failed to list Flux Kustomizations: %w", err)
	}

	timestamp := time.Now().Format(time.RFC3339Nano)
	for i := range kustomizationList.Items {
		ks := &kustomizationList.Items[i]
		patch := client.MergeFrom(ks.DeepCopy())
		if ks.Annotations == nil {
			ks.Annotations = make(map[string]string)
		}
		ks.Annotations["reconcile.fluxcd.io/requestedAt"] = timestamp
		if err := s.client.Patch(ctx, ks, patch); err != nil {
			s.log.Error(err, "Failed to annotate Flux Kustomization", "name", ks.Name)
			continue
		}
		s.log.V(1).Info("Annotated Flux Kustomization for reconciliation", "name", ks.Name)
	}

	return nil
}

var fluxKustomizationListGVK = schema.GroupVersionKind{
	Group:   "kustomize.toolkit.fluxcd.io",
	Version: "v1",
	Kind:    "KustomizationList",
}

// managedKeysMatch returns true if all keys in newData already exist in existing
// with the same values. This avoids unnecessary ConfigMap updates when only
// unrelated keys differ.
func managedKeysMatch(existing, newData map[string]string) bool {
	for k, v := range newData {
		if existing[k] != v {
			return false
		}
	}
	return true
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
