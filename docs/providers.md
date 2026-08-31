# Providers

ddns-updater's `config.json` is a list of **open objects**: each entry carries a
few common fields and, alongside them, whatever settings the provider itself
unmarshals from that same object.

```json
{
  "settings": [
    {
      "provider": "cloudflare",     // common
      "domain": "example.com",      // common
      "host": "www",                // common
      "ip_version": "ipv4",         // common
      "token": "...",               // cloudflare's own
      "zone_identifier": "...",     // cloudflare's own
      "proxied": true               // cloudflare's own
    }
  ]
}
```

The operator mirrors that shape rather than enumerating providers. The common
fields come from named spec fields; everything else is passed through verbatim
from `spec.config` and `spec.configFrom`. That is why any provider ddns-updater
supports works here, including one added upstream after this operator's last
release.

Which settings a provider expects is documented upstream, one file per provider
under [`docs/`](https://github.com/qdm12/ddns-updater/tree/master/docs) — the
key names there are exactly the key names used here.

## Spec reference

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `provider` | yes | — | Any ddns-updater provider name. Passed through verbatim; not restricted to a built-in list. |
| `domain` | yes | — | Base domain, e.g. `example.com`. |
| `host` | yes | — | Subdomain, or `@` for the apex. |
| `ipVersion` | no | `ipv4` | One of `ipv4`, `ipv6`, `ipv4_or_ipv6`, `ipv4_and_ipv6`. |
| `ipv6Suffix` | no | — | Interface identifier to substitute into the detected /64, e.g. `::166/64`. Emitted only on entries that can carry an IPv6 address. |
| `config` | no | — | Provider settings as a map. Values may be strings, booleans or numbers. **Not for credentials** — this ends up in a ConfigMap. |
| `configFrom` | no | — | Provider settings sourced from Secrets: `name` is the setting, `secretKeyRef` (`name`, `key`, optional `namespace`) is where the value comes from. |

`secretKeyRef.namespace` defaults to the DDNSRecord's own namespace. Secrets are
fetched once per reconcile and cached, so records sharing credentials cost one
lookup.

### Reserved keys

`provider`, `domain`, `host`, `ip_version` and `ipv6_suffix` are owned by the
spec fields above. Setting them through `config` or `configFrom` is rejected, as
is setting the same key in both — the record fails to assemble with a config
error rather than silently producing an entry you did not intend.

### IP versions

`ipVersion: ipv4_and_ipv6` emits **two** ddns-updater entries, one per family,
because ddns-updater cannot express both in a single setting. The provider
settings are copied onto both.

`ipv4_or_ipv6` is emitted as `"ipv4 or ipv6"`, the spelling upstream's
`ipversion.Parse` accepts.

### Unknown providers

A provider name the operator does not recognise is **not** rejected —
ddns-updater is the authority on which providers exist. The operator emits an
`UnknownProvider` warning Event so a typo (`cloudlfare`) is visible without
blocking a provider it has not heard of yet.

The advisory list lives in `pkg/assembler/providers.go` and is never consulted
when building the config, so it going stale cannot break a record. See
[Development](development.md#refreshing-the-provider-list) for how to refresh it.

## Examples

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
    - name: token
      secretKeyRef:
        name: cloudflare-credentials
        key: CF_API_TOKEN
```

assembles into:

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

More in [`config/samples/ddnsrecords.yaml`](../config/samples/ddnsrecords.yaml).

## Status and events

Status carries `ready`, `lastSyncedAt`, `observedGeneration` and `conditions`.
The controller emits Events: `Synced` on success, `AssemblyFailed` and
`ConfigUpdateFailed` on failure, `UnknownProvider` for an unrecognised name.
