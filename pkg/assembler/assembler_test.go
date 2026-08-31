package assembler

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
)

// newTestAssembler builds an Assembler backed by a fake client holding the
// given objects.
func newTestAssembler(objs ...runtime.Object) *Assembler {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = connectivityv1alpha1.AddToScheme(scheme)

	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objs {
		builder = builder.WithRuntimeObjects(obj)
	}

	return NewAssembler(builder.Build(), zap.New(zap.UseDevMode(true)))
}

func ovhSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ddns-credentials",
			Namespace: "ddns-updater",
		},
		Data: map[string][]byte{
			"OVH_APPLICATION_KEY":    []byte("test-app-key"),
			"OVH_APPLICATION_SECRET": []byte("test-app-secret"),
			"OVH_CONSUMER_KEY":       []byte("test-consumer-key"),
		},
	}
}

// ovhConfigFrom is the configFrom block an OVH record needs in API mode.
func ovhConfigFrom() []connectivityv1alpha1.ConfigFromSource {
	return []connectivityv1alpha1.ConfigFromSource{
		{Name: "app_key", SecretKeyRef: connectivityv1alpha1.SecretKeySelector{
			Name: "ddns-credentials", Namespace: "ddns-updater", Key: "OVH_APPLICATION_KEY"}},
		{Name: "app_secret", SecretKeyRef: connectivityv1alpha1.SecretKeySelector{
			Name: "ddns-credentials", Namespace: "ddns-updater", Key: "OVH_APPLICATION_SECRET"}},
		{Name: "consumer_key", SecretKeyRef: connectivityv1alpha1.SecretKeySelector{
			Name: "ddns-credentials", Namespace: "ddns-updater", Key: "OVH_CONSUMER_KEY"}},
	}
}

func jsonValue(t *testing.T, v any) apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling test value %v: %v", v, err)
	}
	return apiextensionsv1.JSON{Raw: raw}
}

// parseSettings unmarshals the assembled JSON back into open entries.
func parseSettings(t *testing.T, configJSON string) []DDNSEntry {
	t.Helper()
	var config DDNSConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("Failed to unmarshal config JSON: %v", err)
	}
	return config.Settings
}

func TestAssembler_Assemble(t *testing.T) {
	assembler := newTestAssembler(ovhSecret())

	records := []connectivityv1alpha1.DDNSRecord{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "www", Namespace: "ddns-updater"},
			Spec: connectivityv1alpha1.DDNSRecordSpec{
				Provider:   "ovh",
				Domain:     "example.com",
				Host:       "www",
				Config:     map[string]apiextensionsv1.JSON{"mode": jsonValue(t, "api")},
				ConfigFrom: ovhConfigFrom(),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "root", Namespace: "ddns-updater"},
			Spec: connectivityv1alpha1.DDNSRecordSpec{
				Provider:   "ovh",
				Domain:     "example.com",
				Host:       "@",
				Config:     map[string]apiextensionsv1.JSON{"mode": jsonValue(t, "api")},
				ConfigFrom: ovhConfigFrom(),
			},
		},
	}

	result, err := assembler.Assemble(context.Background(), records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if len(result.Entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(result.Entries))
	}

	settings := parseSettings(t, result.ConfigJSON)
	if len(settings) != 2 {
		t.Fatalf("Expected 2 settings, got %d", len(settings))
	}

	// Sorted by domain then host, so "@" comes before "www"
	if got := settings[0]["host"]; got != "@" {
		t.Errorf("Expected first entry host '@', got %v", got)
	}
	if got := settings[1]["host"]; got != "www" {
		t.Errorf("Expected second entry host 'www', got %v", got)
	}

	first := settings[0]
	if got := first["app_key"]; got != "test-app-key" {
		t.Errorf("Expected app_key 'test-app-key', got %v", got)
	}
	if got := first["app_secret"]; got != "test-app-secret" {
		t.Errorf("Expected app_secret 'test-app-secret', got %v", got)
	}
	if got := first["consumer_key"]; got != "test-consumer-key" {
		t.Errorf("Expected consumer_key 'test-consumer-key', got %v", got)
	}
	if got := first["mode"]; got != "api" {
		t.Errorf("Expected mode 'api', got %v", got)
	}
	if got := first["ip_version"]; got != "ipv4" {
		t.Errorf("Expected default ip_version 'ipv4', got %v", got)
	}
}

// A provider the operator has no special knowledge of must round-trip its
// settings — including non-string JSON types — untouched.
func TestAssembler_ArbitraryProvider(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cloudflare-creds", Namespace: "ddns-updater"},
		Data:       map[string][]byte{"CF_API_TOKEN": []byte("cf-token")},
	}
	assembler := newTestAssembler(secret)

	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "cf", Namespace: "ddns-updater"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider: "cloudflare",
			Domain:   "example.com",
			Host:     "home",
			Config: map[string]apiextensionsv1.JSON{
				"zone_identifier": jsonValue(t, "abc123"),
				"proxied":         jsonValue(t, true),
				"ttl":             jsonValue(t, 300),
			},
			ConfigFrom: []connectivityv1alpha1.ConfigFromSource{{
				Name: "token",
				SecretKeyRef: connectivityv1alpha1.SecretKeySelector{
					Name: "cloudflare-creds", Key: "CF_API_TOKEN"},
			}},
		},
	}}

	result, err := assembler.Assemble(context.Background(), records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	entry := parseSettings(t, result.ConfigJSON)[0]
	if got := entry["provider"]; got != "cloudflare" {
		t.Errorf("Expected provider 'cloudflare', got %v", got)
	}
	if got := entry["zone_identifier"]; got != "abc123" {
		t.Errorf("Expected zone_identifier 'abc123', got %v", got)
	}
	if got := entry["proxied"]; got != true {
		t.Errorf("Expected proxied bool true, got %v (%T)", got, got)
	}
	if got := entry["ttl"]; got != float64(300) {
		t.Errorf("Expected ttl 300, got %v (%T)", got, got)
	}
	if got := entry["token"]; got != "cf-token" {
		t.Errorf("Expected token 'cf-token', got %v", got)
	}
}

// The secretKeyRef namespace is optional and falls back to the record's.
func TestAssembler_SecretNamespaceDefaultsToRecord(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "duck-creds", Namespace: "other-ns"},
		Data:       map[string][]byte{"TOKEN": []byte("duck-token")},
	}
	assembler := newTestAssembler(secret)

	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "duck", Namespace: "other-ns"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider: "duckdns",
			Domain:   "example.duckdns.org",
			Host:     "@",
			ConfigFrom: []connectivityv1alpha1.ConfigFromSource{{
				Name:         "token",
				SecretKeyRef: connectivityv1alpha1.SecretKeySelector{Name: "duck-creds", Key: "TOKEN"},
			}},
		},
	}}

	result, err := assembler.Assemble(context.Background(), records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if got := parseSettings(t, result.ConfigJSON)[0]["token"]; got != "duck-token" {
		t.Errorf("Expected token 'duck-token', got %v", got)
	}
}

func TestAssembler_IPv4AndIPv6(t *testing.T) {
	assembler := newTestAssembler(ovhSecret())

	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "dual-stack", Namespace: "ddns-updater"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider:   "ovh",
			Domain:     "example.com",
			Host:       "www",
			IPVersion:  "ipv4_and_ipv6",
			IPv6Suffix: "::166/64",
			Config:     map[string]apiextensionsv1.JSON{"mode": jsonValue(t, "api")},
			ConfigFrom: ovhConfigFrom(),
		},
	}}

	result, err := assembler.Assemble(context.Background(), records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if len(result.Entries) != 2 {
		t.Fatalf("Expected 2 entries for ipv4_and_ipv6, got %d", len(result.Entries))
	}

	ipVersions := make(map[string]bool)
	for _, entry := range parseSettings(t, result.ConfigJSON) {
		version, _ := entry["ip_version"].(string)
		ipVersions[version] = true

		if got := entry["host"]; got != "www" {
			t.Errorf("Expected host 'www', got %v", got)
		}
		// The suffix only makes sense on the IPv6 half of the pair.
		_, hasSuffix := entry["ipv6_suffix"]
		if version == "ipv6" && !hasSuffix {
			t.Error("Expected ipv6_suffix on the ipv6 entry")
		}
		if version == "ipv4" && hasSuffix {
			t.Error("Did not expect ipv6_suffix on the ipv4 entry")
		}
		// Provider settings are copied onto both halves.
		if got := entry["app_key"]; got != "test-app-key" {
			t.Errorf("Expected app_key on every entry, got %v", got)
		}
	}

	if !ipVersions["ipv4"] {
		t.Error("Expected ipv4 entry not found")
	}
	if !ipVersions["ipv6"] {
		t.Error("Expected ipv6 entry not found")
	}
}

// ddns-updater spells the either-family version "ipv4 or ipv6"; the CRD uses
// an enum-safe underscore form that must be translated on the way out.
func TestAssembler_IPv4OrIPv6Translated(t *testing.T) {
	assembler := newTestAssembler()

	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "either", Namespace: "ddns-updater"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider:  "duckdns",
			Domain:    "example.duckdns.org",
			Host:      "@",
			IPVersion: "ipv4_or_ipv6",
		},
	}}

	result, err := assembler.Assemble(context.Background(), records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if got := parseSettings(t, result.ConfigJSON)[0]["ip_version"]; got != "ipv4 or ipv6" {
		t.Errorf("Expected ip_version 'ipv4 or ipv6', got %v", got)
	}
}

func TestAssembler_MissingSecret(t *testing.T) {
	assembler := newTestAssembler()

	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ddns-updater"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider: "ovh",
			Domain:   "example.com",
			Host:     "@",
			ConfigFrom: []connectivityv1alpha1.ConfigFromSource{{
				Name: "app_key",
				SecretKeyRef: connectivityv1alpha1.SecretKeySelector{
					Name: "missing-secret", Namespace: "ddns-updater", Key: "OVH_APPLICATION_KEY"},
			}},
		},
	}}

	if _, err := assembler.Assemble(context.Background(), records); err == nil {
		t.Error("Expected error for missing secret, got nil")
	}
}

func TestAssembler_MissingSecretKey(t *testing.T) {
	assembler := newTestAssembler(ovhSecret())

	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ddns-updater"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider: "ovh",
			Domain:   "example.com",
			Host:     "@",
			ConfigFrom: []connectivityv1alpha1.ConfigFromSource{{
				Name: "app_key",
				SecretKeyRef: connectivityv1alpha1.SecretKeySelector{
					Name: "ddns-credentials", Namespace: "ddns-updater", Key: "NOPE"},
			}},
		},
	}}

	if _, err := assembler.Assemble(context.Background(), records); err == nil {
		t.Error("Expected error for missing secret key, got nil")
	}
}

func TestAssembler_RejectsReservedAndDuplicateKeys(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ddns-updater"},
		Data:       map[string][]byte{"TOKEN": []byte("t")},
	}

	base := func() connectivityv1alpha1.DDNSRecordSpec {
		return connectivityv1alpha1.DDNSRecordSpec{
			Provider: "duckdns",
			Domain:   "example.duckdns.org",
			Host:     "@",
		}
	}

	tests := map[string]func(spec *connectivityv1alpha1.DDNSRecordSpec){
		"reserved key in config": func(spec *connectivityv1alpha1.DDNSRecordSpec) {
			spec.Config = map[string]apiextensionsv1.JSON{"domain": jsonValue(t, "evil.example")}
		},
		"reserved key in configFrom": func(spec *connectivityv1alpha1.DDNSRecordSpec) {
			spec.ConfigFrom = []connectivityv1alpha1.ConfigFromSource{{
				Name:         "provider",
				SecretKeyRef: connectivityv1alpha1.SecretKeySelector{Name: "creds", Key: "TOKEN"},
			}}
		},
		"key set twice": func(spec *connectivityv1alpha1.DDNSRecordSpec) {
			spec.Config = map[string]apiextensionsv1.JSON{"token": jsonValue(t, "inline")}
			spec.ConfigFrom = []connectivityv1alpha1.ConfigFromSource{{
				Name:         "token",
				SecretKeyRef: connectivityv1alpha1.SecretKeySelector{Name: "creds", Key: "TOKEN"},
			}}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			assembler := newTestAssembler(secret)
			spec := base()
			mutate(&spec)

			records := []connectivityv1alpha1.DDNSRecord{{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ddns-updater"},
				Spec:       spec,
			}}

			if _, err := assembler.Assemble(context.Background(), records); err == nil {
				t.Error("Expected error, got nil")
			}
		})
	}
}

// The ConfigMap is only rewritten when the JSON hash moves, so assembling the
// same records twice must produce byte-identical output.
func TestAssembler_DeterministicOutput(t *testing.T) {
	records := []connectivityv1alpha1.DDNSRecord{{
		ObjectMeta: metav1.ObjectMeta{Name: "cf", Namespace: "ddns-updater"},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Provider: "cloudflare",
			Domain:   "example.com",
			Host:     "home",
			Config: map[string]apiextensionsv1.JSON{
				"zone_identifier": jsonValue(t, "abc123"),
				"proxied":         jsonValue(t, true),
				"ttl":             jsonValue(t, 300),
				"email":           jsonValue(t, "a@example.com"),
			},
		},
	}}

	assembler := newTestAssembler()
	first, err := assembler.Assemble(context.Background(), records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	for i := range 5 {
		next, err := assembler.Assemble(context.Background(), records)
		if err != nil {
			t.Fatalf("Assemble() error = %v", err)
		}
		if next.ConfigJSON != first.ConfigJSON {
			t.Fatalf("Config JSON differs on run %d:\n%s\nvs\n%s", i+2, next.ConfigJSON, first.ConfigJSON)
		}
	}
}

func TestIsKnownProvider(t *testing.T) {
	for _, name := range []string{"ovh", "cloudflare", "duckdns", "name.com", "selfhost.de"} {
		if !IsKnownProvider(name) {
			t.Errorf("Expected %q to be a known provider", name)
		}
	}
	for _, name := range []string{"cloudlfare", "", "not-a-provider"} {
		if IsKnownProvider(name) {
			t.Errorf("Expected %q not to be a known provider", name)
		}
	}
}
