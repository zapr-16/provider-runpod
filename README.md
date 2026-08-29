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
crossplane xpkg install provider ghcr.io/zapr-16/provider-runpod:v0.5.0-pkg
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

### Runtime flags

The controller binary accepts the following flags (set via
`spec.controllerConfigRef`/`DeploymentRuntimeConfig` `args`, or directly on
the container command when running outside Crossplane's package manager):

| Flag | Default | Description |
| --- | --- | --- |
| `--debug` | `false` | Development zap logging (human-readable, more verbose) instead of production JSON. |
| `--poll-interval` | `1m` | How often each resource is polled for drift when no watch event has triggered a reconcile. |
| `--max-reconcile-rate` | `10` | Global maximum reconciles per second across every controller. |
| `--leader-election` | `true` | Use leader election for the controller manager. |
| `--sync-interval` | `1h` | How often the manager's watch cache resyncs against the API server. |
| `--enable-management-policies` | `true` | Enable Crossplane's beta Management Policies support. |

## Create a Pod

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: Pod
metadata:
  name: example-pod
  namespace: default
spec:
  providerConfigRef:
    kind: ClusterProviderConfig
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

Most fields (image, env, disk sizes, ports, ...) are updated in place via
`PATCH /pods/{podId}` — changes apply the next time the pod (re)starts.
Set `spec.forProvider.desiredState: EXITED` to stop the pod (storage keeps
billing while stopped, compute does not) and `RUNNING` (or leave it unset)
to start/resume it; GPU type and interruptible are immutable at the RunPod
API. Changing either after creation is surfaced as drift via
`status.atProvider.driftDetected` rather than being automatically applied
— the controller only replaces the pod if `spec.forProvider.recreateOnTerminate`
also applies.

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
    kind: ClusterProviderConfig
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

To point a tool such as [opencode](https://opencode.ai) at a provisioned
endpoint, see `opencode.json` in this repo: replace the
`<your-endpoint-id>` placeholder in `provider.runpod.options.baseURL` with
the ID from `status.atProvider.endpointId` (or the `RUNPOD_ENDPOINT_ID`
you chose), and export `RUNPOD_API_KEY` in your shell so `{env:RUNPOD_API_KEY}`
resolves.

## Create a NetworkVolume

A `NetworkVolume` is persistent network storage that can be attached to
pods or serverless endpoints for weight/model caching. Size can grow but
never shrink. See `examples/networkvolume.yaml`.

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: NetworkVolume
metadata:
  name: model-cache
  namespace: default
spec:
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
  forProvider:
    size: 50          # GB
    dataCenterId: EU-RO-1
```

## Create a ContainerRegistryAuth

A `ContainerRegistryAuth` registers private container registry credentials
with RunPod so pods/endpoints can pull private images; reference the
resulting `status.atProvider.containerRegistryAuthId` from
`spec.forProvider.containerRegistryAuthId` on a `Pod`, `Endpoint`, or
`Template`. See `examples/containerregistryauth.yaml`.

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: ContainerRegistryAuth
metadata:
  name: ghcr
  namespace: default
spec:
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
  forProvider:
    credentialsSecretRef:
      name: ghcr-creds
```

## Create a Template

A `Template` is a standalone, referenceable serverless template — useful
when several `Endpoint` resources should share one image/config, instead
of each `Endpoint` implicitly owning its own template. Reference the
resulting `status.atProvider.templateId` from an `Endpoint`'s
`spec.forProvider.templateId` (see `examples/endpoint-from-template.yaml`).
See `examples/template.yaml`.

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: Template
metadata:
  name: vllm-base
  namespace: default
spec:
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
  forProvider:
    imageName: runpod/worker-v1-vllm:v2.25.2
    isServerless: true
    env:
      - name: MODEL_NAME
        value: "Qwen/Qwen2.5-Coder-7B-Instruct"
    containerDiskInGb: 30
```

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
