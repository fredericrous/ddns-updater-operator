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

- **API group:** `connectivity.homelab.io/v1alpha1`
- **Kind:** `DDNSRecord` (short name `ddns`)
- **Image:** `ghcr.io/fredericrous/ddns-updater-operator`

## Install

```sh
helm repo add fredericrous https://fredericrous.github.io/charts
helm repo update
helm install ddns-updater-operator fredericrous/ddns-updater-operator \
  --namespace ddns-updater --create-namespace
```

The chart installs the CRD, a ClusterRole/Binding, a ServiceAccount and the
manager Deployment. A copy of the chart also lives in `chart/` if you prefer to
install from source; `make install` applies just the CRD.

## A record

```yaml
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
  config:                       # plain provider settings, any JSON type
    zone_identifier: 0123456789abcdef0123456789abcdef
    proxied: true
    ttl: 300
  configFrom:                   # settings whose value comes from a Secret
    - name: token
      secretKeyRef:
        name: cloudflare-credentials
        key: CF_API_TOKEN
```

```
$ kubectl get ddns
NAME   PROVIDER     DOMAIN        HOST   READY   AGE
www    cloudflare   example.com   www    true    3m
```

The key names under `config` / `configFrom` are exactly the ones the provider
documents in ddns-updater's
[`docs/`](https://github.com/qdm12/ddns-updater/tree/master/docs) directory.

## Documentation

| | |
| --- | --- |
| [Providers](docs/providers.md) | The passthrough model, full spec reference, OVH / Cloudflare / DuckDNS examples |
| [Configuration](docs/configuration.md) | Manager flags, Helm values, metrics and probes |
| [IPv6 prefix sync](docs/ipv6.md) | Tracking a rotating ISP /64 into a ConfigMap |
| [Development](docs/development.md) | Build, test, regenerate manifests, repo layout, CI |

## How it works

```
DDNSRecord (x N)  ─┐
                   ├─► Assembler ─► ConfigMap ddns-updater/ddns-updater-config
Secret (API keys) ─┘                  └─ config.json  ─► mounted by ddns-updater
```

Every reconcile lists *all* `DDNSRecord`s and rebuilds the whole config, sorted
by domain then host so the output is deterministic. The ConfigMap is only
written when the SHA-256 of the rendered JSON differs from the
`ddns.homelab.io/config-hash` annotation already on it, and status writes are
skipped once `status.observedGeneration` matches `metadata.generation` — so a
steady-state cluster produces no churn.

An optional [IPv6 syncer](docs/ipv6.md) handles dynamic-prefix ISPs.

> **Note on secrets:** the assembled `config.json` embeds provider credentials
> in plaintext, because that is the format ddns-updater expects. The target
> ConfigMap is therefore as sensitive as the source Secret — keep it in a
> namespace with matching RBAC.
