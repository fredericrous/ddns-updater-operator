# Development

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

`make test` needs the envtest control-plane binaries. If a run fails with
`fork/exec /usr/local/kubebuilder/bin/etcd: no such file or directory`, the
assets are missing from the environment rather than the tests being broken —
`make test` downloads them into `bin/`, and other entry points need
`KUBEBUILDER_ASSETS` pointed at that directory.

After `make manifests`, copy the regenerated CRD into the chart:

```sh
cp config/crd/bases/*.yaml chart/ddns-updater-operator/crds/
```

## Layout

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
(`RequeueAfter`) and failing permanently — see `pkg/errors`. A missing Secret is
transient (it may appear); a missing key inside one is a config error.

## Determinism

Entries are built as open maps and marshalled with `encoding/json`, which sorts
map keys — so the same records always render the same bytes, and the config
hash only moves on a real change. `TestAssembler_DeterministicOutput` guards
this; breaking it would make the controller rewrite the ConfigMap on every
reconcile.

## Refreshing the provider list

`pkg/assembler/providers.go` holds an advisory list of upstream provider names,
used only for the `UnknownProvider` warning Event. It is never consulted when
building the config, so a stale list cannot break a record. Refresh it from a
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

Run the same gates locally before pushing:

```sh
gofmt -l . && go vet ./... && golangci-lint run && gosec ./... && make test
```
