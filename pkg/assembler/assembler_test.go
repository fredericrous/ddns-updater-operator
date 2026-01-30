package assembler

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
)

func TestAssembler_Assemble(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = connectivityv1alpha1.AddToScheme(scheme)

	// Create test secret
	secret := &corev1.Secret{
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

	// Create fake client with secret
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret).
		Build()

	// Create assembler
	log := zap.New(zap.UseDevMode(true))
	assembler := NewAssembler(client, log)

	// Create test records
	records := []connectivityv1alpha1.DDNSRecord{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "root",
				Namespace: "ddns-updater",
			},
			Spec: connectivityv1alpha1.DDNSRecordSpec{
				Provider: "ovh",
				Domain:   "example.com",
				Host:     "@",
				ProviderConfig: connectivityv1alpha1.OVHProviderConfig{
					Mode: "api",
					CredentialsRef: connectivityv1alpha1.SecretReference{
						Name:      "ddns-credentials",
						Namespace: "ddns-updater",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "www",
				Namespace: "ddns-updater",
			},
			Spec: connectivityv1alpha1.DDNSRecordSpec{
				Provider: "ovh",
				Domain:   "example.com",
				Host:     "www",
				ProviderConfig: connectivityv1alpha1.OVHProviderConfig{
					Mode: "api",
					CredentialsRef: connectivityv1alpha1.SecretReference{
						Name:      "ddns-credentials",
						Namespace: "ddns-updater",
					},
				},
			},
		},
	}

	// Run assembler
	ctx := context.Background()
	result, err := assembler.Assemble(ctx, records)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Verify entries
	if len(result.Entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(result.Entries))
	}

	// Verify JSON structure
	var config DDNSConfig
	if err := json.Unmarshal([]byte(result.ConfigJSON), &config); err != nil {
		t.Fatalf("Failed to unmarshal config JSON: %v", err)
	}

	if len(config.Settings) != 2 {
		t.Errorf("Expected 2 settings, got %d", len(config.Settings))
	}

	// Verify first entry (should be @ since sorted by host)
	if config.Settings[0].Host != "@" {
		t.Errorf("Expected first entry host '@', got '%s'", config.Settings[0].Host)
	}
	if config.Settings[0].AppKey != "test-app-key" {
		t.Errorf("Expected app_key 'test-app-key', got '%s'", config.Settings[0].AppKey)
	}

	// Verify second entry
	if config.Settings[1].Host != "www" {
		t.Errorf("Expected second entry host 'www', got '%s'", config.Settings[1].Host)
	}
}

func TestAssembler_MissingSecret(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = connectivityv1alpha1.AddToScheme(scheme)

	// Create fake client WITHOUT secret
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create assembler
	log := zap.New(zap.UseDevMode(true))
	assembler := NewAssembler(client, log)

	// Create test record
	records := []connectivityv1alpha1.DDNSRecord{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "ddns-updater",
			},
			Spec: connectivityv1alpha1.DDNSRecordSpec{
				Provider: "ovh",
				Domain:   "example.com",
				Host:     "@",
				ProviderConfig: connectivityv1alpha1.OVHProviderConfig{
					CredentialsRef: connectivityv1alpha1.SecretReference{
						Name:      "missing-secret",
						Namespace: "ddns-updater",
					},
				},
			},
		},
	}

	// Run assembler - should fail
	ctx := context.Background()
	_, err := assembler.Assemble(ctx, records)
	if err == nil {
		t.Error("Expected error for missing secret, got nil")
	}
}
