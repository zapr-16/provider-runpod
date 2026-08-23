# Plan: PR #2 follow-up review fixes

Two independent workstreams with disjoint file sets, executable in parallel.

## Workstream A — core code (apis/, internal/, package/crds via generate)

### A1. Namespaced ProviderConfig tenancy (Important #2) — structural fix
The namespaced `ProviderConfig`'s `credentials.secretRef` currently requires a
`namespace` field, letting a namespace-scoped tenant point at any secret in
the cluster (e.g. `crossplane-system/other-teams-runpod-key`) and spend on it,
because the provider holds cluster-wide secret read.

Fix structurally (v0.4.0 is already breaking; the namespaced kind is new in
this PR, so nobody depends on the current shape):
- Give the namespaced `ProviderConfig` its own spec whose secret ref has NO
  namespace field (name + key only), e.g. a `LocalProviderConfigSpec` /
  `LocalSecretKeySelector` pair in `apis/v1beta1`. `ClusterProviderConfig`
  keeps the current namespaced selector (cluster admins may centralize keys).
- Resolution: the connector path (`internal/clients/providerconfigref.go`) and
  the credential reconciler resolve the namespaced config's secret in the
  config's OWN namespace — build the extraction selector with
  `namespace = pc.Namespace` internally.
- Check upstream first: if crossplane/provider-template or crossplane
  apis/v2 already ship a local (namespace-less) credential selector type,
  reuse it instead of defining our own.
- `make generate`; regenerated CRDs must show the namespaced kind without
  `secretRef.namespace`.
- Tests (RED first): namespaced-PC connector test asserts the secret is read
  from the PC's namespace (secret with same name in another namespace must
  NOT be used); reconciler tests updated for the new spec shape.

### A2. Meaningful credential validation (Minor)
`validateCredentials` only checks the secret exists and is non-empty, so a
revoked key still shows Available. Add a cheap authenticated call to the
client (e.g. `Ping(ctx)` doing `GET /pods` and treating any 2xx as valid,
401/403 as invalid-credentials error, other statuses as transient errors) and
call it from `validateCredentials` on the 5-minute requeue. Unit tests via
httptest: valid → Available, 401 → Unavailable + error. The connectors must
NOT gain an extra API call — this is only for the config reconcilers.
NOTE: the reconciler currently constructs clients via `ClientFromCredentials`
with the default base URL; to keep tests hermetic, thread a base-URL override
into the reconciler (e.g. an optional field on Reconciler used only by tests).

### A3. Deduplicate Reconciler/ClusterReconciler (Minor)
~90 duplicated lines; extract a shared generic or interface-based helper so
both Reconcile methods delegate to one implementation parameterized by object
type + credential/status accessors. No behavior change; existing 15+ tests
are the guard.

### A4. Skip unchanged status writes (Minor)
The reconcilers currently write status every 5 minutes even when nothing
changed, causing benign conflict churn. Compare the Ready condition (ignoring
LastTransitionTime, which SetConditions already preserves for equal states)
before `Status().Update` and skip the write when unchanged. Test: second
reconcile with same outcome performs no status update (assert via interceptor
counting SubResourceUpdate calls).

### Workstream A rules
- TDD; all existing tests keep passing; gate after each task:
  `go build ./... && go vet ./... && go test -race ./... && make generate && make lint`.
- Files owned: `apis/v1beta1/**`, `internal/**`, `package/crds/**` (generated
  only). Do NOT touch README.md, Makefile, package/crossplane.yaml,
  opencode.json, hack/**, examples/** (workstream B owns those), except: if
  A1's shape change invalidates a YAML snippet in an example, report it in
  your final message instead of editing.

## Workstream B — docs, versions, harness (README, Makefile, package/crossplane.yaml, opencode.json, hack/)

### B1. README quickstart fails admission (Important #1)
`README.md` still shows `providerConfigRef: {name: default}` without `kind`
(~lines 88-89, 115-116; search the whole file for `providerConfigRef`). Add
`kind: ClusterProviderConfig` everywhere, matching examples/*.yaml.

### B2. Version alignment for the breaking release (Important #3)
README announces v0.4.0 but the install command, `Makefile` (`XPKG_TAG`), and
`package/crossplane.yaml` controller image tag still say v0.3.0 — and v0.3.0
was already released pre-migration, so shipping this under it would be wrong.
Set ALL FOUR to `v0.4.0` (README announce + README install command +
Makefile + crossplane.yaml).

### B3. opencode.json personal endpoint ID (Minor)
`opencode.json` commits a personal live endpoint ID in `baseURL`. Replace the
ID with env templating if opencode supports mid-string substitution (check
https://opencode.ai/docs/config briefly); otherwise use a clearly-fake
placeholder like `<your-endpoint-id>` and add a short note to README's
example section on how to fill it. Do not delete the file.

### B4. In-use protection smoke test (Minor)
Deletion-blocking is only unit-tested up to usage creation. Add
`hack/local-crossplane-endpoint-smoke.sh` mirroring the existing pod smoke
script's conventions (env guards, set -euo pipefail, stdin-fed secret,
cleanup trap) that, against the already-provisioned kind cluster:
1. applies the API-key secret + `examples/providerconfig.yaml`
2. applies `examples/endpoint-vllm.yaml` (workersMin=0 → free) and waits for
   Ready + the `vllm-small-conn` secret in the Endpoint's namespace
3. deletes the ClusterProviderConfig with `--wait=false` and asserts it is
   HELD: deletionTimestamp set, finalizer `in-use.crossplane.io` present,
   `status.users == 1` after a short wait
4. deletes the Endpoint, waits for it to go, then asserts the
   ClusterProviderConfig deletion completes and the endpoint/template return
   404 from the RunPod REST API
5. exits non-zero on any assertion failure; skips cleanly (exit 0) without
   RUNPOD_API_KEY
Also mention the new script in docs/local-testing.md (one short paragraph).

### Workstream B rules
- Files owned: `README.md`, `Makefile`, `package/crossplane.yaml`,
  `opencode.json`, `hack/**`, `docs/local-testing.md`. Nothing else.
- Do NOT run the new smoke script (workstream A is changing the code under
  it); shellcheck-level care only — the maintainer runs it after both
  workstreams land.

## Shared rules
- Git READ-ONLY: no commit/stash/reset/checkout; the maintainer commits.
- Report deviations; don't invent API shapes — verify against sources.
