# Local Crossplane Testing

This repository includes a local Crossplane smoke harness built around `kind`.

## Prerequisites

- `kind`
- `kubectl`
- `docker`
- `helm`
- `make`
- `RUNPOD_API_KEY` set in your shell for the live smoke test

## What the scripts do

- `hack/local-crossplane-up.sh`
  Creates a `kind` cluster if needed, installs Crossplane with Helm, builds the provider binary, builds and loads a local image, applies the CRDs, and deploys the provider controller into `crossplane-system`.
- `hack/local-crossplane-smoke.sh`
  Creates the RunPod credential secret, applies `examples/providerconfig.yaml` (a cluster-scoped `ClusterProviderConfig` named `default`), renders a sample namespaced `Pod` (in the `default` namespace, with `spec.providerConfigRef: {kind: ClusterProviderConfig, name: default}`) using your chosen GPU type, waits for Crossplane to create and observe the external RunPod pod via `status.atProvider.networkingReady`, then deletes the sample resources on exit unless `KEEP_RESOURCES=1`.
- `hack/local-crossplane-down.sh`
  Deletes managed `Pod` resources to trigger remote cleanup, removes the local provider deployment, and deletes the `kind` cluster unless `DELETE_CLUSTER=0`.
- `hack/local-crossplane-endpoint-smoke.sh`
  Exercises in-use protection on an already-provisioned cluster: applies the RunPod credential secret, `examples/providerconfig.yaml`, and `examples/endpoint-vllm.yaml` (a scale-to-zero `Endpoint`, so it's free once idle), waits for it to become Ready and for the `vllm-small-conn` connection secret, then deletes the `ClusterProviderConfig` with `--wait=false` and asserts it is HELD (a `deletionTimestamp`, the `in-use.crossplane.io` finalizer, and `status.users == 1`). It then deletes the `Endpoint`, confirms the held `ClusterProviderConfig` deletion completes once the `Endpoint` is gone, and confirms the endpoint and its implicitly managed template both 404 from RunPod's REST API. Like the pod smoke script, it skips cleanly (exit 0) without `RUNPOD_API_KEY`.

## Usage

Bring up the local Crossplane environment:

```bash
hack/local-crossplane-up.sh
```

Run the smoke test:

```bash
RUNPOD_API_KEY=<your-key> \
hack/local-crossplane-smoke.sh
```

Recommended single-candidate override:

```bash
RUNPOD_API_KEY=<your-key> \
RUNPOD_CLOUD_TYPE=SECURE \
RUNPOD_GPU_TYPE_ID='NVIDIA RTX A4000' \
RUNPOD_SUPPORT_PUBLIC_IP=false \
hack/local-crossplane-smoke.sh
```

Custom fallback matrix:

```bash
RUNPOD_API_KEY=<your-key> \
RUNPOD_SMOKE_CANDIDATES='SECURE|NVIDIA RTX A4000|false;SECURE|NVIDIA RTX A4500|false;SECURE|NVIDIA GeForce RTX 3090|false;COMMUNITY|NVIDIA RTX A4000|true' \
hack/local-crossplane-smoke.sh
```

Tear the environment down:

```bash
hack/local-crossplane-down.sh
```

## Notes

- The smoke test exits successfully without doing anything if `RUNPOD_API_KEY` is unset.
- If `RUNPOD_GPU_TYPE_ID` is unset, the smoke harness defaults to `SECURE + NVIDIA RTX A4000`.
- The default fallback order is `SECURE A4000`, `SECURE A4500`, `SECURE 3090`, `SECURE L4`, then `COMMUNITY A4000` with `supportPublicIp=true`.
- This local smoke harness only exercises the `Pod` resource; it does not create or delete `Endpoint` (serverless) resources. The provider also manages `Endpoint` — see [docs/serverless-endpoints.md](serverless-endpoints.md) — but there is currently no equivalent local smoke script for it.
- `Pod` and `Endpoint` are namespaced managed resources (Crossplane >= v2.0 required), and `ProviderConfig` is also namespaced; the smoke script uses the cluster-scoped `ClusterProviderConfig` kind instead so the sample `Pod` in the `default` namespace can reference credentials in `crossplane-system`. `spec.providerConfigRef.kind` must be set explicitly to `ClusterProviderConfig` — the CRD's default value (`{kind: ClusterProviderConfig, name: default}`) only applies when `providerConfigRef` is omitted from the manifest entirely, not when it's partially specified.
- The smoke test creates a real RunPod GPU pod and may incur cloud cost.
- The harness fast-fails to the next fallback candidate when RunPod returns the known capacity error `This machine does not have the resources to deploy your pod`.
- Set `KEEP_RESOURCES=1` when running `hack/local-crossplane-smoke.sh` to keep the `ClusterProviderConfig` and `Pod` resources for debugging.
- Set `DELETE_CLUSTER=0` when running `hack/local-crossplane-down.sh` to remove provider resources but keep the cluster.
