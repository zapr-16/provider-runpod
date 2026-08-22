# Design: RunPod Serverless Endpoint support (v0.3.0 candidate)

Status: **implemented (2026-07-14)** — see "Implementation notes" at the bottom
for where the shipped code diverges from this plan.

## Motivation

The provider manages RunPod *Pods*: dedicated GPUs billed per second while
they exist. For intermittent inference (the gc-llm lab), that means either
paying for idle time or engineering around Spot reclaims
(`recreateOnTerminate`, Envoy health-checked failover, the network-volume
warm-cache design). RunPod *Serverless* moves all of that to RunPod's side:
autoscaled workers, scale-to-zero, pay per active second. Supporting it
would likely make the network-volume-cache design (docs/network-volume-cache.md)
unnecessary for the lab use case.

## What Serverless is, API-wise

- Data plane: `https://api.runpod.ai/v2/{endpointId}/…` — includes an
  OpenAI-compatible route (`/v2/{endpointId}/openai/v1`) when using RunPod's
  official vLLM worker (`runpod/worker-v1-vllm`, `MODEL_NAME` env).
  **Requires `Authorization: Bearer <api key>` on every request** — unlike
  pod proxy URLs, which are unauthenticated.
- Control plane (REST v1, same base URL the provider already uses):
  - `POST/GET/PATCH/DELETE /endpoints[/{id}]`
  - Endpoints reference a **template** (`/templates` CRUD) that carries the
    image, env, and disk config.
- Workers are picked by GPU *tier* (VRAM class, e.g. 24 GB → 3090/4090/L4/A5000),
  not by exact GPU type list like pods.
- FlashBoot keeps warm workers at ~2–5 s cold start; a fully cold worker
  still has to load model weights (mitigable with a network volume attached
  to the endpoint — different mechanism than the pod-volume cache doc).

## Proposed shape

One new managed resource, **`Endpoint`** (`runpod.crossplane.io/v1alpha1`).
The controller owns the backing template implicitly (option B below).

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: Endpoint
metadata:
  name: vllm-small
spec:
  providerConfigRef: { name: default }
  forProvider:
    # template (managed implicitly by the controller)
    imageName: runpod/worker-v1-vllm:stable
    env:
      - { name: MODEL_NAME, value: "Qwen/Qwen2.5-Coder-7B-Instruct" }
    containerDiskInGb: 30
    # endpoint
    gpuTierIds: ["24"]           # VRAM tier, not GPU model list
    workersMin: 0                # scale to zero
    workersMax: 2
    idleTimeoutSeconds: 60
    flashBoot: true
    scalerType: QUEUE_DELAY
    scalerValue: 4
    networkVolumeId: ""          # optional, for weight caching
status:
  atProvider:
    endpointId: abc123
    runtimeEndpoint: https://api.runpod.ai/v2/abc123/openai/v1
    templateId: tpl-xyz
    workersReady: 1
```

### Template handling: two options

- **A. Separate `Template` CRD** — clean Crossplane semantics, referenceable
  by several endpoints; more code, more objects to reconcile.
- **B. Implicit template owned by the Endpoint controller** (recommended) —
  Create makes template then endpoint; Delete removes both; template drift
  folded into Endpoint drift. Matches the lab's one-template-per-endpoint
  reality and keeps UX to a single CR.

### Controller work (mirrors the existing pod controller)

| Piece | Work |
|-------|------|
| `apis/v1alpha1/endpoint_types.go` | new types + `make generate` |
| `internal/clients/runpod.go` | `CreateTemplate/CreateEndpoint/GetEndpoint/UpdateEndpoint/DeleteEndpoint/DeleteTemplate` |
| `internal/controller/endpoint/` | connector + external (Observe/Create/**Update**/Delete) |
| Update support | unlike Pod (immutable), `workersMin/Max`, idle timeout, scaler are PATCHable — first real Update() implementation |
| Tests | httptest unit tests same pattern as pod; smoke script variant |

Estimate: ~1 day reusing the pod controller structure. Release as v0.3.0.

## gc-llm integration sketch

- Replace `gitops-gke/runpod/1x-vllm-*-pod.yaml` Pod CRs with Endpoint CRs.
- **Auth changes the routing picture**: serverless needs the API key on data
  plane calls. Either Envoy injects the `Authorization` header from a secret
  (keeps current topology), or drop Envoy for serverless entirely — RunPod
  already does LB/health/scaling, which is most of what Envoy was added for.
  Router would call `api.runpod.ai` directly with the key.
- `recreateOnTerminate`, reclaim handling, and the Envoy upstream-rotation
  CronJob (backlog item 5) all become irrelevant for serverless workloads.

## Cost sanity check (approximate)

24 GB serverless tier ≈ $0.7–1.1/hr *while active* vs 3090 Spot pod
$0.22/hr *always-on while up*. Serverless wins below roughly 20–30% duty
cycle — the lab's actual profile (bursty benchmarks, demos) is far below
that, plus $0 when nobody uses it.

## Risks / open questions

- ~~Exact REST field names for endpoint/template create need verification
  against current API docs at implementation time (RunPod moves fast).~~
  Verified against the live OpenAPI spec (`https://rest.runpod.io/v1/openapi.json`)
  on 2026-07-14 — see implementation notes below.
- Cold-start with large models when scaled to zero: acceptable for lab;
  mitigate with `workersMin: 1` (loses scale-to-zero) or network volume.
- GPU tier availability/pricing on COMMUNITY vs SECURE serverless fleets.
- Whether `/v2/{id}/openai/v1` streaming behaves identically to raw vLLM
  for the bench scripts (TTFB measurement passes through RunPod's queue).

## Implementation notes (as shipped)

Verified against the live OpenAPI spec on 2026-07-14 and exercised
end-to-end against the real RunPod API (create → observe → patch → delete,
template cleanup confirmed via 404s). The plan above is kept as written;
these are the deltas:

- **No GPU tiers.** The REST API selects serverless workers by the same GPU
  type IDs used for pods (`gpuTypeIds`, e.g. `"NVIDIA GeForce RTX 3090"`,
  ordered by rental priority) — the proposed `gpuTierIds: ["24"]` field does
  not exist. The spec field is `gpuTypeIds`.
- **Field name corrections:** `idleTimeout` (seconds, API default 5, max
  3600) instead of `idleTimeoutSeconds`; the API wire name for FlashBoot is
  lowercase `flashboot` (spec keeps `flashBoot`).
- **Template create requires `isServerless: true`** plus `name` and
  `imageName`; env is a JSON object, not a list.
- **`flashboot` IS returned by `GET /endpoints/{id}`** even though the
  OpenAPI spec omits it from the response schema, so drift on it is
  detected. `dataCenterIds` is the opposite: accepted on create/update but
  never echoed back, so it is excluded from drift detection.
- **Observation quirks** (found live, not in the spec): workers require
  `?includeWorkers=true`; the template embedded via `?includeTemplate=true`
  omits `imageName`, so Observe reads the template with a separate
  `GET /templates/{id}` (only when the endpoint itself shows no drift).
- **Update is PATCH** `/endpoints/{endpointId}` (there is also a legacy
  `POST /endpoints/{id}/update`); template drift is handled with
  `PATCH /templates/{templateId}`. Both are exercised by `Update()` — the
  first real Update implementation in the provider, as planned.
- **Option B (implicit template) shipped.** Create makes template → endpoint
  and cleans up the template if the endpoint create fails; Delete resolves
  the template ID from status (or a GET fallback), deletes the endpoint,
  then the template.
- **Status surface:** `runtimeEndpoint` is the data-plane base
  (`https://api.runpod.ai/v2/{id}`) and `openAIBaseURL` adds `/openai/v1`;
  `workersReady`/`workersTotal` are derived from the `workers` array (pods)
  in the GET response. Connection secret keys: `endpointId`, `endpoint`,
  `openaiUrl`.
- **Readiness:** the resource is Available as soon as the endpoint exists —
  zero workers is the normal scale-to-zero state.
- Extra optional spec fields exposed because the API supports them cheaply:
  `dataCenterIds`, `executionTimeoutMs`, `networkVolumeId`, `gpuCount`.
- Not exposed (YAGNI for the lab): CPU endpoints (`computeType`,
  `vcpuCount`, `cpuFlavorIds`), CUDA version pinning
  (`allowedCudaVersions`, `minCudaVersion`), multiple network volumes.

## Field notes from live agentic-client testing (2026-08-22)

Found while driving the endpoint from opencode (OpenAI-compatible, streaming,
tool definitions on every request):

- **Pin the worker image tag.** `runpod/worker-v1-vllm:stable` was removed
  from the registry; template create fails with a 500 "image not found".
  Use a released tag (e.g. `v2.25.2`).
- **Tool calling needs explicit env**: `ENABLE_AUTO_TOOL_CHOICE=true` and
  `TOOL_CALL_PARSER=hermes` (Qwen). Without them, requests carrying `tools`
  fail at the worker (`400 "auto tool choice requires..."`, surfaced as an
  opaque gateway 500 / empty stream).
- **`MAX_MODEL_LEN` must exceed the client's `max_tokens`**. opencode sends
  `max_tokens=32000` by default; with a smaller `MAX_MODEL_LEN` vLLM rejects
  the request and opencode reports an empty step (`finish: "unknown"`).
  24576 with opencode `limit: {context, output}` capped works.
  `MAX_MODEL_LEN=49152` left workers "ready" but unable to serve on the
  24 GB tier (jobs queued forever) — don't set it beyond VRAM reality.
- **Template env changes do not recycle live workers.** Running/idle workers
  keep the old env indefinitely (FlashBoot standby never hits idleTimeout).
  Recycle via `workersMax: 0` then back (exercises the provider's PATCH path).
- **Killing a streaming client can wedge the worker** (server-side generation
  continues; the worker stays "running" and new jobs queue). Recovery:
  `POST /v2/{id}/purge-queue` + worker recycle.
- **Streaming + image input hangs** at the OpenAI gateway (`/openai/v1`,
  stream=true + `image_url` content): no bytes, no error. Non-streaming
  vision (or `/runsync`) with base64 data URLs works; remote image URLs
  500. If you need vision, send non-streaming requests.
- **RBAC**: connection-secret publishing needs `create/update/patch/delete`
  on secrets — `deploy/local/rbac.yaml` was updated accordingly.
