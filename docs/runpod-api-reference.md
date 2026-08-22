# RunPod Pod Lifecycle API Reference

Last reviewed: 2026-04-19

Scope: this document focuses on the RunPod pod lifecycle endpoints needed for a Crossplane provider implementation: create, get/observe, and delete. It uses the published REST API as the canonical interface. GraphQL and broader pod management docs were used only to corroborate state semantics where the REST docs were thin.

## Authentication

- REST base URL: `https://rest.runpod.io/v1`
- Auth header: `Authorization`
- Auth format: `Bearer <RUNPOD_API_KEY>`
- Content type for JSON requests: `Content-Type: application/json`

Example:

```bash
curl --request GET \
  --url https://rest.runpod.io/v1/pods \
  --header 'Authorization: Bearer RUNPOD_API_KEY'
```

Notes:

- The REST API overview states that all requests require a RunPod API key in request headers.
- GraphQL documentation still shows `?api_key=...` in examples, while official SDK release notes indicate GraphQL moved to `Authorization` headers. For this task, REST is the authoritative source.

## Pod Create

- Method: `POST`
- Path: `/pods`
- Full URL: `https://rest.runpod.io/v1/pods`
- Success response: `201 Created`
- Response body: a `Pod` object using the same schema documented for `GET /pods/{podId}`

Important schema note:

- The published OpenAPI schema for `PodCreateInput` does not mark any request fields as required.
- That means every field below is formally `optional` in the published schema.
- Practical minimums for a valid deployment are not documented as schema constraints. For example, a real GPU pod still needs enough placement and image information to schedule successfully.

Request fields:

- `allowedCudaVersions`: `string[]`, optional. Allowed values in OpenAPI: `13.0`, `12.9`, `12.8`, `12.7`, `12.6`, `12.5`, `12.4`, `12.3`, `12.2`, `12.1`, `12.0`, `11.8`.
- `cloudType`: `string`, optional. Enum: `SECURE | COMMUNITY`. Default: `SECURE`.
- `computeType`: `string`, optional. Enum: `GPU | CPU`. Default: `GPU`.
- `containerDiskInGb`: `integer`, optional. Default: `50`.
- `containerRegistryAuthId`: `string`, optional.
- `countryCodes`: `string[]`, optional.
- `cpuFlavorIds`: `string[]`, optional.
- `cpuFlavorPriority`: `string`, optional. Enum: `availability | custom`. Default: `availability`.
- `dataCenterIds`: `string[]`, optional.
- `dataCenterPriority`: `string`, optional. Enum: `availability | custom`. Default: `availability`.
- `dockerEntrypoint`: `string[]`, optional. Default: `[]`.
- `dockerStartCmd`: `string[]`, optional. Default: `[]`.
- `env`: `object<string,string>`, optional. Default: `{}`.
- `globalNetworking`: `boolean`, optional.
- `gpuCount`: `integer`, optional. Default: `1`.
- `gpuTypeIds`: `string[]`, optional.
- `gpuTypePriority`: `string`, optional. Enum: `availability | custom`. Default: `availability`.
- `imageName`: `string`, optional.
- `interruptible`: `boolean`, optional. Default: `false`.
- `locked`: `boolean`, optional. Default: `false`.
- `minDiskBandwidthMBps`: `number`, optional.
- `minDownloadMbps`: `number`, optional.
- `minRAMPerGPU`: `integer`, optional. Default: `8`.
- `minUploadMbps`: `number`, optional.
- `minVCPUPerGPU`: `integer`, optional. Default: `2`.
- `name`: `string`, optional.
- `networkVolumeId`: `string`, optional.
- `ports`: `string[]`, optional. Default: `["8888/http","22/tcp"]`.
- `supportPublicIp`: `boolean`, optional.
- `templateId`: `string`, optional.
- `vcpuCount`: `integer`, optional. Default: `2`.
- `volumeEncrypted`: `boolean`, optional.
- `volumeInGb`: `integer`, optional. Default: `20`.
- `volumeMountPath`: `string`, optional. Default: `"/workspace"`.

Observed design implications for CRD mapping:

- The REST schema supports both GPU and CPU pods via `computeType`.
- Placement can be explicit (`gpuTypeIds`, `cpuFlavorIds`, `dataCenterIds`, `countryCodes`) or preference-driven via the `*Priority` fields.
- Storage has three distinct knobs:
  - `containerDiskInGb`: ephemeral container disk
  - `volumeInGb`: persisted pod volume
  - `networkVolumeId`: external reusable network volume
- `templateId` overlaps with direct image/config fields. The docs do not define precedence if both are supplied.

## Pod Get

- Method: `GET`
- Path: `/pods/{podId}`
- Full URL: `https://rest.runpod.io/v1/pods/{podId}`
- Success response: `200 OK`
- Path parameter:
  - `podId`: `string`, required

Query parameters:

- `includeMachine`: `boolean`, optional, default `false`
- `includeNetworkVolume`: `boolean`, optional, default `false`
- `includeSavingsPlans`: `boolean`, optional, default `false`
- `includeTemplate`: `boolean`, optional, default `false`
- `includeWorkers`: `boolean`, optional, default `false`

Response schema (`Pod`):

- `adjustedCostPerHr`: `number`
- `aiApiId`: `string`
- `consumerUserId`: `string`
- `containerDiskInGb`: `integer`
- `containerRegistryAuthId`: `string`
- `costPerHr`: `number` (currency)
- `cpuFlavorId`: `string`
- `desiredStatus`: `string`
- `dockerEntrypoint`: `string[]`
- `dockerStartCmd`: `string[]`
- `endpointId`: `string`
- `env`: `object<string,string>`
- `gpu`: `object`
  - `id`: `string`
  - `count`: `integer`
  - `displayName`: `string`
  - `securePrice`: `number`
  - `communityPrice`: `number`
  - `oneMonthPrice`: `number`
  - `threeMonthPrice`: `number`
  - `sixMonthPrice`: `number`
  - `oneWeekPrice`: `number`
  - `communitySpotPrice`: `number`
  - `secureSpotPrice`: `number`
- `id`: `string`
- `image`: `string`
- `interruptible`: `boolean`
- `lastStartedAt`: `string` (timestamp)
- `lastStatusChange`: `string`
- `locked`: `boolean`
- `machine`: `object`
  - `costPerHr`: `number`
  - `cpuCount`: `integer`
  - `cpuTypeId`: `string`
  - `cpuType`: `object`
    - `id`: `string`
    - `displayName`: `string`
    - `cores`: `number`
    - `threadsPerCore`: `number`
    - `groupId`: `string`
  - `currentPricePerGpu`: `number`
  - `dataCenterId`: `string`
  - `diskThroughputMBps`: `integer`
  - `gpuAvailable`: `integer`
  - `gpuDisplayName`: `string`
  - `gpuTypeId`: `string`
  - `gpuType`: `object`
    - `id`: `string`
    - `count`: `integer`
    - `displayName`: `string`
    - `securePrice`: `number`
    - `communityPrice`: `number`
    - `oneMonthPrice`: `number`
    - `threeMonthPrice`: `number`
    - `sixMonthPrice`: `number`
    - `oneWeekPrice`: `number`
    - `communitySpotPrice`: `number`
    - `secureSpotPrice`: `number`
  - `location`: `string`
  - `maintenanceEnd`: `string`
  - `maintenanceNote`: `string`
  - `maintenanceStart`: `string`
  - `maxDownloadSpeedMbps`: `integer`
  - `maxUploadSpeedMbps`: `integer`
  - `minPodGpuCount`: `integer`
  - `note`: `string`
  - `secureCloud`: `boolean`
  - `supportPublicIp`: `boolean`
- `machineId`: `string`
- `memoryInGb`: `number`
- `name`: `string`
- `networkVolume`: `object`
  - `id`: `string`
  - `name`: `string`
  - `size`: `integer`
  - `dataCenterId`: `string`
- `portMappings`: `object<string,integer> | null`
- `ports`: `string[]`
- `publicIp`: `string | null`
- `savingsPlans`: `object[]`
  - `costPerHr`: `number`
  - `endTime`: `string`
  - `gpuTypeId`: `string`
  - `id`: `string`
  - `podId`: `string`
  - `startTime`: `string`
- `slsVersion`: `integer`
- `templateId`: `string`
- `vcpuCount`: `number`
- `volumeEncrypted`: `boolean`
- `volumeInGb`: `integer`
- `volumeMountPath`: `string`

Pod state values:

- `RUNNING`: documented as the desired status returned for an active pod. GraphQL `podResume` examples also return `desiredStatus: RUNNING`.
- `EXITED`: documented as the desired status returned after a pod is stopped. GraphQL `podStop` examples return `desiredStatus: EXITED`.
- `TERMINATED`: exposed in the REST enum and list filters. The docs do not define it in prose, but it is the terminal deleted/terminated state implied by the API vocabulary.

Important observation caveat:

- The REST schema does not publish a separate `STARTING`, `PENDING`, or `INITIALIZING` value in the `desiredStatus` enum.
- However, the schema explicitly says `publicIp` and `portMappings` may still be empty while a pod is initializing.
- For controller design, that means readiness cannot be inferred from `desiredStatus` alone; networking fields may lag.

## Pod Delete

- Method: `DELETE`
- Path: `/pods/{podId}`
- Full URL: `https://rest.runpod.io/v1/pods/{podId}`
- Success response: `204 No Content`
- Path parameter:
  - `podId`: `string`, required

Documented success behavior:

- The pod is deleted and no response body is returned.

Behavior when the pod is already gone:

- Undocumented in the published pod REST reference.
- No idempotent-delete guarantee is stated.
- A `404`-style behavior is plausible, but that is an inference and should not be treated as a contract without runtime testing.

## Error Codes

Documented or directly evidenced:

- `200 OK`: successful `GET /pods` or `GET /pods/{podId}`
- `201 Created`: successful `POST /pods`
- `204 No Content`: successful `DELETE /pods/{podId}`
- `401 Unauthorized`: globally defined in the OpenAPI schema via `UnauthorizedError` with body shape:

```json
{
  "message": "string"
}
```

Undocumented for pod endpoints:

- `404 Not Found` for unknown pod IDs is not explicitly documented on the pod pages.
- `409`, `422`, `429`, and `5xx` pod-specific behavior is not documented in the pod reference pages reviewed.

Implementation guidance:

- Treat any non-`2xx` response as an error surface to capture verbatim until real API traffic establishes stable behavior.

## Rate Limits

Undocumented for pod lifecycle endpoints.

Notes:

- RunPod documents rate limits for Serverless endpoint job APIs.
- No equivalent official rate-limit table was found for pod `POST /pods`, `GET /pods/{podId}`, or `DELETE /pods/{podId}`.
- For this reference, pod lifecycle rate limits should be treated as undocumented.

## State Machine

The published REST pod enum is limited to `RUNNING`, `EXITED`, and `TERMINATED`. A conservative state machine based on the official docs is:

```text
POST /pods
  -> RUNNING

RUNNING
  -- POST /pods/{podId}/stop --> EXITED

EXITED
  -- POST /pods/{podId}/start --> RUNNING

RUNNING or EXITED
  -- DELETE /pods/{podId} --> TERMINATED
```

Important caveats:

- `POST /pods/{podId}/start` and `POST /pods/{podId}/stop` are documented in the broader pod management docs, not in the narrow pod CRUD reference pages.
- The docs mention initialization windows where `publicIp` and `portMappings` are still empty, but they do not expose a distinct initialization status in the REST `desiredStatus` enum.
- Because of that, a controller should treat networking readiness as separate from lifecycle state.

## Sources

- RunPod REST API overview: https://docs.runpod.io/api-reference/overview
- OpenAPI schema: https://rest.runpod.io/v1/openapi.json
- Create pod: https://docs.runpod.io/api-reference/pods/POST/pods
- Get pod by ID: https://docs.runpod.io/api-reference/pods/GET/pods/podId
- List pods: https://docs.runpod.io/api-reference/pods/GET/pods
- Delete pod: https://docs.runpod.io/api-reference/pods/DELETE/pods/podId
- GraphQL pod management: https://docs.runpod.io/sdks/graphql/manage-pods
- Pod management guide with start/stop examples: https://docs.runpod.io/pods/manage-pods
- API key guide: https://docs.runpod.io/get-started/api-keys
