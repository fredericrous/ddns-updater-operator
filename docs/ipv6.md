# IPv6 prefix auto-detection

Many residential ISPs rotate the delegated IPv6 /64 while the interface suffix
of each host stays fixed. The address a service should advertise is therefore
"whatever the current prefix is, plus my fixed suffix" — which nothing in
Kubernetes knows on its own.

With `--ipv6-enabled`, the operator runs a periodic syncer (registered as a
`manager.Runnable`, so it starts after the informer cache is warm) that:

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

A resolution failure for one target is logged and skipped — the others still
sync. If nothing resolves, the ConfigMap is left alone rather than emptied.

## Driving it from a record

```yaml
apiVersion: connectivity.homelab.io/v1alpha1
kind: DDNSRecord
metadata:
  name: gateway
  annotations:
    ddns.homelab.io/ipv6-configmap-key: GATEWAY_IPV6
spec:
  provider: ovh
  domain: example.com
  host: home
  ipVersion: ipv4_and_ipv6
  ipv6Suffix: "::166/64"
  # ... provider settings
```

yields, in `flux-system/cluster-config`:

```
GATEWAY_IPV6:        2a01:cb04:6a8:5100::166
GATEWAY_IPV6_SUFFIX: ::166/64
```

Anything that substitutes from that ConfigMap — a Flux `postBuild`, a MetalLB
pool, a Gateway's `loadBalancerIPs` — then follows the prefix automatically.

## Driving it from Helm alone

Overrides need no `DDNSRecord` at all, which is useful for a host that is not
managed by this operator:

```yaml
config:
  ipv6:
    enabled: true
    configMapName: cluster-config
    configMapNamespace: flux-system
    syncInterval: 5m
    resolvers: "1.1.1.1:53,9.9.9.9:53"
    overrides:
      GATEWAY_IPV6:
        domain: home.example.com
        suffix: "::166/64"
```

Enabling IPv6 also adds the `kustomize.toolkit.fluxcd.io` get/list/patch rules
to the ClusterRole, for step 4 above.

## Suffix format

`ipv6Suffix` accepts `::166/64`, `0:0:0:0:0:0:0:166/64` or a bare `::166`. The
prefix length is stripped; the low 64 bits of the parsed suffix are combined
with the high 64 bits of the resolved address. An empty suffix means the
resolved address is used as-is.
