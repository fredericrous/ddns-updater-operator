package ipv6

import (
	"context"
	"fmt"
	"net"
	"sort"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/config"
)

// --- pure helpers ---

func TestMapKeys(t *testing.T) {
	got := mapKeys(map[string]string{"a": "1", "b": "2", "c": "3"})
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("mapKeys len = %d, want %d", len(got), len(want))
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("mapKeys[%d] = %q, want %q", i, got[i], k)
		}
	}

	// Empty map returns empty slice (not nil).
	empty := mapKeys(map[string]string{})
	if empty == nil || len(empty) != 0 {
		t.Errorf("mapKeys on empty map should return empty slice, got %v", empty)
	}
}

// TestManagedKeysMatch lives in sync_test.go — not duplicated here.

// --- buildResolver: smoke-test the constructor produces a usable resolver ---

func TestBuildResolver(t *testing.T) {
	// Can't easily observe which upstream gets chosen without reaching the
	// network, but we can verify the resolver is configured as expected and
	// that Dial doesn't panic with multiple calls (covers the round-robin
	// closure path).
	servers := []string{"1.2.3.4:53", "5.6.7.8:53"}
	r := buildResolver(servers)
	if r == nil {
		t.Fatal("buildResolver returned nil")
	}
	if !r.PreferGo {
		t.Error("resolver should set PreferGo=true so the custom Dial is used")
	}
	if r.Dial == nil {
		t.Fatal("resolver.Dial must be populated")
	}
	// UDP "dial" is just a socket bind, so it succeeds even for unreachable
	// IPs — close each conn so we don't leak FDs.
	for i := 0; i < 4; i++ {
		conn, err := r.Dial(context.Background(), "udp", "unused")
		if err != nil {
			continue
		}
		_ = conn.Close()
	}
}

// --- updateConfigMap: create + unchanged + update branches via fake client ---

func newSyncerWithClient(c client.Client) *Syncer {
	return &Syncer{
		client: c,
		cfg:    config.NewDefaultConfig(),
		log:    logr.Discard(),
	}
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("register corev1: %v", err)
	}
	return s
}

func TestUpdateConfigMap_CreatesWhenMissing(t *testing.T) {
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()
	syncer := newSyncerWithClient(c)

	changed, err := syncer.updateConfigMap(context.Background(), map[string]string{"ipv6_home": "::1"})
	if err != nil {
		t.Fatalf("updateConfigMap err: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when ConfigMap is created")
	}

	got := &corev1.ConfigMap{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name:      syncer.cfg.IPv6ConfigMapName,
		Namespace: syncer.cfg.IPv6ConfigMapNamespace,
	}, got)
	if err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}
	if got.Data["ipv6_home"] != "::1" {
		t.Errorf("Data = %v, want ipv6_home=::1", got.Data)
	}
	if got.Labels["app.kubernetes.io/managed-by"] != "ddns-updater-operator" {
		t.Errorf("missing managed-by label: %v", got.Labels)
	}
}

func TestUpdateConfigMap_NoopWhenManagedKeysMatch(t *testing.T) {
	s := newScheme(t)
	cfg := config.NewDefaultConfig()
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.IPv6ConfigMapName, Namespace: cfg.IPv6ConfigMapNamespace},
		Data:       map[string]string{"ipv6_home": "::1", "other": "preserved"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	syncer := newSyncerWithClient(c)

	changed, err := syncer.updateConfigMap(context.Background(), map[string]string{"ipv6_home": "::1"})
	if err != nil {
		t.Fatalf("updateConfigMap err: %v", err)
	}
	if changed {
		t.Error("expected changed=false when managed keys match")
	}
}

func TestUpdateConfigMap_MergesPreservingUnmanagedKeys(t *testing.T) {
	s := newScheme(t)
	cfg := config.NewDefaultConfig()
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.IPv6ConfigMapName, Namespace: cfg.IPv6ConfigMapNamespace},
		Data:       map[string]string{"ipv6_home": "::old", "unmanaged": "keep-me"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	syncer := newSyncerWithClient(c)

	changed, err := syncer.updateConfigMap(context.Background(), map[string]string{"ipv6_home": "::new"})
	if err != nil {
		t.Fatalf("updateConfigMap err: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when value differs")
	}

	got := &corev1.ConfigMap{}
	_ = c.Get(context.Background(), types.NamespacedName{
		Name:      cfg.IPv6ConfigMapName,
		Namespace: cfg.IPv6ConfigMapNamespace,
	}, got)
	if got.Data["ipv6_home"] != "::new" {
		t.Errorf("updated value not written: %v", got.Data)
	}
	if got.Data["unmanaged"] != "keep-me" {
		t.Errorf("unmanaged key should survive merge, got %v", got.Data)
	}
}

// --- resolveAAAA: use a resolver pointed at a local UDP socket that never replies,
//     to exercise the error path without hitting real DNS. ---

func TestResolveAAAA_LookupFailurePropagates(t *testing.T) {
	// Use a closed port so the resolver errors out quickly.
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return nil, fmt.Errorf("simulated dial failure")
		},
	}
	_, err := resolveAAAA(context.Background(), r, "example.test", logr.Discard())
	if err == nil {
		t.Error("expected error when DNS dial fails")
	}
}

// --- NewSyncer + annotateFluxKustomizations ---

func TestNewSyncer(t *testing.T) {
	cfg := config.NewDefaultConfig()
	c := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).Build()
	s := NewSyncer(c, cfg, logr.Discard())
	if s == nil {
		t.Fatal("NewSyncer returned nil")
	}
	if s.cfg != cfg {
		t.Error("NewSyncer did not wire config")
	}
	if s.client != c {
		t.Error("NewSyncer did not wire client")
	}
}

func TestAnnotateFluxKustomizations_EmptyList(t *testing.T) {
	// With no Kustomization resources in the target namespace, the function
	// should return nil without errors.
	c := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).Build()
	s := newSyncerWithClient(c)
	if err := s.annotateFluxKustomizations(context.Background()); err != nil {
		t.Errorf("empty list should not error, got %v", err)
	}
}
