package assembler

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
	operrors "github.com/fredericrous/homelab/ddns-updater-operator/pkg/errors"
)

// Assembler handles DDNS configuration assembly for ddns-updater
type Assembler struct {
	Client client.Client
	Log    logr.Logger
}

// NewAssembler creates a new Assembler
func NewAssembler(client client.Client, log logr.Logger) *Assembler {
	return &Assembler{Client: client, Log: log}
}

// DDNSEntry is a single entry of the ddns-updater config. ddns-updater reads
// the common fields below and hands the very same object to the provider,
// which unmarshals its own settings from the remaining top-level keys — so an
// entry is an open map rather than a fixed struct.
type DDNSEntry map[string]any

// Reserved keys of a ddns-updater config entry. They are derived from the
// DDNSRecord spec fields and may not be set through spec.config/configFrom.
const (
	keyProvider   = "provider"
	keyDomain     = "domain"
	keyHost       = "host"
	keyIPVersion  = "ip_version"
	keyIPv6Suffix = "ipv6_suffix"
)

var reservedKeys = []string{keyProvider, keyDomain, keyHost, keyIPVersion, keyIPv6Suffix}

// DDNSConfig represents the complete ddns-updater configuration
type DDNSConfig struct {
	Settings []DDNSEntry `json:"settings"`
}

// AssemblyResult contains the result of DDNS configuration assembly
type AssemblyResult struct {
	Entries    []DDNSEntry
	ConfigJSON string
}

// Assemble processes all DDNSRecords and assembles the ddns-updater configuration
func (a *Assembler) Assemble(ctx context.Context, records []connectivityv1alpha1.DDNSRecord) (*AssemblyResult, error) {
	result := &AssemblyResult{
		Entries: make([]DDNSEntry, 0, len(records)),
	}

	// Sort records by domain and host for deterministic output
	sortedRecords := slices.Clone(records)
	slices.SortFunc(sortedRecords, func(a, b connectivityv1alpha1.DDNSRecord) int {
		if c := cmp.Compare(a.Spec.Domain, b.Spec.Domain); c != 0 {
			return c
		}
		return cmp.Compare(a.Spec.Host, b.Spec.Host)
	})

	// Cache resolved Secrets so records sharing credentials cost one lookup
	secretCache := make(map[string]*corev1.Secret)

	for i := range sortedRecords {
		entries, err := a.buildEntries(ctx, &sortedRecords[i], secretCache)
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, entries...)
	}

	// Build JSON config. Map keys marshal in sorted order, so the output is
	// stable across reconciles and the config hash only moves on real changes.
	config := DDNSConfig{Settings: result.Entries}
	jsonBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, operrors.NewPermanentError("failed to marshal config JSON", err)
	}
	result.ConfigJSON = string(jsonBytes)

	return result, nil
}

// buildEntries builds DDNSEntry(s) from a DDNSRecord
// For ipv4_and_ipv6, it creates two entries since ddns-updater doesn't support both in one
func (a *Assembler) buildEntries(
	ctx context.Context,
	record *connectivityv1alpha1.DDNSRecord,
	secretCache map[string]*corev1.Secret,
) ([]DDNSEntry, error) {
	spec := &record.Spec

	// Provider-specific settings, shared by every entry this record produces
	providerSettings, err := a.resolveProviderSettings(ctx, record, secretCache)
	if err != nil {
		return nil, err
	}

	// ipv4_and_ipv6 is an operator-level convenience: ddns-updater cannot
	// express both families in one entry, so it becomes two.
	ipVersions := []string{cmp.Or(spec.IPVersion, "ipv4")}
	if spec.IPVersion == "ipv4_and_ipv6" {
		ipVersions = []string{"ipv4", "ipv6"}
	}

	entries := make([]DDNSEntry, 0, len(ipVersions))
	for _, ipVersion := range ipVersions {
		entry := DDNSEntry{
			keyProvider:  spec.Provider,
			keyDomain:    spec.Domain,
			keyHost:      spec.Host,
			keyIPVersion: ddnsIPVersion(ipVersion),
		}

		// Add IPv6 suffix for IPv6 entries if specified
		if spec.IPv6Suffix != "" && (ipVersion == "ipv6" || ipVersion == "ipv4_or_ipv6") {
			entry[keyIPv6Suffix] = spec.IPv6Suffix
		}

		for k, v := range providerSettings {
			entry[k] = v
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// ddnsIPVersion translates the CRD's ipVersion vocabulary into the values
// ddns-updater's ipversion.Parse accepts. Everything but the "or" form is
// already spelled the way upstream wants it.
func ddnsIPVersion(ipVersion string) string {
	if ipVersion == "ipv4_or_ipv6" {
		return "ipv4 or ipv6"
	}
	return ipVersion
}

// resolveProviderSettings merges spec.config with the Secret values named by
// spec.configFrom into the flat key/value set ddns-updater hands to the
// provider.
func (a *Assembler) resolveProviderSettings(
	ctx context.Context,
	record *connectivityv1alpha1.DDNSRecord,
	secretCache map[string]*corev1.Secret,
) (map[string]any, error) {
	spec := &record.Spec
	settings := make(map[string]any, len(spec.Config)+len(spec.ConfigFrom))

	for key, raw := range spec.Config {
		if err := checkSettingKey(record, key); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal(raw.Raw, &value); err != nil {
			return nil, operrors.NewConfigError(
				fmt.Sprintf("config value for %q is not valid JSON", key), err).
				WithContext("record", record.Namespace+"/"+record.Name)
		}
		settings[key] = value
	}

	for i := range spec.ConfigFrom {
		src := &spec.ConfigFrom[i]
		if err := checkSettingKey(record, src.Name); err != nil {
			return nil, err
		}
		if _, exists := settings[src.Name]; exists {
			return nil, operrors.NewConfigError(
				fmt.Sprintf("setting %q is set by both config and configFrom", src.Name), nil).
				WithContext("record", record.Namespace+"/"+record.Name)
		}

		value, err := a.resolveSecretValue(ctx, record, &src.SecretKeyRef, secretCache)
		if err != nil {
			return nil, err
		}
		settings[src.Name] = value
	}

	return settings, nil
}

// checkSettingKey rejects provider settings that would overwrite a field the
// spec already owns.
func checkSettingKey(record *connectivityv1alpha1.DDNSRecord, key string) error {
	if !slices.Contains(reservedKeys, key) {
		return nil
	}
	return operrors.NewConfigError(
		fmt.Sprintf("setting %q is reserved and must be set through the DDNSRecord spec", key), nil).
		WithContext("record", record.Namespace+"/"+record.Name)
}

// resolveSecretValue reads one key out of the referenced Secret.
func (a *Assembler) resolveSecretValue(
	ctx context.Context,
	record *connectivityv1alpha1.DDNSRecord,
	ref *connectivityv1alpha1.SecretKeySelector,
	secretCache map[string]*corev1.Secret,
) (string, error) {
	namespace := cmp.Or(ref.Namespace, record.Namespace)
	cacheKey := namespace + "/" + ref.Name

	secret, ok := secretCache[cacheKey]
	if !ok {
		secret = &corev1.Secret{}
		if err := a.Client.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, secret); err != nil {
			return "", operrors.NewTransientError("failed to get credentials secret", err).
				WithContext("secretName", ref.Name).
				WithContext("namespace", namespace).
				WithContext("record", record.Namespace+"/"+record.Name)
		}
		secretCache[cacheKey] = secret
	}

	value, ok := secret.Data[ref.Key]
	if !ok || len(value) == 0 {
		return "", operrors.NewConfigError(
			fmt.Sprintf("key %q not found in secret", ref.Key), nil).
			WithContext("secretName", ref.Name).
			WithContext("namespace", namespace).
			WithContext("record", record.Namespace+"/"+record.Name)
	}

	return string(value), nil
}
