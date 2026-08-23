# Plan: Managed-resource scope vs. crossplane-runtime version (review issue #8)

## Problem

`Pod` and `Endpoint` are declared `scope=Namespaced`, but the provider is built
on crossplane-runtime **v1.20**, whose managed-resource machinery assumes the
Crossplane v1 convention of **cluster-scoped** MRs:

1. **Connection-secret garbage collection.** The v1.20 secret publisher sets an
   ownerReference from the connection Secret to the MR. Kubernetes treats a
   cross-namespace ownerRef as absent, so a Secret written to a different
   namespace than the MR gets garbage-collected (or is never usable). Only
   same-namespace `writeConnectionSecretToRef` works reliably today.
2. **Ecosystem expectations.** Crossplane v1.x composition and the package
   manager expect providers to ship cluster-scoped MRs; Crossplane v2
   introduced first-class namespaced MRs together with crossplane-runtime v2
   APIs (`LocalSecretReference`, namespaced ProviderConfig resolution, etc.).

The current combination works for the common case (secret in the MR's own
namespace) but is an off-spec hybrid either way. A decision is needed; the two
consistent end-states are below.

## Option A — Stay namespaced, adopt Crossplane v2 / crossplane-runtime v2 (RECOMMENDED)

Namespaced MRs are the direction Crossplane itself is moving (v2 makes them the
default authoring model), and this provider already ships namespaced kinds with
e2e tests built around them. Moving *backwards* to cluster scope would be a
breaking change now and likely reversed later.

Steps:
1. Bump `github.com/crossplane/crossplane-runtime` to v2.x and Go deps to match
   (controller-runtime per the runtime's go.mod).
2. Migrate `PodSpec`/`EndpointSpec` from `xpv1.ResourceSpec` to the v2
   namespaced-MR spec type; connection secret refs become namespace-local
   (`LocalSecretReference` — name only, always the MR's namespace), which fixes
   the GC problem structurally.
3. Regenerate CRDs; drop the now-invalid `namespace` field from
   `writeConnectionSecretToRef` in examples and docs.
4. Raise the package requirement in `package/crossplane.yaml`:
   `spec.crossplane.version: ">=v2.0.0-0"`.
5. Re-run e2e (`RUNPOD_API_KEY=... go test ./tests/e2e/...`) against a
   Crossplane v2 cluster (`hack/local-crossplane-up.sh` needs its Helm chart
   version bumped).
6. Release as v0.4.0 with a breaking-change note (CRs must be re-applied
   without the secret namespace field; cluster must run Crossplane v2).

Cost: a focused migration PR; the controllers themselves barely change (the
Observe/Create/Update/Delete bodies are scope-agnostic).

## Option B — Flip MRs to cluster scope, stay on runtime v1.20

Matches the runtime actually in use today with minimal dependency churn:
change the kubebuilder markers to `scope=Cluster`, regenerate CRDs, keep
namespace-qualified `writeConnectionSecretToRef` (any namespace works with a
cluster-scoped owner).

Cost: breaking change for every existing namespaced CR *and* it abandons
multi-tenancy namespacing, only to be re-reversed when the ecosystem lands on
v2. Choose this only if staying compatible with Crossplane v1.x clusters is a
hard requirement.

## Interim guard (ship before either migration)

Until Option A lands, reject cross-namespace connection secret refs at
reconcile time: in `Connect()` (or early `Observe()`), if
`spec.writeConnectionSecretToRef.namespace` is set and differs from
`metadata.namespace`, return an error explaining the limitation. This turns a
silent secret-GC footgun into an explicit, actionable condition. Document the
same-namespace requirement in the README.

## Decision to make

- **Required Crossplane baseline:** may this provider require Crossplane v2
  clusters (Option A), or must it keep supporting v1.x (Option B)?

Recommendation: **Option A** — the provider is pre-1.0 with few consumers;
breaking now toward the ecosystem's target state is the cheapest it will
ever be.
