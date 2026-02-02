package assembler

import (
	"cmp"
	"context"
	"encoding/json"
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

// DDNSEntry represents a single entry in the ddns-updater config
type DDNSEntry struct {
	Provider    string `json:"provider"`
	Domain      string `json:"domain"`
	Host        string `json:"host"`
	Mode        string `json:"mode,omitempty"`
	IPVersion   string `json:"ip_version,omitempty"`
	IPv6Suffix  string `json:"ipv6_suffix,omitempty"`
	AppKey      string `json:"app_key,omitempty"`
	AppSecret   string `json:"app_secret,omitempty"`
	ConsumerKey string `json:"consumer_key,omitempty"`
}

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

	// Group records by credential reference to minimize secret lookups
	credCache := make(map[string]*corev1.Secret)

	for _, record := range sortedRecords {
		entries, err := a.buildEntries(ctx, &record, credCache)
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, entries...)
	}

	// Build JSON config
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
	credCache map[string]*corev1.Secret,
) ([]DDNSEntry, error) {
	spec := &record.Spec

	// Resolve credentials based on provider
	var creds *OVHCredentials
	if spec.Provider == "ovh" {
		var err error
		creds, err = a.resolveOVHCredentials(ctx, record, credCache)
		if err != nil {
			return nil, err
		}
	}

	// Determine which IP versions to create entries for
	ipVersions := []string{cmp.Or(spec.IPVersion, "ipv4")}
	if spec.IPVersion == "ipv4_and_ipv6" {
		ipVersions = []string{"ipv4", "ipv6"}
	}

	entries := make([]DDNSEntry, 0, len(ipVersions))
	for _, ipVersion := range ipVersions {
		entry := DDNSEntry{
			Provider:  spec.Provider,
			Domain:    spec.Domain,
			Host:      spec.Host,
			Mode:      cmp.Or(spec.ProviderConfig.Mode, "api"),
			IPVersion: ipVersion,
		}

		// Add IPv6 suffix for IPv6 entries if specified
		if ipVersion == "ipv6" && spec.IPv6Suffix != "" {
			entry.IPv6Suffix = spec.IPv6Suffix
		}

		if creds != nil {
			entry.AppKey = creds.AppKey
			entry.AppSecret = creds.AppSecret
			entry.ConsumerKey = creds.ConsumerKey
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// OVHCredentials holds resolved OVH credentials
type OVHCredentials struct {
	AppKey      string
	AppSecret   string
	ConsumerKey string
}

// resolveOVHCredentials resolves OVH credentials from the referenced secret
func (a *Assembler) resolveOVHCredentials(
	ctx context.Context,
	record *connectivityv1alpha1.DDNSRecord,
	credCache map[string]*corev1.Secret,
) (*OVHCredentials, error) {
	ref := &record.Spec.ProviderConfig.CredentialsRef
	namespace := cmp.Or(ref.Namespace, record.Namespace)
	cacheKey := namespace + "/" + ref.Name

	// Check cache first
	secret, ok := credCache[cacheKey]
	if !ok {
		secret = &corev1.Secret{}
		if err := a.Client.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, secret); err != nil {
			return nil, operrors.NewTransientError("failed to get credentials secret", err).
				WithContext("secretName", ref.Name).
				WithContext("namespace", namespace).
				WithContext("record", record.Namespace+"/"+record.Name)
		}
		credCache[cacheKey] = secret
	}

	// Extract credentials
	appKey := string(secret.Data["OVH_APPLICATION_KEY"])
	if appKey == "" {
		return nil, operrors.NewConfigError("OVH_APPLICATION_KEY not found in secret", nil).
			WithContext("secretName", ref.Name).
			WithContext("namespace", namespace)
	}

	appSecret := string(secret.Data["OVH_APPLICATION_SECRET"])
	if appSecret == "" {
		return nil, operrors.NewConfigError("OVH_APPLICATION_SECRET not found in secret", nil).
			WithContext("secretName", ref.Name).
			WithContext("namespace", namespace)
	}

	consumerKey := string(secret.Data["OVH_CONSUMER_KEY"])
	if consumerKey == "" {
		return nil, operrors.NewConfigError("OVH_CONSUMER_KEY not found in secret", nil).
			WithContext("secretName", ref.Name).
			WithContext("namespace", namespace)
	}

	return &OVHCredentials{
		AppKey:      appKey,
		AppSecret:   appSecret,
		ConsumerKey: consumerKey,
	}, nil
}
