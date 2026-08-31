# Configuration

Every manager flag has a matching Helm value; the chart renders the flags onto
the manager's `args`.

| Flag | Default | Helm value |
| --- | --- | --- |
| `--ddns-namespace` | `ddns-updater` | `config.ddnsNamespace` |
| `--ddns-configmap` | `ddns-updater-config` | `config.ddnsConfigMap` |
| `--metrics-bind-address` | `:8080` | `metrics.port` / `metrics.enabled` |
| `--health-probe-bind-address` | `:8081` | `health.port` |
| `--leader-elect` | `false` | `config.leaderElect` |
| `--leader-election-id` | `ddns-updater-operator` | — |
| `--max-concurrent-reconciles` | `3` | `config.maxConcurrentReconciles` |
| `--reconcile-timeout` | `5m` | — |
| `--zap-log-level` | `info` | `config.logLevel` |
| `--zap-encoder` | `json` | `config.logEncoder` |
| `--zap-devel` | `false` | — |
| `--ipv6-enabled` | `false` | `config.ipv6.enabled` |
| `--ipv6-configmap-name` | `cluster-config` | `config.ipv6.configMapName` |
| `--ipv6-configmap-namespace` | `flux-system` | `config.ipv6.configMapNamespace` |
| `--ipv6-sync-interval` | `5m` | `config.ipv6.syncInterval` |
| `--ipv6-resolvers` | `1.1.1.1:53,9.9.9.9:53` | `config.ipv6.resolvers` |
| `--ipv6-overrides` | — | `config.ipv6.overrides` (rendered as JSON) |

Invalid combinations are rejected at startup by `OperatorConfig.Validate()`:
`maxConcurrentReconciles` must be at least 1, `reconcileTimeout` at least a
second, and `ddnsNamespace` non-empty.

## Health and metrics

- `/healthz` and `/readyz` on the probe port (`:8081` by default); the chart
  wires both to liveness and readiness probes.
- Prometheus metrics — the standard controller-runtime set — on the metrics
  port (`:8080`). Setting `metrics.enabled: false` passes
  `--metrics-bind-address=0`, which disables the listener entirely.

## RBAC

The chart's ClusterRole grants what the controller actually uses: read on
Secrets; full access to ConfigMaps; read/write on `ddnsrecords` and their
status; Events; and Leases for leader election.

Enabling `config.ipv6.enabled` additionally grants get/list/patch on
`kustomize.toolkit.fluxcd.io` Kustomizations, which the
[IPv6 syncer](ipv6.md) annotates to trigger a Flux reconcile.

## Leader election

Off by default, which suits a single replica. Turn on `config.leaderElect` when
running more than one; the lease is named by `--leader-election-id` and lives in
the release namespace.
