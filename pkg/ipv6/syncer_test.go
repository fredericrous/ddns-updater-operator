package ipv6

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/config"
)

// --- fake Resolver ---

type fakeResolver struct {
	byHost map[string][]net.IP
	err    error
}

func (f *fakeResolver) LookupIP(_ context.Context, _ string, host string) ([]net.IP, error) {
	if f.err != nil {
		return nil, f.err
	}
	ips, ok := f.byHost[host]
	if !ok {
		return nil, fmt.Errorf("no record for %s", host)
	}
	return ips, nil
}

// --- scheme + builders ---

func ipv6Scheme(t *testing.T) (client.Client, *config.OperatorConfig) {
	t.Helper()
	s := newScheme(t)
	if err := connectivityv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register connectivity scheme: %v", err)
	}
	cfg := config.NewDefaultConfig()
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()
	return c, cfg
}

// --- discoverServers ---

func TestDiscoverServers_Overrides(t *testing.T) {
	c, cfg := ipv6Scheme(t)
	cfg.IPv6Overrides = map[string]config.IPv6Override{
		"GATEWAY": {Domain: "gw.example.test", Suffix: "::1/64"},
	}
	s := &Syncer{client: c, cfg: cfg, log: logr.Discard()}

	servers, err := s.discoverServers(context.Background())
	if err != nil {
		t.Fatalf("discoverServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d: %+v", len(servers), servers)
	}
	if servers[0].Key != "GATEWAY" || servers[0].Domain != "gw.example.test" {
		t.Errorf("unexpected server: %+v", servers[0])
	}
}

func TestDiscoverServers_FromDDNSRecordAnnotations(t *testing.T) {
	s := newScheme(t)
	if err := connectivityv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	record := &connectivityv1alpha1.DDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "home",
			Namespace:   "default",
			Annotations: map[string]string{AnnotationIPv6ConfigMapKey: "HOME_IPV6"},
		},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Host:       "home",
			Domain:     "example.test",
			IPv6Suffix: "::166/64",
		},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(record).Build()
	syncer := &Syncer{client: c, cfg: config.NewDefaultConfig(), log: logr.Discard()}

	servers, err := syncer.discoverServers(context.Background())
	if err != nil {
		t.Fatalf("discoverServers: %v", err)
	}
	if len(servers) != 1 || servers[0].Key != "HOME_IPV6" || servers[0].Domain != "home.example.test" {
		t.Errorf("unexpected servers: %+v", servers)
	}
}

func TestDiscoverServers_RootDomainHostIsAtSign(t *testing.T) {
	s := newScheme(t)
	if err := connectivityv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	record := &connectivityv1alpha1.DDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "root",
			Namespace:   "default",
			Annotations: map[string]string{AnnotationIPv6ConfigMapKey: "ROOT_IPV6"},
		},
		Spec: connectivityv1alpha1.DDNSRecordSpec{Host: "@", Domain: "example.test"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(record).Build()
	syncer := &Syncer{client: c, cfg: config.NewDefaultConfig(), log: logr.Discard()}
	servers, err := syncer.discoverServers(context.Background())
	if err != nil {
		t.Fatalf("discoverServers: %v", err)
	}
	if len(servers) != 1 || servers[0].Domain != "example.test" {
		t.Errorf("@-host should produce bare domain; got %+v", servers)
	}
}

func TestDiscoverServers_OverrideBeatsAnnotation(t *testing.T) {
	s := newScheme(t)
	if err := connectivityv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	record := &connectivityv1alpha1.DDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "home",
			Namespace:   "default",
			Annotations: map[string]string{AnnotationIPv6ConfigMapKey: "HOME_IPV6"},
		},
		Spec: connectivityv1alpha1.DDNSRecordSpec{Host: "home", Domain: "example.test"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(record).Build()
	cfg := config.NewDefaultConfig()
	cfg.IPv6Overrides = map[string]config.IPv6Override{
		"HOME_IPV6": {Domain: "override.example.test"},
	}
	syncer := &Syncer{client: c, cfg: cfg, log: logr.Discard()}
	servers, err := syncer.discoverServers(context.Background())
	if err != nil {
		t.Fatalf("discoverServers: %v", err)
	}
	if len(servers) != 1 || servers[0].Domain != "override.example.test" {
		t.Errorf("override should win, got %+v", servers)
	}
}

// --- syncOnce end-to-end with a fake Resolver ---

func TestSyncOnce_ResolvesAndWritesConfigMap(t *testing.T) {
	s := newScheme(t)
	if err := connectivityv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	record := &connectivityv1alpha1.DDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "home",
			Namespace:   "default",
			Annotations: map[string]string{AnnotationIPv6ConfigMapKey: "HOME_IPV6"},
		},
		Spec: connectivityv1alpha1.DDNSRecordSpec{
			Host:       "home",
			Domain:     "example.test",
			IPv6Suffix: "::166/64",
		},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(record).Build()
	cfg := config.NewDefaultConfig()
	syncer := &Syncer{
		client: c,
		cfg:    cfg,
		log:    logr.Discard(),
		resolver: &fakeResolver{
			byHost: map[string][]net.IP{
				"home.example.test": {net.ParseIP("2a01:cb04:6a8:5100::1")},
			},
		},
	}

	syncer.syncOnce(context.Background())

	got := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name:      cfg.IPv6ConfigMapName,
		Namespace: cfg.IPv6ConfigMapNamespace,
	}, got)
	if err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}
	if got.Data["HOME_IPV6"] != "2a01:cb04:6a8:5100::166" {
		t.Errorf("HOME_IPV6 = %q, want 2a01:cb04:6a8:5100::166; full data=%v",
			got.Data["HOME_IPV6"], got.Data)
	}
	if got.Data["HOME_IPV6_SUFFIX"] != "::166/64" {
		t.Errorf("HOME_IPV6_SUFFIX not written: %v", got.Data)
	}
}

func TestSyncOnce_NoServersDiscovered_NoOp(t *testing.T) {
	c, cfg := ipv6Scheme(t)
	syncer := &Syncer{client: c, cfg: cfg, log: logr.Discard(), resolver: &fakeResolver{}}
	syncer.syncOnce(context.Background())

	got := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name:      cfg.IPv6ConfigMapName,
		Namespace: cfg.IPv6ConfigMapNamespace,
	}, got)
	if err == nil {
		t.Errorf("expected ConfigMap not found when no servers are discovered, got %+v", got)
	}
}

func TestSyncOnce_ResolverErrorSkipsKey(t *testing.T) {
	s := newScheme(t)
	if err := connectivityv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	record := &connectivityv1alpha1.DDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "home",
			Namespace:   "default",
			Annotations: map[string]string{AnnotationIPv6ConfigMapKey: "HOME_IPV6"},
		},
		Spec: connectivityv1alpha1.DDNSRecordSpec{Host: "home", Domain: "example.test"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(record).Build()
	syncer := &Syncer{
		client:   c,
		cfg:      config.NewDefaultConfig(),
		log:      logr.Discard(),
		resolver: &fakeResolver{err: fmt.Errorf("dns dead")},
	}
	syncer.syncOnce(context.Background())

	// No ConfigMap should be created because no keys were successfully resolved.
	got := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name:      syncer.cfg.IPv6ConfigMapName,
		Namespace: syncer.cfg.IPv6ConfigMapNamespace,
	}, got)
	if err == nil {
		t.Errorf("expected no ConfigMap when all DNS lookups fail, got %+v", got)
	}
}

// --- Start: cover the ticker loop by cancelling quickly ---

func TestStart_RunsOnceAndExitsOnContextCancel(t *testing.T) {
	c, cfg := ipv6Scheme(t)
	cfg.IPv6SyncInterval = 10 * time.Second // long; we cancel before it ticks
	syncer := &Syncer{client: c, cfg: cfg, log: logr.Discard(), resolver: &fakeResolver{}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- syncer.Start(ctx) }()

	// Give the initial syncOnce() a chance to run, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}
