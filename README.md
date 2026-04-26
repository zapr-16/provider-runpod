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

## Development

```bash
make generate
make build
make test
RUNPOD_API_KEY=<your-key> go test -v ./tests/e2e/...
```
