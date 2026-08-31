# ddns-updater-operator

A Kubernetes operator that turns `DDNSRecord` custom resources into the
`config.json` consumed by [ddns-updater](https://github.com/qdm12/ddns-updater).

Instead of hand-editing a JSON blob (and the Secret holding the API keys) every
time a hostname is added, you declare one `DDNSRecord` per hostname. The
operator watches them cluster-wide, resolves the referenced Secrets, and writes
a single assembled `config.json` into a ConfigMap that ddns-updater mounts.

**Every provider ddns-updater supports works** — cloudflare, ovh, duckdns,
route53, gandi, porkbun, namecheap and the rest. The operator does not model
providers individually: `spec.config` and `spec.configFrom` are passed through
verbatim as the provider's settings, so a provider added upstream needs no
operator release.

It also ships an optional IPv6 syncer for dynamic-prefix ISPs: it periodically
resolves the AAAA records of annotated records, recombines the current /64
prefix with a fixed interface suffix, and publishes the result into a ConfigMap
(by default `flux-system/cluster-config`) so the rest of the cluster can
template against a stable key.

- **API group:** `connectivity.homelab.io/v1alpha1`
- **Kind:** `DDNSRecord` (short name `ddns`)
- **Image:** `ghcr.io/fredericrous/ddns-updater-operator`

## How it works

```
DDNSRecord (x N)  ─┐
                   ├─► Assembler ─► ConfigMap ddns-updater/ddns-updater-config
Secret (API keys) ─┘                  └─ config.json  ─► mounted by ddns-updater

DDNSRecord annotated with
ddns.homelab.io/ipv6-configmap-key ─► IPv6 syncer ─► ConfigMap flux-system/cluster-config
                                        └─ AAAA lookup via external resolvers
                                        └─ annotates Flux Kustomizations to reconcile
```

Every reconcile lists *all* `DDNSRecord`s and rebuilds the whole config, sorted
by domain then host so the output is deterministic. The ConfigMap is only
written when the SHA-256 of the rendered JSON differs from the
`ddns.homelab.io/config-hash` annotation already on it, and status writes are
skipped once `status.observedGeneration` matches `metadata.generation` — so a
steady-state cluster produces no churn.

> **Note on secrets:** the assembled `config.json` embeds provider credentials
> in plaintext, because that is the format ddns-updater expects. The target
> ConfigMap is therefore as sensitive as the source Secret — keep it in a
> namespace with matching RBAC.

## Install

The Helm chart is published to <https://fredericrous.github.io/charts>:

```sh
helm repo add fredericrous https://fredericrous.github.io/charts
helm repo update
helm install ddns-updater-operator fredericrous/ddns-updater-operator \
  --namespace ddns-updater --create-namespace
```

The chart installs the CRD, a ClusterRole/Binding, a ServiceAccount and the
manager Deployment. A copy of the chart also lives in `chart/` in this repo if
you prefer to install from source.

To install just the CRD against the current kubecontext:

```sh
make install    # kubectl apply -f config/crd/bases/
```

## Usage

A `DDNSRecord` has two halves: the fields the operator understands (`provider`,
`domain`, `host`, `ipVersion`, `ipv6Suffix`) and the provider's own settings,
which are passed straight through.

- `spec.config` — plain settings, any JSON type (string, bool, number).
- `spec.configFrom` — settings whose value comes from a Secret key.

Which settings a provider expects is documented upstream, one file per provider
under [`docs/`](https://github.com/qdm12/ddns-updater/tree/master/docs) — the
key names there are exactly the key names used here.

### Cloudflare

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloudflare-credentials
  namespace: ddns-updater
stringData:
  CF_API_TOKEN: "..."
---
apiVersion: connectivity.homelab.io/v1alpha1
kind: DDNSRecord
metadata:
  name: www
  namespace: ddns-updater
spec:
  provider: cloudflare
  domain: example.com
  host: www
  ipVersion: ipv4
  config:
    zone_identifier: 0123456789abcdef0123456789abcdef
    proxied: true
    ttl: 300
  configFrom:
    - name: token                 # the key ddns-updater reads
      secretKeyRef:
        name: cloudflare-credentials
        key: CF_API_TOKEN         # the key in the Secret
```

which assembles into:

```json
{
  "settings": [
    {
      "domain": "example.com",
      "host": "www",
      "ip_version": "ipv4",
      "provider": "cloudflare",
      "proxied": true,
      "token": "...",
      "ttl": 300,
      "zone_identifier": "0123456789abcdef0123456789abcdef"
    }
  ]
}
```

### OVH (API mode)

```yaml
spec:
  provider: ovh
  domain: example.com
  host: home
  ipVersion: ipv4_and_ipv6
  ipv6Suffix: "::166/64"
  config:
    mode: api
  configFrom:
    - name: app_key
      secretKeyRef: { name: ovh-credentials, key: OVH_APPLICATION_KEY }
    - name: app_secret
      secretKeyRef: { name: ovh-credentials, key: OVH_APPLICATION_SECRET }
    - name: consumer_key
      secretKeyRef: { name: ovh-credentials, key: OVH_CONSUMER_KEY }
```

### DuckDNS

```yaml
spec:
  provider: duckdns
  domain: example.duckdns.org
  host: "@"
  configFrom:
    - name: token
      secretKeyRef: { name: duckdns-credentials, key: DUCKDNS_TOKEN }
```

More in [`config/samples/ddnsrecords.yaml`](config/samples/ddnsrecords.yaml).

```
$ kubectl get ddns
NAME   PROVIDER     DOMAIN        HOST   READY   AGE
home   ovh          example.com   home   true    3m
www    cloudflare   example.com   www    true    3m
```

### Spec reference

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `provider` | yes | — | Any ddns-updater provider name. Passed through verbatim; not restricted to a built-in list. |
| `domain` | yes | — | Base domain, e.g. `example.com`. |
| `host` | yes | — | Subdomain, or `@` for the apex. |
| `ipVersion` | no | `ipv4` | One of `ipv4`, `ipv6`, `ipv4_or_ipv6`, `ipv4_and_ipv6`. |
| `ipv6Suffix` | no | — | Interface identifier to substitute into the detected /64, e.g. `::166/64`. Emitted only on entries that can carry an IPv6 address. |
| `config` | no | — | Provider settings as a map. Values may be strings, booleans or numbers. **Not for credentials** — this ends up in a ConfigMap. |
| `configFrom` | no | — | Provider settings sourced from Secrets: `name` is the setting, `secretKeyRef` (`name`, `key`, optional `namespace`) is where the value comes from. |

`provider`, `domain`, `host`, `ip_version` and `ipv6_suffix` are owned by the
spec fields above; setting them through `config`/`configFrom` is rejected, as is
setting the same key in both.

`ipVersion: ipv4_and_ipv6` emits **two** ddns-updater entries (one per family),
since ddns-updater cannot express both in a single setting. `ipv4_or_ipv6` is
emitted as the `ipv4 or ipv6` spelling upstream expects.

An unrecognised provider name is not rejected — ddns-updater is the authority on
which providers exist. The operator emits an `UnknownProvider` warning Event so
a typo is visible without blocking a provider it has not heard of yet.

Status carries `ready`, `lastSyncedAt`, `observedGeneration` and `conditions`.
The controller also emits Events: `Synced` on success, `AssemblyFailed` and
`ConfigUpdateFailed` on failure.

### IPv6 prefix auto-detection

Many residential ISPs rotate the delegated IPv6 /64 while the interface suffix
of each host stays fixed. With `--ipv6-enabled`, the operator runs a periodic
syncer that:

1. collects targets — Helm-provided overrides first, then every `DDNSRecord`
   carrying the annotation `ddns.homelab.io/ipv6-configmap-key: <KEY>`
   (overrides win on key collision);
2. resolves each target's AAAA record through **external** resolvers
   (`1.1.1.1:53`, `9.9.9.9:53` by default) so a split-horizon CoreDNS does not
   return the internal answer;
3. splices the current /64 prefix onto the record's `ipv6Suffix` and writes
   `<KEY>` and `<KEY>_SUFFIX` into the target ConfigMap, merging rather than
   replacing so unrelated keys survive;
4. on an actual change, annotates the Flux `Kustomization`s in that namespace
   with `reconcile.fluxcd.io/requestedAt` to trigger an immediate rollout.

```yaml
metadata:
  annotations:
    ddns.homelab.io/ipv6-configmap-key: GATEWAY_IPV6
```

yields, in `flux-system/cluster-config`:

```
GATEWAY_IPV6:        2a01:cb04:6a8:5100::166
GATEWAY_IPV6_SUFFIX: ::166/64
```

Overrides can be set entirely from Helm values, without any annotated record:

```yaml
config:
  ipv6:
    enabled: true
    overrides:
      GATEWAY_IPV6:
        domain: home.example.com
        suffix: "::166/64"
```

Enabling IPv6 in the chart also adds the `kustomize.toolkit.fluxcd.io`
get/list/patch rules to the ClusterRole.

## Configuration

Manager flags (each has a matching `config.*` Helm value):

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

Health endpoints are `/healthz` and `/readyz`; Prometheus metrics (the standard
controller-runtime set) are served on the metrics port.

## Upgrading from the OVH-only spec

Releases before generic provider support had an OVH-shaped
`spec.providerConfig` with a `credentialsRef` pointing at a Secret whose
`OVH_APPLICATION_KEY` / `OVH_APPLICATION_SECRET` / `OVH_CONSUMER_KEY` keys were
read implicitly. That field is **gone**; rewrite each record before upgrading.
The Secret itself does not change.

```yaml
# before
spec:
  provider: ovh
  domain: example.com
  host: home
  providerConfig:
    mode: api
    credentialsRef:
      name: ovh-credentials

# after
spec:
  provider: ovh
  domain: example.com
  host: home
  config:
    mode: api
  configFrom:
    - name: app_key
      secretKeyRef: { name: ovh-credentials, key: OVH_APPLICATION_KEY }
    - name: app_secret
      secretKeyRef: { name: ovh-credentials, key: OVH_APPLICATION_SECRET }
    - name: consumer_key
      secretKeyRef: { name: ovh-credentials, key: OVH_CONSUMER_KEY }
```

`providerConfig` is no longer in the schema, so applying an old-shaped record
fails with `unknown field "spec.providerConfig"` under the strict field
validation `kubectl apply` and Flux use by default. If validation is disabled,
the field is pruned instead and the record assembles into an entry with no
credentials — which ddns-updater then rejects at startup.

## Development

Requires Go (see `go.mod`) and Docker for image builds. Tooling
(`controller-gen`, `setup-envtest`) is installed into `bin/` on demand.

```sh
make help              # list all targets
make build             # build bin/manager
make run               # run the controller against your current kubecontext
make test              # unit + envtest integration tests, writes cover.out
make test-unit         # ./api/... ./pkg/... only, no envtest needed
make test-integration  # ./controllers with ginkgo verbose output
make test-coverage     # HTML report at coverage.html
make manifests generate  # regenerate CRDs, RBAC and deepcopy code
make docker-build docker-push IMG=...
```

After `make manifests`, copy the regenerated CRD into the chart:

```sh
cp config/crd/bases/*.yaml chart/ddns-updater-operator/crds/
```

Layout:

```
api/v1alpha1/      CRD types + generated deepcopy
controllers/       DDNSRecord reconciler (+ envtest suite)
pkg/assembler/     DDNSRecord list -> ddns-updater config.json
pkg/config/        operator config struct and validation
pkg/errors/        transient / permanent / config error classification
pkg/ipv6/          AAAA-based prefix syncer (manager.Runnable)
chart/             Helm chart
config/            CRD bases, generated RBAC, sample records
```

Errors are classified so the reconciler can decide between retrying
(`RequeueAfter`) and failing permanently — see `pkg/errors`.

`pkg/assembler/providers.go` holds an advisory list of upstream provider names,
used only for the `UnknownProvider` warning. It is never consulted when building
the config, so it going stale cannot break a record. Refresh it from a
ddns-updater checkout with:

```sh
grep -oE 'models\.Provider = "[a-z0-9._-]+"' \
  internal/provider/constants/providers.go | sed 's/.*"\(.*\)"/\1/'
```

## CI

- **PRs** — build, `golangci-lint`, `gofmt` check, `go vet`, `gosec`,
  `govulncheck`, unit tests with a coverage comment, and `go-licenses` on
  `go.mod` changes. Test failures fail the job.
- **main** — full test suite with envtest, then a `linux/amd64` image push to
  GHCR and a Trivy scan uploaded to the Security tab.
- **tags (`v*`)** — tests, image build/push, GitHub release, an automatic
  `Chart.yaml` version bump, and a dispatch to update the published Helm chart.

Releases are cut by pushing a `v*` tag; never force-push one — cut the next
patch version instead.
