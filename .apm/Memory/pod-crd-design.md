# Pod CRD Field Mapping Design

Last reviewed: 2026-04-19

This document maps the RunPod pod API reference into the Go CRD types that Task 4.1 should implement. It is intentionally narrower than the full RunPod API: v0.1 is optimized for GPU inference workloads and avoids fields whose semantics are ambiguous, under-documented, or likely to expand the controller surface before basic pod lifecycle support is stable.

## Section 1 — ForProvider fields (PodParameters)

Design rules:

- Keep the v0.1 CRD GPU-focused. Do not expose `computeType`; hard-code GPU behavior in the client layer.
- Mirror the RunPod OpenAPI optionality by marking every `PodParameters` field `// +optional`.
- Use pointer types for optional scalar spec fields so omitted values can be distinguished from explicit zero values.
- Use typed helper structs for env vars and ports so the CRD is easier to validate and use than raw `map[string]string` and `[]string`.

Supporting types:

```go
type CloudType string

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Port struct {
	Number   int32  `json:"number"`
	Protocol string `json:"protocol,omitempty"`
}
```

Recommended `CloudType` enum values:

- `SECURE`
- `COMMUNITY`

Recommended `PodParameters` fields:

| Go field | Go type | JSON tag | Marker | Comment |
| --- | --- | --- | --- | --- |
| `ImageName` | `*string` | `imageName,omitempty` | `// +optional` | Container image to run for the pod workload. |
| `GPUTypeIDs` | `[]string` | `gpuTypeIds,omitempty` | `// +optional` | Ordered set of acceptable RunPod GPU type IDs for placement. |
| `GPUCount` | `*int32` | `gpuCount,omitempty` | `// +optional` | Number of GPUs requested for the pod. |
| `CloudType` | `*CloudType` | `cloudType,omitempty` | `// +optional` | RunPod cloud class for scheduling, limited to `SECURE` or `COMMUNITY`. |
| `ContainerDiskInGb` | `*int32` | `containerDiskInGb,omitempty` | `// +optional` | Size of the ephemeral container disk in GiB. |
| `VolumeInGb` | `*int32` | `volumeInGb,omitempty` | `// +optional` | Size of the persisted pod volume in GiB. |
| `VolumeMountPath` | `*string` | `volumeMountPath,omitempty` | `// +optional` | Mount path inside the container for the persisted pod volume. |
| `Env` | `[]EnvVar` | `env,omitempty` | `// +optional` | Environment variables injected into the container at startup. |
| `Ports` | `[]Port` | `ports,omitempty` | `// +optional` | Container ports to expose, later serialized to RunPod `"<port>/<protocol>"` strings. |
| `DockerStartCmd` | `[]string` | `dockerStartCmd,omitempty` | `// +optional` | Command array passed to the container as its startup command. |

Fields intentionally excluded from v0.1:

- `TemplateID`: omitted because the REST docs do not define precedence when a template is combined with direct image and runtime fields.
- `NetworkVolumeID`: omitted to keep the initial storage model limited to the built-in pod volume.
- `CountryCodes`, `DataCenterIDs`, `CPUFlavorIDs`, and `*Priority` fields: omitted to keep the scheduling surface small until the provider has basic stability.
- `Interruptible`: omitted because spot-style lifecycle behavior increases controller-state complexity and is not needed for the initial inference-focused scope.
- `AllowedCudaVersions`, `MinRAMPerGPU`, `MinVCPUPerGPU`, and bandwidth constraints: omitted for v0.1 because they are useful tuning knobs but not required to model a minimal managed pod.

Implementation note for Task 4.1:

- The client should translate `[]EnvVar` into the RunPod `env` object and `[]Port` into the RunPod `ports` string list.

## Section 2 — Immutable field list

These `ForProvider` fields should receive `// +immutable` markers because changing them after pod creation should require delete and recreate rather than an in-place update:

| Field | Immutable | Justification |
| --- | --- | --- |
| `ImageName` | Yes | Changing the image changes the pod artifact itself and the pod CRUD API in scope does not expose a safe in-place reimage operation. |
| `GPUTypeIDs` | Yes | Changing the acceptable GPU types changes placement constraints and should be treated as a new scheduling request. |
| `GPUCount` | Yes | Resizing GPU count changes the machine shape and should be handled as replacement in v0.1. |
| `CloudType` | Yes | Moving between `SECURE` and `COMMUNITY` changes the placement domain and pricing model rather than mutable runtime state. |
| `ContainerDiskInGb` | Yes | Ephemeral container disk size is part of pod provisioning and the documented API surface does not define an in-place resize path. |
| `VolumeInGb` | Yes | Persisted volume size is provision-time storage configuration and should not be mutated implicitly by the controller. |
| `VolumeMountPath` | Yes | The volume mount path changes container filesystem layout and is safer as a recreate-only setting. |
| `DockerStartCmd` | Yes | Startup command changes process boot behavior and should not be drift-corrected in place without an explicit replacement flow. |

Mutable `ForProvider` fields retained for drift detection only:

- `Env`
- `Ports`

Rationale:

- The implementation plan for `Observe()` already expects mutable drift comparison for env vars and ports.
- Even though `Update()` is a no-op in v0.1, surfacing mutable drift is still useful because it tells the user the live pod no longer matches the declared runtime interface.

## Section 3 — AtProvider fields (PodObservation)

Observation rules:

- `desiredStatus` is not enough to determine readiness; networking readiness must be derived separately.
- The provider should expose both raw API fields and derived convenience fields needed by `Observe()` and connection-detail publishing.
- Fields may populate incrementally after create: a pod can report `desiredStatus=RUNNING` while `publicIp` is empty and `portMappings` is nil.

Recommended `PodObservation` fields:

| Go field | Go type | JSON tag | Comment |
| --- | --- | --- | --- |
| `PodID` | `string` | `podId,omitempty` | RunPod pod ID returned by create and mirrored from the external name. |
| `DesiredStatus` | `string` | `desiredStatus,omitempty` | Raw RunPod lifecycle status from `GET /pods/{podId}`. |
| `PublicIP` | `string` | `publicIp,omitempty` | Public IP assigned to the pod once networking is ready. |
| `PortMappings` | `map[string]int32` | `portMappings,omitempty` | External port numbers keyed by RunPod port token, absent during the networking initialization window. |
| `RuntimeEndpoint` | `string` | `runtimeEndpoint,omitempty` | Derived endpoint URL built from the first HTTP port mapping when networking is ready. |
| `CostPerHr` | `float64` | `costPerHr,omitempty` | Effective hourly pod cost from the RunPod observation response. |
| `GPUDisplayName` | `string` | `gpuDisplayName,omitempty` | Human-readable GPU name from `machine.gpuDisplayName`, or from the nested GPU object as fallback. |
| `LastStartedAt` | `*metav1.Time` | `lastStartedAt,omitempty` | Timestamp of the last pod start event, parsed from the RunPod response. |
| `NetworkingReady` | `bool` | `networkingReady` | Derived readiness flag, true only when `PublicIP` is non-empty and `PortMappings` is non-nil. |

Derivation rules:

- `NetworkingReady = (PublicIP != "" && PortMappings != nil)`
- `RuntimeEndpoint` is empty until `NetworkingReady` is true and an HTTP port can be resolved.
- `GPUDisplayName` should prefer `machine.gpuDisplayName` because it reflects the attached machine type seen during observation.

## Section 4 — Drift detection strategy + ConnectionDetails

### Drift detection in `Observe()`

Only mutable fields should be diffed:

- `Env`
- `Ports`

Normalization rules:

- `Env`: convert `[]EnvVar` into a normalized `map[string]string` and compare order-insensitively with the live RunPod `env` object.
- `Ports`: convert `[]Port` into normalized RunPod port tokens such as `8888/http` or `22/tcp` and compare as an order-insensitive set against the live `ports` array.

Default-handling rules:

- If `ForProvider.Env` is omitted, do not mark drift solely because RunPod returns an empty or default env object.
- If `ForProvider.Ports` is omitted, do not mark drift solely because RunPod applies its default port list.
- Immutable fields are never compared for drift; changes to them are replacement semantics, not update semantics.

Fields explicitly not drift-compared:

- `ImageName`
- `GPUTypeIDs`
- `GPUCount`
- `CloudType`
- `ContainerDiskInGb`
- `VolumeInGb`
- `VolumeMountPath`
- `DockerStartCmd`

### State-to-condition mapping

Use this exact mapping in `Observe()`:

- `RUNNING + NetworkingReady=true -> Available`
- `RUNNING + NetworkingReady=false -> Creating`
- `EXITED -> Unavailable`
- `TERMINATED -> Unavailable`
- `Unknown or empty desiredStatus -> Unavailable` with a warning log

Why this mapping is required:

- The RunPod REST API does not publish a `STARTING` state.
- The same pod can already be `RUNNING` while `publicIp` and `portMappings` are still not populated.
- Readiness therefore depends on both lifecycle state and networking readiness.

### ConnectionDetails

Publish these keys:

- `endpoint`: the first HTTP port mapping URL derived as `http://<PublicIP>:<externalPort>`
- `podId`: the external name / RunPod pod ID
- `port`: the first mapped external port rendered as a string

Deterministic selection rules:

1. Iterate through `ForProvider.Ports` in declared order.
2. Select the first port whose protocol is `http` for `endpoint`.
3. Resolve its RunPod port token against `AtProvider.PortMappings`.
4. Build `endpoint` only when `PublicIP` is non-empty and the mapping exists.
5. For `port`, use the same selected external port; if there is no HTTP port, use the first declared port that resolves successfully.
6. If no configured port resolves, leave `endpoint` and `port` empty rather than guessing from map iteration order.

Implementation note:

- Because RunPod defaults ports when none are provided, Task 4.1 should not attempt to derive a stable public endpoint from an unordered `portMappings` map when the spec omitted `Ports`.
