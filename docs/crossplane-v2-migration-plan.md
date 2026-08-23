# Implementation plan: migrate to crossplane-runtime v2 (namespaced MRs) — Option A

Executes the decision in `mr-scope-decision-plan.md` (Option A). Goal state:
`Pod` and `Endpoint` remain **namespaced** managed resources, built on
**crossplane-runtime v2**, with namespace-local connection secret refs, on a
package that requires **Crossplane >= v2.0**.

## Baseline

The working tree contains uncommitted review fixes that are the baseline —
they must survive the migration semantically:
- Strict client status handling: only 404 = "not found" on GETs; only 404/410 =
  success on DELETEs (`internal/clients/`).
- Resource-ID validation before URL interpolation (`validateResourceID`).
- Pod drift surfaced via `status.atProvider.driftDetected`; Observe always
  reports up-to-date (pods are immutable).
- `recreateOnTerminate` deletes the old pod before clearing external-name.
- Proxy-probe gating of the Available condition (`probeHTTP`).
- Leader election + healthz/readyz in `cmd/provider/main.go`.
- ProviderConfig in-use protection: `ProviderConfigUsage` type, usage tracking
  in both connectors, runtime providerconfig reconciler.

## Tasks (sequential)

### 1. Dependency bump
- `go get github.com/crossplane/crossplane-runtime/v2@latest`; keep the v1
  module only if the v2 template still imports it for common types.
- Align `sigs.k8s.io/controller-runtime` and `k8s.io/*` with crossplane-runtime
  v2's own go.mod; drop the `controller-runtime v0.19.0` replace directive if
  no longer needed.
- Authoritative references (fetch and follow, do not guess API shapes):
  - https://github.com/crossplane/provider-template (main branch is the v2
    pattern; note how it defines namespaced MRs, ProviderConfig kinds, usage
    tracking, and controller setup)
  - https://docs.crossplane.io/ (v2 provider/MR migration docs)
  - The crossplane-runtime/v2 source in the module cache once fetched.

### 2. API types (`apis/`)
- Migrate `PodSpec`/`EndpointSpec` from `xpv1.ResourceSpec` to the v2
  namespaced managed-resource spec type used by provider-template; connection
  secret refs become namespace-local (name-only).
- Adopt the v2 ProviderConfig pattern exactly as the template does (v2
  providers typically ship namespaced `ProviderConfig` + cluster-scoped
  `ClusterProviderConfig`, and matching usage types). Preserve the existing
  credential shape (`spec.credentials` secret ref with `apiKey` key) so
  existing user secrets keep working.
- Replace hand-written interface methods only where v2 provides embeddings;
  keep the existing style otherwise.
- `make generate` after every type change; CRDs in `package/crds` must be
  regenerated, committed state must be drift-free.

### 3. Controllers (`internal/controller/`)
- Update `managed.NewReconciler` setup, connectors, and usage tracking to the
  v2 equivalents per the template. ProviderConfig resolution must work for
  namespaced MRs (typed providerConfigRef with kind, if that is the v2 shape).
- Keep the custom credential-validating providerconfig reconciler working
  (adapt types), and keep the runtime usage/in-use reconciler (v2 equivalent).
- The `external` implementations (Observe/Create/Update/Delete bodies) should
  need little change — preserve their semantics and comments.

### 4. Package, examples, deploy
- `package/crossplane.yaml`: `spec.crossplane.version: ">=v2.0.0-0"`.
- `examples/*.yaml`: drop `namespace` from `writeConnectionSecretToRef`;
  update providerConfigRef shape if v2 changes it.
- `deploy/local/rbac.yaml`: add rules for any new kinds (ClusterProviderConfig,
  new usage kinds). Do not broaden the existing secrets rule.
- Do NOT touch: `Dockerfile`, `tools/tools.go`, the resources block in
  `deploy/local/provider.yaml`, `.github/workflows/*` (already fixed).

### 5. Local cluster + e2e
- `hack/local-crossplane-up.sh`: bump the Crossplane Helm chart to the latest
  2.x. While in this file, fix the Sonar smells: assign positional parameters
  to named local variables (S7679 at lines 20-21).
- `tests/e2e/*`: update spec shapes so they compile; they must still skip
  cleanly without `RUNPOD_API_KEY`. Do not attempt to run a live cluster.
- Update `README.md` (Crossplane v2 requirement, secret ref example) and add a
  breaking-change note for v0.4.0.

### 6. Sonar code-smell cleanup (in files this migration already owns)
- go:S1192 — extract constants for duplicated literals: `"/templates/"`,
  `"/endpoints/"` (`internal/clients/serverless.go`), `"external-name"`
  (`internal/controller/pod/external.go`).
- godre:S8193 — `internal/controller/pod/external_test.go:497` unnecessary
  variable declaration.
- Maintainer decision: do NOT do any go:S3776 cognitive-complexity refactors —
  neither on test functions nor on production functions (`Observe` stays as
  is). List all S3776 findings in the report as accepted.

## Rules
- TDD for any behavior change; adapt existing tests to v2 shapes rather than
  deleting them. Every test that exists today must still exist (possibly
  updated) and pass.
- Verification gate after each task and at the end:
  `go build ./... && go vet ./... && go test -race ./... && make generate`
  (then `git status` must show only intended changes) `&& make lint`.
- Git: read-only. NEVER commit, stash, reset, checkout, or otherwise move
  HEAD/index — the working tree contains uncommitted baseline work.
- If the v2 API makes a task impossible as written (e.g. ProviderConfig shape
  differs), follow upstream convention and record the deviation in the report.
