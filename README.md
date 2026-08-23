# provider-runpod

provider-runpod is a Crossplane provider for RunPod GPU cloud.

## Prerequisites

Crossplane **v2.0 or later** must be installed in the cluster before
installing this provider: https://docs.crossplane.io/latest/software/install/
`Pod` and `Endpoint` are namespaced managed resources (Crossplane v2's
namespaced-MR model), so they will not install on a v1.x Crossplane.

> **Breaking change (v0.4.0):** this provider migrated to crossplane-runtime
> v2. `Pod` and `Endpoint` are now namespace-scoped (previously
> cluster-scoped) with namespace-local `writeConnectionSecretToRef` (no
> `namespace` field). `ProviderConfig` is now namespace-scoped; a new
> cluster-scoped `ClusterProviderConfig` kind is available for credentials
> shared across namespaces and is the default `providerConfigRef` kind when
> unset. Existing `v0.3.x` manifests must be migrated: re-create
> `ProviderConfig` as `ClusterProviderConfig` (or add a namespaced
> `ProviderConfig` per namespace), drop `namespace` from any
> `writeConnectionSecretToRef`, and re-apply `Pod`/`Endpoint` manifests into
> a namespace.

## Install

The provider ships as two OCI artifacts: a controller image and a
Crossplane package (`.xpkg`) that references it. Install the package:

```bash
crossplane xpkg install provider ghcr.io/zapr-16/provider-runpod:v0.3.0-pkg
```

(The `-pkg` suffix distinguishes the package from the raw controller
image at the same tag without it.)

> **Note on kind names:** this provider's `Pod` and `Endpoint` kinds collide
> with the core Kubernetes kinds, so plain `kubectl get pods` resolves to core
> Pods. Use the fully qualified names instead:
> `kubectl get pods.runpod.crossplane.io` and
> `kubectl get endpoints.runpod.crossplane.io`.

## Configure

Create a Kubernetes Secret with your RunPod API key:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: runpod-api-key
  namespace: crossplane-system
type: Opaque
stringData:
  apiKey: <your-runpod-api-key>
```

Create a `ClusterProviderConfig` that references the secret. Being
cluster-scoped, it can be referenced by `Pod`/`Endpoint` resources in any
namespace, and is the default `providerConfigRef` kind when unset:

```yaml
apiVersion: runpod.crossplane.io/v1beta1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  credentials:
    secretRef:
      namespace: crossplane-system
      name: runpod-api-key
      key: apiKey
```

A namespace-scoped `ProviderConfig` (same `spec` shape) is also available
for credentials that should only be usable from a single namespace; set
`spec.providerConfigRef: {name: default, kind: ProviderConfig}` on the
`Pod`/`Endpoint` to use it.

## Create a Pod

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: Pod
metadata:
  name: example-pod
  namespace: default
spec:
  providerConfigRef:
    name: default
  forProvider:
    imageName: runpod/base:0.4.4
    gpuTypeIds:
      - NVIDIA A100-SXM4-80GB
    gpuCount: 1
    cloudType: SECURE
    ports:
      - number: 8888
        protocol: http
```

## Create a serverless Endpoint

Serverless endpoints run autoscaled workers with scale-to-zero, billed
per active second. The controller implicitly creates and owns the
backing RunPod template. See `examples/endpoint-vllm.yaml` for a full
example and `docs/serverless-endpoints.md` for the design.

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: Endpoint
metadata:
  name: vllm-small
  namespace: default
spec:
  providerConfigRef:
    name: default
  forProvider:
    imageName: runpod/worker-v1-vllm:v2.25.2
    env:
      - name: MODEL_NAME
        value: "Qwen/Qwen2.5-Coder-7B-Instruct"
    containerDiskInGb: 30
    gpuTypeIds:
      - NVIDIA GeForce RTX 3090
    workersMin: 0
    workersMax: 2
    idleTimeout: 60
    flashBoot: true
```

Note: unlike pod proxy URLs, the serverless data plane
(`status.atProvider.runtimeEndpoint`) requires an
`Authorization: Bearer <RunPod API key>` header on every request.

## Docs

- `docs/local-testing.md` — local kind/Crossplane smoke harness
- `docs/serverless-endpoints.md` — serverless Endpoint design + field notes
- `docs/pod-crd-design.md` — Pod CRD field mapping design
- `docs/runpod-api-reference.md` — RunPod REST API reference notes

## Development

```bash
make generate
make build
make test
RUNPOD_API_KEY=<your-key> go test -v ./tests/e2e/...
```
