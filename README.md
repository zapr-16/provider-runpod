# provider-runpod

provider-runpod is a Crossplane provider for RunPod GPU cloud.

## Prerequisites

Crossplane must be installed in the cluster before installing this provider: https://docs.crossplane.io/latest/software/install/

## Install

The provider ships as two OCI artifacts: a controller image and a
Crossplane package (`.xpkg`) that references it. Install the package:

```bash
crossplane xpkg install provider ghcr.io/zapr-16/provider-runpod:v0.1.0-pkg
```

(The `-pkg` suffix distinguishes the package from the raw controller
image at the same tag without it.)

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

Create a `ProviderConfig` that references the secret:

```yaml
apiVersion: runpod.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    secretRef:
      namespace: crossplane-system
      name: runpod-api-key
      key: apiKey
```

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
    imageName: runpod/worker-v1-vllm:stable
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

## Development

```bash
make generate
make build
make test
RUNPOD_API_KEY=<your-key> go test -v ./tests/e2e/...
```
