# RunPod API Coverage Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the API-coverage gaps between this provider and the RunPod REST API v1 (beads runpod-mt0, runpod-8o0, runpod-62n, runpod-rgn, runpod-4fi, runpod-rz0, runpod-18b, runpod-eov): real Pod updates + lifecycle, three new managed kinds (NetworkVolume, ContainerRegistryAuth, Template), full field parity on Pod/Endpoint, endpoint worker recycling, and endpoint-from-template mode.

**Architecture:** Same three-layer pattern the repo already uses everywhere: thin HTTP client methods in `internal/clients/` (all going through `doJSON`/`deleteStrict`/strict-404 GET helpers), namespaced managed-resource types in `apis/v1alpha1/`, and one `internal/controller/<kind>/` package per kind with a `connector` + `external` implementing Observe/Create/Update/Delete. New kinds copy the Endpoint controller's structure verbatim.

**Tech Stack:** Go, crossplane-runtime v2 (namespaced MRs, `xpv2.ManagedResourceSpec`), controller-runtime, kubebuilder markers + CEL validation, httptest for client/controller unit tests.

## Global Constraints

- Toolchain: do NOT touch `go.mod` go/toolchain directives or CI Go pins.
- `make reviewable` (generate + lint) and `go test ./...` must pass after every task.
- Do NOT refactor for cognitive complexity (Sonar go:S3776) — not in tests, not in production code. Table tests in the existing idiom are preferred over "clean" splits.
- All RunPod resource IDs interpolated into URL paths MUST pass `validateResourceID` (see `internal/clients/runpod.go:38`).
- GET semantics: only HTTP 404 means "not found"; every other non-2xx is an error (never report absence on a transient failure). DELETE semantics: 404/410 count as success. Copy the existing helpers; do not reinvent.
- Drift rule inherited from the codebase: nil/empty spec fields never drift ("unmanaged"); a field is only compared when set. Fields the API accepts but does not reliably echo back are EXCLUDED from drift detection (precedent: `dataCenterIds` on Endpoint) — the OpenAPI response schema over-promises, live behavior is authoritative and unverified for new endpoint fields.
- Never report `ResourceLateInitialized` (see comment in `internal/controller/pod/external.go:174-177`).
- Wire names come from the live OpenAPI spec (checked 2026-08-25): FlashBoot is lowercase `flashboot`; endpoint idle timeout is `idleTimeout` in seconds.
- Commit after every task with a conventional-commit message; do not push until the final task.
- Plan docs convention: this file lives in `docs/` like the other `*-plan.md` files.

---

### Task 1: Pod client operations (UpdatePod, StartPod, StopPod, full create/observe structs)

Beads: runpod-mt0, runpod-8o0, runpod-4fi (client half).

**Files:**
- Modify: `internal/clients/runpod.go`
- Test: `internal/clients/pod_ops_test.go` (new)

**Interfaces:**
- Consumes: existing `Client`, `doJSON`, `deleteStrict`, `validateResourceID`, `readErrorBody`.
- Produces (used by Task 2):
  - `type UpdatePodRequest struct {...}` (fields below)
  - `func (c *Client) UpdatePod(ctx context.Context, podID string, payload UpdatePodRequest) error` — PATCH `/pods/{podId}`
  - `func (c *Client) StartPod(ctx context.Context, podID string) error` — POST `/pods/{podId}/start`
  - `func (c *Client) StopPod(ctx context.Context, podID string) error` — POST `/pods/{podId}/stop`
  - Expanded `CreatePodRequest` and `PodResponse` fields (below).

- [ ] **Step 1: Write failing tests**

Create `internal/clients/pod_ops_test.go` following the httptest style of `internal/clients/runpod_test.go`. Test cases:

```go
package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdatePod(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"pod-1"}`))
	}))
	defer srv.Close()

	c := NewClient("key", WithBaseURL(srv.URL))
	image := "new/image:tag"
	err := c.UpdatePod(context.Background(), "pod-1", UpdatePodRequest{
		ImageName: &image,
		Env:       map[string]string{"A": "1"},
	})
	if err != nil {
		t.Fatalf("UpdatePod: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/pods/pod-1" {
		t.Fatalf("got %s %s, want PATCH /pods/pod-1", gotMethod, gotPath)
	}
	if gotBody["imageName"] != "new/image:tag" {
		t.Fatalf("imageName not sent: %v", gotBody)
	}
}

func TestUpdatePodRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.UpdatePod(context.Background(), "../evil", UpdatePodRequest{}); err == nil {
		t.Fatal("want error for invalid pod ID")
	}
}

func TestUpdatePodErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	defer srv.Close()
	c := NewClient("key", WithBaseURL(srv.URL))
	if err := c.UpdatePod(context.Background(), "pod-1", UpdatePodRequest{}); err == nil {
		t.Fatal("want error on 400")
	}
}

func TestStartStopPod(t *testing.T) {
	cases := []struct {
		name string
		call func(*Client) error
		path string
	}{
		{"start", func(c *Client) error { return c.StartPod(context.Background(), "pod-1") }, "/pods/pod-1/start"},
		{"stop", func(c *Client) error { return c.StopPod(context.Background(), "pod-1") }, "/pods/pod-1/stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"pod-1"}`))
			}))
			defer srv.Close()
			c := NewClient("key", WithBaseURL(srv.URL))
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != http.MethodPost || gotPath != tc.path {
				t.Fatalf("got %s %s, want POST %s", gotMethod, gotPath, tc.path)
			}
		})
	}
}

func TestStartPodRejectsInvalidID(t *testing.T) {
	c := NewClient("key")
	if err := c.StartPod(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid pod ID")
	}
	if err := c.StopPod(context.Background(), "a/b"); err == nil {
		t.Fatal("want error for invalid pod ID")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail** — `go test ./internal/clients/ -run 'TestUpdatePod|TestStartStopPod|TestStartPodRejects' -v`. Expected: compile error (`UpdatePodRequest` undefined).

- [ ] **Step 3: Implement in `internal/clients/runpod.go`**

Expand `CreatePodRequest` (keep existing fields; add all remaining POST /pods fields — this makes create 1:1 with the API):

```go
// CreatePodRequest mirrors the RunPod pod create payload (POST /pods, 1:1).
type CreatePodRequest struct {
	Name                 *string           `json:"name,omitempty"`
	ImageName            *string           `json:"imageName,omitempty"`
	GPUTypeIDs           []string          `json:"gpuTypeIds,omitempty"`
	GPUCount             *int32            `json:"gpuCount,omitempty"`
	CloudType            *string           `json:"cloudType,omitempty"`
	SupportPublicIP      *bool             `json:"supportPublicIp,omitempty"`
	ContainerDiskInGb    *int32            `json:"containerDiskInGb,omitempty"`
	VolumeInGb           *int32            `json:"volumeInGb,omitempty"`
	VolumeMountPath      *string           `json:"volumeMountPath,omitempty"`
	Env                  map[string]string `json:"env,omitempty"`
	Ports                []string          `json:"ports,omitempty"`
	DockerStartCmd       []string          `json:"dockerStartCmd,omitempty"`
	DockerEntrypoint     []string          `json:"dockerEntrypoint,omitempty"`
	ComputeType          *string           `json:"computeType,omitempty"`
	VCPUCount            *int32            `json:"vcpuCount,omitempty"`
	CPUFlavorIDs         []string          `json:"cpuFlavorIds,omitempty"`
	CPUFlavorPriority    *string           `json:"cpuFlavorPriority,omitempty"`
	DataCenterIDs        []string          `json:"dataCenterIds,omitempty"`
	DataCenterPriority   *string           `json:"dataCenterPriority,omitempty"`
	GPUTypePriority      *string           `json:"gpuTypePriority,omitempty"`
	CountryCodes         []string          `json:"countryCodes,omitempty"`
	Interruptible        *bool             `json:"interruptible,omitempty"`
	Locked               *bool             `json:"locked,omitempty"`
	GlobalNetworking     *bool             `json:"globalNetworking,omitempty"`
	VolumeEncrypted      *bool             `json:"volumeEncrypted,omitempty"`
	AllowedCudaVersions  []string          `json:"allowedCudaVersions,omitempty"`
	MinRAMPerGPU         *int32            `json:"minRAMPerGPU,omitempty"`
	MinVCPUPerGPU        *int32            `json:"minVCPUPerGPU,omitempty"`
	MinDiskBandwidthMBps *int32            `json:"minDiskBandwidthMBps,omitempty"`
	MinDownloadMbps      *int32            `json:"minDownloadMbps,omitempty"`
	MinUploadMbps        *int32            `json:"minUploadMbps,omitempty"`
	TemplateID           *string           `json:"templateId,omitempty"`
	NetworkVolumeID      *string           `json:"networkVolumeId,omitempty"`
	ContainerRegistryAuthID *string        `json:"containerRegistryAuthId,omitempty"`
}
```

Add `UpdatePodRequest` (exactly the PATCH /pods/{podId} schema):

```go
// UpdatePodRequest mirrors the RunPod pod PATCH payload. RunPod applies
// container-level changes (image, env, ...) when the pod next (re)starts.
type UpdatePodRequest struct {
	Name                    *string           `json:"name,omitempty"`
	ImageName               *string           `json:"imageName,omitempty"`
	ContainerDiskInGb       *int32            `json:"containerDiskInGb,omitempty"`
	VolumeInGb              *int32            `json:"volumeInGb,omitempty"`
	VolumeMountPath         *string           `json:"volumeMountPath,omitempty"`
	Env                     map[string]string `json:"env,omitempty"`
	Ports                   []string          `json:"ports,omitempty"`
	DockerStartCmd          []string          `json:"dockerStartCmd,omitempty"`
	DockerEntrypoint        []string          `json:"dockerEntrypoint,omitempty"`
	Locked                  *bool             `json:"locked,omitempty"`
	GlobalNetworking        *bool             `json:"globalNetworking,omitempty"`
	ContainerRegistryAuthID *string           `json:"containerRegistryAuthId,omitempty"`
}
```

Expand `PodResponse` with the observe fields Task 2 needs for drift (all present in GET /pods/{podId} per docs/runpod-api-reference.md):

```go
// add to PodResponse:
	Image                   string   `json:"image"`
	DockerStartCmd          []string `json:"dockerStartCmd"`
	DockerEntrypoint        []string `json:"dockerEntrypoint"`
	ContainerDiskInGb       int32    `json:"containerDiskInGb"`
	VolumeInGb              int32    `json:"volumeInGb"`
	VolumeMountPath         string   `json:"volumeMountPath"`
	Locked                  bool     `json:"locked"`
	Interruptible           bool     `json:"interruptible"`
	ContainerRegistryAuthID string   `json:"containerRegistryAuthId"`
```

Add the three methods (reuse `doJSON` for PATCH; start/stop have no body — POST with nil payload via a small helper or `doJSON` with `struct{}{}` is fine, but the API expects no body; use `NewRequest` directly like `DeletePod` does):

```go
// UpdatePod patches mutable fields of a RunPod pod. RunPod applies
// container-level changes when the pod next (re)starts.
func (c *Client) UpdatePod(ctx context.Context, podID string, payload UpdatePodRequest) error {
	if err := validateResourceID(podID); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPatch, "/pods/"+podID, payload, nil)
}

// StartPod starts or resumes a stopped pod (POST /pods/{podId}/start).
func (c *Client) StartPod(ctx context.Context, podID string) error {
	return c.podAction(ctx, podID, "start")
}

// StopPod stops a running pod without deleting it (POST /pods/{podId}/stop).
// The pod keeps its volume and keeps billing storage while stopped.
func (c *Client) StopPod(ctx context.Context, podID string) error {
	return c.podAction(ctx, podID, "stop")
}

func (c *Client) podAction(ctx context.Context, podID, action string) error {
	if err := validateResourceID(podID); err != nil {
		return err
	}
	req, err := c.NewRequest(ctx, http.MethodPost, "/pods/"+podID+"/"+action, nil)
	if err != nil {
		return errors.Wrap(err, errCreateRequest)
	}
	resp, err := c.Do(req)
	if err != nil {
		return errors.Wrap(err, errDoRequest)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errors.Errorf("RunPod POST /pods/%s/%s returned status %d: %s", podID, action, resp.StatusCode, readErrorBody(resp.Body))
	}
	return nil
}
```

- [ ] **Step 4: Run tests, verify pass** — `go test ./internal/clients/ -v`. Expected: PASS (all, including pre-existing).

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(client): pod PATCH/start/stop + full create/observe field parity"`

---

### Task 2: Pod CRD expansion + real Update + desiredState lifecycle

Beads: runpod-mt0, runpod-8o0, runpod-4fi (CRD/controller half). Depends on Task 1.

**Files:**
- Modify: `apis/v1alpha1/pod_types.go`
- Modify: `internal/controller/pod/external.go`
- Modify: `examples/pod-minimal.yaml` (comment block only, showing new knobs)
- Test: `internal/controller/pod/external_test.go`
- Generated: `make generate` (zz_generated.deepcopy.go, package/crds/runpod.crossplane.io_pods.yaml)

**Interfaces:**
- Consumes: `UpdatePodRequest`, `UpdatePod`, `StartPod`, `StopPod`, expanded `PodResponse` from Task 1.
- Produces: `PodParameters` fields listed below; behavior contract: Observe returns `ResourceUpToDate: false` only for PATCHable/lifecycle drift; `DriftDetected` in status now covers ONLY immutable-field drift.

- [ ] **Step 1: Extend `PodParameters` in `apis/v1alpha1/pod_types.go`**

Add after the existing fields (keep every existing field untouched). Mutability follows the PATCH schema: fields in `UpdatePodRequest` are mutable, everything else is `+immutable`:

```go
	// DesiredState drives the pod lifecycle: RUNNING starts/resumes the
	// pod, EXITED stops it (storage keeps billing while stopped).
	// Unset means RUNNING.
	// +kubebuilder:validation:Enum=RUNNING;EXITED
	// +optional
	DesiredState *string `json:"desiredState,omitempty"`

	// Entrypoint array overriding the image ENTRYPOINT.
	// +optional
	DockerEntrypoint []string `json:"dockerEntrypoint,omitempty"`

	// Prevent the pod from being modified or deleted from the RunPod console.
	// +optional
	Locked *bool `json:"locked,omitempty"`

	// Enable RunPod global networking for the pod.
	// Write-only: the API accepts it but does not echo it back.
	// +optional
	GlobalNetworking *bool `json:"globalNetworking,omitempty"`

	// ID of a ContainerRegistryAuth for pulling private images.
	// +optional
	ContainerRegistryAuthID *string `json:"containerRegistryAuthId,omitempty"`

	// Rent interruptible (Spot) capacity: cheaper, may be reclaimed.
	// Combine with recreateOnTerminate for auto-replacement.
	// +optional
	// +immutable
	Interruptible *bool `json:"interruptible,omitempty"`

	// GPU vs CPU pod. Defaults to GPU server-side.
	// +kubebuilder:validation:Enum=GPU;CPU
	// +optional
	// +immutable
	ComputeType *string `json:"computeType,omitempty"`

	// Number of vCPUs (CPU pods).
	// +optional
	// +immutable
	VCPUCount *int32 `json:"vcpuCount,omitempty"`

	// Acceptable CPU flavor IDs (CPU pods).
	// +optional
	// +immutable
	CPUFlavorIDs []string `json:"cpuFlavorIds,omitempty"`

	// CPU flavor selection strategy: availability or custom.
	// +kubebuilder:validation:Enum=availability;custom
	// +optional
	// +immutable
	CPUFlavorPriority *string `json:"cpuFlavorPriority,omitempty"`

	// Restrict placement to specific data center IDs.
	// +optional
	// +immutable
	DataCenterIDs []string `json:"dataCenterIds,omitempty"`

	// Data center selection strategy: availability or custom.
	// +kubebuilder:validation:Enum=availability;custom
	// +optional
	// +immutable
	DataCenterPriority *string `json:"dataCenterPriority,omitempty"`

	// GPU type selection strategy: availability or custom.
	// +kubebuilder:validation:Enum=availability;custom
	// +optional
	// +immutable
	GPUTypePriority *string `json:"gpuTypePriority,omitempty"`

	// Restrict placement to specific countries (ISO codes).
	// +optional
	// +immutable
	CountryCodes []string `json:"countryCodes,omitempty"`

	// Acceptable CUDA versions on the host.
	// +optional
	// +immutable
	AllowedCudaVersions []string `json:"allowedCudaVersions,omitempty"`

	// Minimum RAM per GPU in GB.
	// +optional
	// +immutable
	MinRAMPerGPU *int32 `json:"minRAMPerGPU,omitempty"`

	// Minimum vCPUs per GPU.
	// +optional
	// +immutable
	MinVCPUPerGPU *int32 `json:"minVCPUPerGPU,omitempty"`

	// Minimum disk bandwidth in MBps.
	// +optional
	// +immutable
	MinDiskBandwidthMBps *int32 `json:"minDiskBandwidthMBps,omitempty"`

	// Minimum download bandwidth in Mbps.
	// +optional
	// +immutable
	MinDownloadMbps *int32 `json:"minDownloadMbps,omitempty"`

	// Minimum upload bandwidth in Mbps.
	// +optional
	// +immutable
	MinUploadMbps *int32 `json:"minUploadMbps,omitempty"`

	// RunPod template to create the pod from. Direct fields set here
	// override template values server-side.
	// +optional
	// +immutable
	TemplateID *string `json:"templateId,omitempty"`

	// Network volume to attach (must be in the same data center).
	// +optional
	// +immutable
	NetworkVolumeID *string `json:"networkVolumeId,omitempty"`

	// Encrypt the pod volume.
	// +optional
	// +immutable
	VolumeEncrypted *bool `json:"volumeEncrypted,omitempty"`
```

Also REMOVE the `+immutable` marker (they are PATCHable now) from: `ImageName`, `ContainerDiskInGb`, `VolumeInGb`, `VolumeMountPath`, `DockerStartCmd`. Leave `+immutable` on `GPUTypeIDs`, `GPUCount`, `CloudType`, `SupportPublicIP`.

Update the `DriftDetected` doc comment in `PodObservation` to: "True when immutable spec fields diverge from the running pod (placement can never be reconciled in place). Mutable-field drift is reconciled via PATCH instead."

- [ ] **Step 2: Write failing controller tests in `internal/controller/pod/external_test.go`**

Follow the existing table-test style in that file (httptest server + `external{client, log, probeHTTP}`). Add cases:

```go
// 1. Observe reports not-up-to-date when env drifts (env is now PATCHable):
//    spec env {A:1}, GET response env {A:2} → ResourceUpToDate=false, DriftDetected=false.
// 2. Observe reports not-up-to-date when imageName drifts:
//    spec imageName "img:v2", GET response image "img:v1" → ResourceUpToDate=false.
// 3. Observe reports up-to-date + DriftDetected=true when only an immutable
//    field drifts: spec gpuTypeIds vs machine.gpuTypeId mismatch is NOT
//    checked (unchanged behavior) — instead use: spec unchanged env/image
//    matching response, and nothing else → ResourceUpToDate=true.
// 4. Observe lifecycle drift: spec desiredState EXITED, response desiredStatus
//    RUNNING → ResourceUpToDate=false. And the reverse (RUNNING vs EXITED).
// 5. Observe with desiredState EXITED and response EXITED → up-to-date,
//    condition Available (stopped-by-request is the desired state), and
//    recreateOnTerminate MUST NOT fire even when true.
// 6. Update sends PATCH /pods/{id} with only the mutable fields, then no
//    lifecycle call when states match.
// 7. Update calls POST /pods/{id}/stop when spec desiredState EXITED and
//    observed status RUNNING (record requests server-side and assert order:
//    PATCH first, then stop).
// 8. Update calls POST /pods/{id}/start when spec desiredState RUNNING (or
//    unset) and observed status EXITED.
```

For Update tests the `external` needs the observed status; see Step 3 for how Update re-reads it (Update calls `GetPod` itself — the test server serves GET + PATCH + POST from one handler that switches on method/path, appending each `method+" "+path` to a slice for order assertions).

- [ ] **Step 3: Run tests, verify the new ones fail** — `go test ./internal/controller/pod/ -v`. Expected: new cases FAIL (Observe still always returns up-to-date; Update is a no-op).

- [ ] **Step 4: Implement in `internal/controller/pod/external.go`**

(a) New helpers:

```go
// desiredStateOrDefault returns the lifecycle target; unset means RUNNING.
func desiredStateOrDefault(spec v1alpha1.PodParameters) string {
	if spec.DesiredState != nil {
		return *spec.DesiredState
	}
	return "RUNNING"
}

// hasMutableDrift reports whether any PATCHable spec field diverges from
// the observation. Nil/empty spec fields never drift (unmanaged).
func hasMutableDrift(spec v1alpha1.PodParameters, r *runpodclient.PodResponse) bool {
	if spec.ImageName != nil && *spec.ImageName != r.Image {
		return true
	}
	if spec.ContainerDiskInGb != nil && *spec.ContainerDiskInGb != r.ContainerDiskInGb {
		return true
	}
	if spec.VolumeInGb != nil && *spec.VolumeInGb != r.VolumeInGb {
		return true
	}
	if spec.VolumeMountPath != nil && *spec.VolumeMountPath != r.VolumeMountPath {
		return true
	}
	if spec.Locked != nil && *spec.Locked != r.Locked {
		return true
	}
	if spec.ContainerRegistryAuthID != nil && *spec.ContainerRegistryAuthID != r.ContainerRegistryAuthID {
		return true
	}
	if len(spec.DockerStartCmd) > 0 && !stringSlicesEqual(spec.DockerStartCmd, r.DockerStartCmd) {
		return true
	}
	if len(spec.DockerEntrypoint) > 0 && !stringSlicesEqual(spec.DockerEntrypoint, r.DockerEntrypoint) {
		return true
	}
	// globalNetworking is write-only (never echoed) and excluded, like
	// dataCenterIds on Endpoint.
	return hasEnvDrift(spec.Env, r.Env) || hasPortsDrift(spec.Ports, r.Ports)
}

// hasLifecycleDrift reports whether the observed lifecycle status diverges
// from spec.desiredState. Only RUNNING/EXITED participate; TERMINATED is
// handled by the recreateOnTerminate path.
func hasLifecycleDrift(spec v1alpha1.PodParameters, observedStatus string) bool {
	if observedStatus != "RUNNING" && observedStatus != "EXITED" {
		return false
	}
	return desiredStateOrDefault(spec) != observedStatus
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

(b) In `Observe`, replace the condition-setting switch and the drift block:

- `case "RUNNING"`: unchanged.
- `case "EXITED", "TERMINATED"`: if `response.DesiredStatus == "EXITED" && desiredStateOrDefault(pod.Spec.ForProvider) == "EXITED"` → `pod.SetConditions(xpv2.Available())` (stopped on purpose IS the desired state) and skip the recreate block entirely. Otherwise keep the existing Unavailable + recreateOnTerminate logic verbatim.
- Replace the final drift block with:

```go
	// Immutable placement drift can never be reconciled in place; surface it
	// via status only. Mutable drift and lifecycle drift are reconciled by
	// Update() via PATCH + start/stop.
	pod.Status.AtProvider.DriftDetected = false
	upToDate := !hasMutableDrift(pod.Spec.ForProvider, response) &&
		!hasLifecycleDrift(pod.Spec.ForProvider, response.DesiredStatus)
```

and return `ResourceUpToDate: upToDate`.

(c) In `Create`, wire ALL new spec fields into `CreatePodRequest` (every field added in Task 1's struct; `ComputeType`/priorities pass through as `*string`, `CloudType` keeps its existing conversion). Do NOT send `DesiredState` (not an API field).

(d) Replace `Update`:

```go
func (e *external) Update(ctx context.Context, mg xpresource.Managed) (managed.ExternalUpdate, error) {
	pod, ok := mg.(*v1alpha1.Pod)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotPod)
	}
	externalName := meta.GetExternalName(pod)
	if externalName == "" {
		return managed.ExternalUpdate{}, nil
	}

	spec := pod.Spec.ForProvider
	response, found, err := e.client.GetPod(ctx, externalName)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errGetPod)
	}
	if !found {
		return managed.ExternalUpdate{}, nil
	}

	if hasMutableDrift(spec, response) {
		if err := e.client.UpdatePod(ctx, externalName, runpodclient.UpdatePodRequest{
			ImageName:               spec.ImageName,
			ContainerDiskInGb:       spec.ContainerDiskInGb,
			VolumeInGb:              spec.VolumeInGb,
			VolumeMountPath:         spec.VolumeMountPath,
			Env:                     buildEnvMap(spec.Env),
			Ports:                   buildPortTokens(spec.Ports),
			DockerStartCmd:          cloneStrings(spec.DockerStartCmd),
			DockerEntrypoint:        cloneStrings(spec.DockerEntrypoint),
			Locked:                  spec.Locked,
			GlobalNetworking:        spec.GlobalNetworking,
			ContainerRegistryAuthID: spec.ContainerRegistryAuthID,
		}); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errUpdatePod)
		}
	}

	if hasLifecycleDrift(spec, response.DesiredStatus) {
		if desiredStateOrDefault(spec) == "EXITED" {
			if err := e.client.StopPod(ctx, externalName); err != nil {
				return managed.ExternalUpdate{}, errors.Wrap(err, errStopPod)
			}
		} else {
			if err := e.client.StartPod(ctx, externalName); err != nil {
				return managed.ExternalUpdate{}, errors.Wrap(err, errStartPod)
			}
		}
	}

	return managed.ExternalUpdate{}, nil
}
```

Add error constants `errUpdatePod = "cannot update pod via RunPod API"`, `errStartPod = "cannot start pod via RunPod API"`, `errStopPod = "cannot stop pod via RunPod API"`.

- [ ] **Step 5: Run tests** — `go test ./internal/controller/pod/ ./internal/clients/ -v`. Expected: PASS. Fix any pre-existing case that asserted the old always-up-to-date behavior (e.g. cases that set env drift and expected `ResourceUpToDate: true` + `DriftDetected: true` now expect `false`/`false`).

- [ ] **Step 6: Regenerate + example** — `make generate`. Append to `examples/pod-minimal.yaml` a commented block showing `desiredState: EXITED`, `interruptible: true`, `networkVolumeId`, `containerRegistryAuthId`, `templateId`.

- [ ] **Step 7: Full check + commit** — `go test ./... && make reviewable`. Then `git add -A && git commit -m "feat(pod): in-place PATCH updates, desiredState start/stop lifecycle, full spec parity"`

---

### Task 3: NetworkVolume managed resource

Bead: runpod-62n. Independent of Tasks 1-2 except for one Pod-spec field already added in Task 2 (`networkVolumeId`).

**Files:**
- Create: `internal/clients/networkvolume.go`, `internal/clients/networkvolume_test.go`
- Create: `apis/v1alpha1/networkvolume_types.go`
- Create: `internal/controller/networkvolume/networkvolume.go`, `internal/controller/networkvolume/external.go`, `internal/controller/networkvolume/external_test.go`
- Create: `examples/networkvolume.yaml`
- Modify: `apis/v1alpha1/register.go`, `cmd/provider/main.go`, `deploy/local/rbac.yaml`
- Generated: `make generate`

**Interfaces:**
- Produces:
  - `type CreateNetworkVolumeRequest struct { Name string; Size int32; DataCenterID string }` (wire: `name`, `size`, `dataCenterId` — all three required by the API)
  - `type UpdateNetworkVolumeRequest struct { Name *string; Size *int32 }`
  - `type NetworkVolumeResponse struct { ID, Name, DataCenterID string; Size int32 }`
  - `CreateNetworkVolume(ctx, payload) (string, error)`, `GetNetworkVolume(ctx, id) (*NetworkVolumeResponse, bool, error)`, `UpdateNetworkVolume(ctx, id, payload) error`, `DeleteNetworkVolume(ctx, id) error`
  - Kind `NetworkVolume` with `spec.forProvider: {size (required, min 1), dataCenterId (required, immutable), name (optional, defaults to metadata.name)}`; `status.atProvider: {networkVolumeId, name, size, dataCenterId}`; connection detail `networkVolumeId`.

- [ ] **Step 1: Client tests** (`internal/clients/networkvolume_test.go`, same httptest pattern as Task 1): create returns ID from `{"id":"nv-1",...}`; GET 404 → `found=false, err=nil`; GET 500 → error; update sends PATCH `/networkvolumes/nv-1`; delete treats 404/410 as success; invalid IDs rejected on get/update/delete. Run → FAIL (undefined).

- [ ] **Step 2: Client implementation** (`internal/clients/networkvolume.go`):

```go
package clients

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pkg/errors"
)

const (
	networkVolumesPath       = "/networkvolumes"
	networkVolumesPathPrefix = networkVolumesPath + "/"
)

// CreateNetworkVolumeRequest mirrors POST /networkvolumes; all fields are
// required by the API.
type CreateNetworkVolumeRequest struct {
	Name         string `json:"name"`
	Size         int32  `json:"size"`
	DataCenterID string `json:"dataCenterId"`
}

// UpdateNetworkVolumeRequest mirrors PATCH /networkvolumes/{id}. RunPod only
// allows growing size; shrink attempts surface as API errors.
type UpdateNetworkVolumeRequest struct {
	Name *string `json:"name,omitempty"`
	Size *int32  `json:"size,omitempty"`
}

// NetworkVolumeResponse mirrors the network volume observation.
type NetworkVolumeResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Size         int32  `json:"size"`
	DataCenterID string `json:"dataCenterId"`
}

// CreateNetworkVolume creates a RunPod network volume and returns its ID.
func (c *Client) CreateNetworkVolume(ctx context.Context, payload CreateNetworkVolumeRequest) (string, error) {
	var out NetworkVolumeResponse
	if err := c.doJSON(ctx, http.MethodPost, networkVolumesPath, payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// GetNetworkVolume retrieves a network volume; only 404 means "not found".
func (c *Client) GetNetworkVolume(ctx context.Context, id string) (*NetworkVolumeResponse, bool, error) {
	if err := validateResourceID(id); err != nil {
		return nil, false, err
	}
	req, err := c.NewRequest(ctx, http.MethodGet, networkVolumesPathPrefix+id, nil)
	if err != nil {
		return nil, false, errors.Wrap(err, errCreateRequest)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, false, errors.Wrap(err, errDoRequest)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false, errors.Errorf("RunPod GET /networkvolumes/%s returned status %d: %s", id, resp.StatusCode, readErrorBody(resp.Body))
	}
	var out NetworkVolumeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, errors.Wrap(err, errDecodeResponse)
	}
	return &out, true, nil
}

// UpdateNetworkVolume patches a network volume's name and/or size.
func (c *Client) UpdateNetworkVolume(ctx context.Context, id string, payload UpdateNetworkVolumeRequest) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPatch, networkVolumesPathPrefix+id, payload, nil)
}

// DeleteNetworkVolume deletes a network volume; 404/410 count as success.
func (c *Client) DeleteNetworkVolume(ctx context.Context, id string) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	return c.deleteStrict(ctx, networkVolumesPathPrefix+id)
}
```

Run client tests → PASS. Commit: `git commit -am "feat(client): network volume CRUD"`.

- [ ] **Step 3: API type** (`apis/v1alpha1/networkvolume_types.go`) — copy the structure of `pod_types.go` (interface checks, Spec/Status wrappers, getters/setters, List type, type metadata vars) with:

```go
// NetworkVolumeParameters define the desired state of a RunPod network volume.
type NetworkVolumeParameters struct {
	// Volume name shown in the RunPod console. Defaults to the resource name.
	// +optional
	Name *string `json:"name,omitempty"`

	// Volume size in GB. RunPod only supports growing a volume; shrinking
	// is rejected by the API.
	// +kubebuilder:validation:Minimum=1
	Size int32 `json:"size"`

	// Data center hosting the volume (e.g. "EU-RO-1"). Pods/endpoints using
	// the volume must be scheduled in the same data center.
	// +immutable
	DataCenterID string `json:"dataCenterId"`
}

// NetworkVolumeObservation captures the observed state returned by RunPod.
type NetworkVolumeObservation struct {
	NetworkVolumeID string `json:"networkVolumeId,omitempty"`
	Name            string `json:"name,omitempty"`
	Size            int32  `json:"size,omitempty"`
	DataCenterID    string `json:"dataCenterId,omitempty"`
}
```

Register in `apis/v1alpha1/register.go` init: `SchemeBuilder.Register(&NetworkVolume{}, &NetworkVolumeList{})`.

- [ ] **Step 4: Controller tests** (`internal/controller/networkvolume/external_test.go`, mirror `internal/controller/endpoint/external_test.go` style): Observe no-external-name → not exists; Observe 404 → not exists; Observe match → exists+up-to-date+Available, connection detail `networkVolumeId`; Observe size 100 vs spec 200 → not up-to-date; Update sends PATCH with name+size; Create POSTs `{name, size, dataCenterId}` using metadata.name when spec.name nil, sets external-name from response id; Delete calls DELETE. Run → FAIL.

- [ ] **Step 5: Controller** — `internal/controller/networkvolume/networkvolume.go` is a copy of `internal/controller/endpoint/endpoint.go` with `Endpoint`→`NetworkVolume` types/kind string and error text `"managed resource is not a NetworkVolume"`. `external.go`:

```go
func (e *external) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotNetworkVolume)
	}
	externalName := meta.GetExternalName(nv)
	if externalName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	response, found, err := e.client.GetNetworkVolume(ctx, externalName)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetNetworkVolume)
	}
	if !found {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	nv.Status.AtProvider = v1alpha1.NetworkVolumeObservation{
		NetworkVolumeID: response.ID,
		Name:            response.Name,
		Size:            response.Size,
		DataCenterID:    response.DataCenterID,
	}
	nv.SetConditions(xpv2.Available())
	upToDate := response.Size == nv.Spec.ForProvider.Size &&
		(nv.Spec.ForProvider.Name == nil || *nv.Spec.ForProvider.Name == response.Name)
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
		ConnectionDetails: managed.ConnectionDetails{
			"networkVolumeId": []byte(response.ID),
		},
	}, nil
}

func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotNetworkVolume)
	}
	name := nv.GetName()
	if nv.Spec.ForProvider.Name != nil {
		name = *nv.Spec.ForProvider.Name
	}
	id, err := e.client.CreateNetworkVolume(ctx, runpodclient.CreateNetworkVolumeRequest{
		Name:         name,
		Size:         nv.Spec.ForProvider.Size,
		DataCenterID: nv.Spec.ForProvider.DataCenterID,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateNetworkVolume)
	}
	meta.SetExternalName(nv, id)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{"networkVolumeId": []byte(id)},
	}, nil
}

func (e *external) Update(ctx context.Context, mg xpresource.Managed) (managed.ExternalUpdate, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotNetworkVolume)
	}
	externalName := meta.GetExternalName(nv)
	if externalName == "" {
		return managed.ExternalUpdate{}, nil
	}
	size := nv.Spec.ForProvider.Size
	payload := runpodclient.UpdateNetworkVolumeRequest{Size: &size, Name: nv.Spec.ForProvider.Name}
	if err := e.client.UpdateNetworkVolume(ctx, externalName, payload); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateNetworkVolume)
	}
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg xpresource.Managed) (managed.ExternalDelete, error) {
	nv, ok := mg.(*v1alpha1.NetworkVolume)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotNetworkVolume)
	}
	externalName := meta.GetExternalName(nv)
	if externalName == "" {
		return managed.ExternalDelete{}, nil
	}
	return managed.ExternalDelete{}, errors.Wrap(e.client.DeleteNetworkVolume(ctx, externalName), errDeleteNetworkVolume)
}

func (e *external) Disconnect(_ context.Context) error { return nil }
```

(`errors.Wrap(nil, ...)` returns nil, so the Delete one-liner is safe — same pattern is fine expanded if the linter complains.)

- [ ] **Step 6: Wire-up** — `cmd/provider/main.go`: add `networkvolumecontroller "github.com/zapr-16/provider-runpod/internal/controller/networkvolume"` and a `Setup` block after the endpoint one. `deploy/local/rbac.yaml`: add `networkvolumes`, `networkvolumes/status`, `networkvolumes/finalizers` to the managed-resources rule. `examples/networkvolume.yaml`:

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
    size: 50          # GB; can grow, never shrink
    dataCenterId: EU-RO-1
```

- [ ] **Step 7: Generate, test, commit** — `make generate && go test ./... && make reviewable`; `git add -A && git commit -m "feat: NetworkVolume managed resource (runpod-62n)"`

---

### Task 4: ContainerRegistryAuth managed resource

Bead: runpod-rgn (kind half; the passthrough fields landed in Tasks 2 and 6). Independent of Tasks 1-3.

**Files:**
- Create: `internal/clients/containerregistryauth.go`, `internal/clients/containerregistryauth_test.go`
- Create: `apis/v1alpha1/containerregistryauth_types.go`
- Create: `internal/controller/containerregistryauth/containerregistryauth.go`, `internal/controller/containerregistryauth/external.go`, `internal/controller/containerregistryauth/external_test.go`
- Create: `examples/containerregistryauth.yaml`
- Modify: `apis/v1alpha1/register.go`, `cmd/provider/main.go`, `deploy/local/rbac.yaml`
- Generated: `make generate`

**Interfaces:**
- Produces:
  - `type CreateContainerRegistryAuthRequest struct { Name, Username, Password string }` (wire: `name`, `username`, `password` — all required)
  - `type ContainerRegistryAuthResponse struct { ID, Name string }` (the API never returns credentials)
  - `CreateContainerRegistryAuth(ctx, payload) (string, error)`, `GetContainerRegistryAuth(ctx, id) (*ContainerRegistryAuthResponse, bool, error)`, `DeleteContainerRegistryAuth(ctx, id) error` — there is NO update endpoint in the API; the kind is immutable (credential rotation = delete + recreate).
  - Kind `ContainerRegistryAuth`: `spec.forProvider: {name (optional, defaults to metadata.name, immutable), credentialsSecretRef {name, usernameKey (default "username"), passwordKey (default "password")}}`. The secret is read from the resource's OWN namespace at Create time. `status.atProvider: {containerRegistryAuthId, name}`; connection detail `containerRegistryAuthId`.

- [ ] **Step 1: Client tests** — mirror Task 3 Step 1 (create returns id; get 404/500 semantics; delete 404/410 ok; invalid IDs rejected). Run → FAIL.

- [ ] **Step 2: Client implementation** (`internal/clients/containerregistryauth.go`) — same shape as Task 3 Step 2 with paths `/containerregistryauth` + `/containerregistryauth/`; no Update method. Run tests → PASS. Commit `feat(client): container registry auth create/get/delete`.

- [ ] **Step 3: API type** (`apis/v1alpha1/containerregistryauth_types.go`):

```go
// ContainerRegistryAuthParameters define a private container registry
// credential. The referenced secret must live in the resource's namespace.
type ContainerRegistryAuthParameters struct {
	// Display name in RunPod. Defaults to the resource name. The RunPod
	// API has no update call for registry auths, so all fields are
	// immutable: rotate credentials by deleting and recreating.
	// +optional
	// +immutable
	Name *string `json:"name,omitempty"`

	// Reference to a Secret in this resource's namespace holding the
	// registry username and password.
	// +immutable
	CredentialsSecretRef ContainerRegistrySecretRef `json:"credentialsSecretRef"`
}

// ContainerRegistrySecretRef points at the credential secret keys.
type ContainerRegistrySecretRef struct {
	// Name of the secret.
	Name string `json:"name"`

	// Key holding the registry username.
	// +kubebuilder:default=username
	// +optional
	UsernameKey string `json:"usernameKey,omitempty"`

	// Key holding the registry password or token.
	// +kubebuilder:default=password
	// +optional
	PasswordKey string `json:"passwordKey,omitempty"`
}

// ContainerRegistryAuthObservation captures the observed state.
type ContainerRegistryAuthObservation struct {
	ContainerRegistryAuthID string `json:"containerRegistryAuthId,omitempty"`
	Name                    string `json:"name,omitempty"`
}
```

Boilerplate (Spec/Status/getters/List/metadata vars) copied from `pod_types.go`; register in `register.go`.

- [ ] **Step 4: Controller tests** — mirror endpoint tests, plus: Create reads the secret (use `fake.NewClientBuilder` from `sigs.k8s.io/controller-runtime/pkg/client/fake` with a Secret fixture in namespace `default`, keys `username`/`password`) and POSTs `{name, username, password}`; Create fails cleanly when the secret or key is missing; Observe → up-to-date always when found (immutable kind); Update is a no-op; Delete strict. Run → FAIL.

- [ ] **Step 5: Controller** — `containerregistryauth.go` copies the endpoint connector/Setup (type renames). The `external` struct additionally carries `kube client.Client` and the managed resource's namespace comes from `cra.GetNamespace()`. Create:

```go
func (e *external) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	cra, ok := mg.(*v1alpha1.ContainerRegistryAuth)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotContainerRegistryAuth)
	}
	ref := cra.Spec.ForProvider.CredentialsSecretRef
	secret := &corev1.Secret{}
	if err := e.kube.Get(ctx, types.NamespacedName{Namespace: cra.GetNamespace(), Name: ref.Name}, secret); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errGetCredentialsSecret)
	}
	usernameKey := ref.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := ref.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}
	username, uok := secret.Data[usernameKey]
	password, pok := secret.Data[passwordKey]
	if !uok || !pok {
		return managed.ExternalCreation{}, errors.Errorf(errMissingSecretKeys, usernameKey, passwordKey)
	}
	name := cra.GetName()
	if cra.Spec.ForProvider.Name != nil {
		name = *cra.Spec.ForProvider.Name
	}
	id, err := e.client.CreateContainerRegistryAuth(ctx, runpodclient.CreateContainerRegistryAuthRequest{
		Name:     name,
		Username: string(username),
		Password: string(password),
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateContainerRegistryAuth)
	}
	meta.SetExternalName(cra, id)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{"containerRegistryAuthId": []byte(id)},
	}, nil
}
```

with `errMissingSecretKeys = "credentials secret is missing key %q or %q"`. Observe: GET → exists, Available, `ResourceUpToDate: true` always (immutable), populate atProvider + connection detail. Update: no-op returning empty (log V(1) like the old pod Update). Delete: `DeleteContainerRegistryAuth`.

- [ ] **Step 6: Wire-up + example** — main.go Setup, RBAC (`containerregistryauths{,/status,/finalizers}`), example:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ghcr-creds
  namespace: default
type: Opaque
stringData:
  username: my-github-user
  password: ghp_xxxx
---
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

- [ ] **Step 7: Generate, test, commit** — `make generate && go test ./... && make reviewable`; commit `feat: ContainerRegistryAuth managed resource (runpod-rgn)`.

---

### Task 5: Standalone Template managed resource + full template client structs

Beads: runpod-18b, and the template-struct half of runpod-eov.

**Files:**
- Modify: `internal/clients/serverless.go` (expand template structs)
- Create: `apis/v1alpha1/template_types.go`
- Create: `internal/controller/template/template.go`, `internal/controller/template/external.go`, `internal/controller/template/external_test.go`
- Create: `examples/template.yaml`
- Modify: `apis/v1alpha1/register.go`, `cmd/provider/main.go`, `deploy/local/rbac.yaml`, `internal/clients/serverless_test.go` (or wherever template client tests live — check `grep -rl CreateTemplate internal/clients/*_test.go`)
- Generated: `make generate`

**Interfaces:**
- Consumes: existing `CreateTemplate`/`GetTemplate`/`UpdateTemplate`/`DeleteTemplate`.
- Produces (Task 6 depends on these struct fields):

```go
// added to CreateTemplateRequest:
	DockerStartCmd          []string `json:"dockerStartCmd,omitempty"`
	DockerEntrypoint        []string `json:"dockerEntrypoint,omitempty"`
	ContainerRegistryAuthID *string  `json:"containerRegistryAuthId,omitempty"`
	Ports                   []string `json:"ports,omitempty"`
	VolumeInGb              *int32   `json:"volumeInGb,omitempty"`
	VolumeMountPath         *string  `json:"volumeMountPath,omitempty"`
// added to UpdateTemplateRequest: same six fields (all pointer/slice, omitempty)
// added to TemplateResponse:
	DockerStartCmd          []string `json:"dockerStartCmd"`
	DockerEntrypoint        []string `json:"dockerEntrypoint"`
	ContainerRegistryAuthID string   `json:"containerRegistryAuthId"`
	Ports                   []string `json:"ports"`
	VolumeInGb              int32    `json:"volumeInGb"`
	VolumeMountPath         string   `json:"volumeMountPath"`
```

- Kind `Template`: `spec.forProvider: {imageName (required), isServerless (optional bool, immutable), name (optional, defaults to metadata.name), env ([]EnvVar), containerDiskInGb, dockerStartCmd, dockerEntrypoint, containerRegistryAuthId, ports ([]Port, reuse the Pod Port type + buildPortTokens), volumeInGb, volumeMountPath}`; `status.atProvider: {templateId, name}`; connection detail `templateId`.

- [ ] **Step 1: Expand the client structs** exactly as listed above (no new methods needed). Extend the existing template client tests to assert the new fields round-trip in create/update payloads. Run → PASS quickly (struct-only change; tests updated first per TDD: add assertions, watch them fail, add fields).

- [ ] **Step 2: API type** — `TemplateParameters` per the interface block; all fields mutable EXCEPT `isServerless` (`+immutable`; the API's PATCH schema has no isServerless). Boilerplate from pod_types.go; register.

- [ ] **Step 3: Controller tests** — Observe found+match → up-to-date; imageName drift → not up-to-date; env drift → not up-to-date; Create POSTs full payload (name defaulting, `isServerless` passthrough) and sets external-name; Update PATCHes only spec-set fields; Delete strict 404-ok. Run → FAIL.

- [ ] **Step 4: Controller** — connector copies endpoint's. Drift helper (in `internal/controller/template/external.go`):

```go
// hasStandaloneTemplateDrift compares spec-set fields against the template
// observation; nil/empty spec fields never drift.
func hasStandaloneTemplateDrift(spec v1alpha1.TemplateParameters, r runpodclient.TemplateResponse) bool {
	if spec.ImageName != r.ImageName {
		return true
	}
	if spec.ContainerDiskInGb != nil && *spec.ContainerDiskInGb != r.ContainerDiskInGb {
		return true
	}
	if spec.VolumeInGb != nil && *spec.VolumeInGb != r.VolumeInGb {
		return true
	}
	if spec.VolumeMountPath != nil && *spec.VolumeMountPath != r.VolumeMountPath {
		return true
	}
	if spec.ContainerRegistryAuthID != nil && *spec.ContainerRegistryAuthID != r.ContainerRegistryAuthID {
		return true
	}
	if len(spec.DockerStartCmd) > 0 && !stringSlicesEqual(spec.DockerStartCmd, r.DockerStartCmd) {
		return true
	}
	if len(spec.DockerEntrypoint) > 0 && !stringSlicesEqual(spec.DockerEntrypoint, r.DockerEntrypoint) {
		return true
	}
	if len(spec.Env) > 0 && !stringMapsEqual(buildEnvMap(spec.Env), r.Env) {
		return true
	}
	if len(spec.Ports) > 0 && !portTokensEqual(buildPortTokens(spec.Ports), r.Ports) {
		return true
	}
	return false
}
```

(`stringSlicesEqual`, `stringMapsEqual`, `buildEnvMap`, `buildPortTokens`, and a `portTokensEqual` set-compare are small file-local helpers — copy them from the pod/endpoint controllers; each controller package already keeps its own copies by convention.) Observe/Create/Update/Delete follow the NetworkVolume shape (Task 3 Step 5) with the template client methods; Update PATCHes `UpdateTemplateRequest{ImageName: &spec.ImageName, Env: buildEnvMap(spec.Env), ContainerDiskInGb: spec.ContainerDiskInGb, DockerStartCmd: ..., DockerEntrypoint: ..., ContainerRegistryAuthID: ..., Ports: buildPortTokens(spec.Ports), VolumeInGb: ..., VolumeMountPath: ...}`.

- [ ] **Step 5: Wire-up + example** — main.go, RBAC (`templates{,/status,/finalizers}`), example:

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

- [ ] **Step 6: Generate, test, commit** — `make generate && go test ./... && make reviewable`; commit `feat: standalone Template managed resource (runpod-18b)`.

---

### Task 6: Endpoint spec expansion (CPU, CUDA, networkVolumeIds, template-carried fields)

Bead: runpod-eov. Depends on Task 5 (template struct fields).

**Files:**
- Modify: `apis/v1alpha1/endpoint_types.go`
- Modify: `internal/clients/serverless.go` (endpoint request structs)
- Modify: `internal/controller/endpoint/external.go`
- Modify: `examples/endpoint-vllm.yaml` (commented new knobs)
- Test: `internal/controller/endpoint/external_test.go`, client tests
- Generated: `make generate`

**Interfaces:**
- Produces on `EndpointParameters` (all `+optional`):
  - Endpoint-level: `ComputeType *string` (enum GPU;CPU, `+immutable`, wire `computeType`, create-only — absent from the PATCH schema), `VCPUCount *int32` (wire `vcpuCount`), `CPUFlavorIDs []string` (wire `cpuFlavorIds`), `AllowedCudaVersions []string`, `MinCudaVersion *string`, `NetworkVolumeIDs []string` (wire `networkVolumeIds`)
  - Template-carried: `DockerStartCmd []string`, `DockerEntrypoint []string`, `ContainerRegistryAuthID *string`
- CEL on `EndpointParameters`: `+kubebuilder:validation:XValidation:rule="!(has(self.networkVolumeId) && has(self.networkVolumeIds))",message="networkVolumeId and networkVolumeIds are mutually exclusive"`

- [ ] **Step 1: Extend client structs** — add to BOTH `CreateEndpointRequest` and `UpdateEndpointRequest` (all omitempty): `VCPUCount *int32 json:"vcpuCount"`, `CPUFlavorIDs []string json:"cpuFlavorIds"`, `AllowedCudaVersions []string`, `MinCudaVersion *string`, `NetworkVolumeIDs []string json:"networkVolumeIds"`; add `ComputeType *string json:"computeType"` to Create ONLY. Extend the implicit-template create/update call sites later in Step 3.

- [ ] **Step 2: Failing tests** — controller: Create sends the new fields on POST /endpoints and the template-carried fields on POST /templates; Update forwards them on both PATCHes; drift does NOT fire for any of the new endpoint-level fields (serve a GET response omitting them while spec sets them → still up-to-date); template drift DOES fire for dockerStartCmd/dockerEntrypoint/containerRegistryAuthId (they are echoed by GET /templates). Run → FAIL.

- [ ] **Step 3: Implement** — types per interface block; wire into `buildCreateEndpointRequest`, `Update()`'s `UpdateEndpointRequest`, the `CreateTemplateRequest` in `Create()` (`DockerStartCmd: cloneStrings(spec.DockerStartCmd), DockerEntrypoint: cloneStrings(spec.DockerEntrypoint), ContainerRegistryAuthID: spec.ContainerRegistryAuthID`), the `UpdateTemplateRequest` in `Update()`, and `hasTemplateDrift` (three new comparisons, same nil-safe style). Do NOT add any of the new endpoint-level fields to `hasEndpointDrift` — comment: `// computeType/vcpuCount/cpuFlavorIds/allowedCudaVersions/minCudaVersion/networkVolumeIds are not verified to be echoed back (the OpenAPI response schema over-promises; see dataCenterIds), so they are write-only for drift purposes.`

- [ ] **Step 4: Generate, test, commit** — `make generate && go test ./... && make reviewable`; commit `feat(endpoint): CPU endpoints, CUDA pinning, multi network volumes, full template passthrough (runpod-eov)`.

---

### Task 7: Endpoint templateId reference mode + worker recycle on template change

Beads: runpod-18b (endpoint half), runpod-rz0. Depends on Tasks 5-6.

**Files:**
- Modify: `apis/v1alpha1/endpoint_types.go`
- Modify: `internal/clients/serverless.go` (`TemplateID *string json:"templateId,omitempty"` on `UpdateEndpointRequest`)
- Modify: `internal/controller/endpoint/external.go`
- Create: `examples/endpoint-from-template.yaml`
- Test: `internal/controller/endpoint/external_test.go`
- Generated: `make generate`

**Interfaces:**
- Produces on `EndpointParameters`:

```go
	// Existing RunPod template to run the endpoint from, e.g. one managed
	// by a Template resource. Mutually exclusive with imageName and the
	// other template-carried fields (env, containerDiskInGb,
	// dockerStartCmd, dockerEntrypoint, containerRegistryAuthId): when
	// templateId is set the controller does NOT create, patch, or delete
	// any template — the referenced template is owned elsewhere.
	// +optional
	TemplateID *string `json:"templateId,omitempty"`

	// Recycle running/idle workers after the implicit template changes
	// (image/env/disk), by cycling workersMax through 0. Without this,
	// FlashBoot standby workers keep serving the old template
	// indefinitely. Defaults to true. Ignored in templateId mode.
	// +kubebuilder:default=true
	// +optional
	RecycleWorkersOnTemplateChange *bool `json:"recycleWorkersOnTemplateChange,omitempty"`
```

- `ImageName` changes from required `string` to `*string` + `+optional` (ripple: every `spec.ImageName` use becomes nil-guarded; in template mode it is nil).
- CEL on `EndpointParameters` (add to the marker block from Task 6):
  - `+kubebuilder:validation:XValidation:rule="has(self.imageName) != has(self.templateId)",message="exactly one of imageName or templateId must be set"`
  - `+kubebuilder:validation:XValidation:rule="!has(self.templateId) || (!has(self.env) && !has(self.containerDiskInGb) && !has(self.dockerStartCmd) && !has(self.dockerEntrypoint) && !has(self.containerRegistryAuthId))",message="template-carried fields cannot be set together with templateId"`

- [ ] **Step 1: Failing tests** (controller):

```go
// templateId mode:
// 1. Create with templateId "tpl-ext": POST /endpoints carries templateId
//    tpl-ext and NO POST /templates happens.
// 2. Observe in templateId mode: no GET /templates call; observed
//    templateId "tpl-ext" vs spec "tpl-ext" → up-to-date; observed
//    "tpl-old" vs spec "tpl-new" → not up-to-date.
// 3. Update in templateId mode: PATCH /endpoints carries templateId; NO
//    PATCH /templates; NO recycle.
// 4. Delete in templateId mode: DELETE /endpoints only — the referenced
//    template MUST NOT be deleted.
// implicit mode recycle:
// 5. Update with template drift (GET /templates shows old image) and
//    recycle default-true: request order is PATCH /endpoints → PATCH
//    /templates/{id} → PATCH /endpoints {workersMax:0} → PATCH /endpoints
//    {workersMax:<spec or observed max>}.
// 6. Update with template drift and recycleWorkersOnTemplateChange=false:
//    no workersMax cycling PATCHes.
// 7. Update with NO template drift: no template PATCH, no recycling.
```

Run → FAIL.

- [ ] **Step 2: Implement in `internal/controller/endpoint/external.go`**

(a) `templateMode := ep.Spec.ForProvider.TemplateID != nil` everywhere.

(b) `Observe`: in template mode, skip the implicit-template drift GET; instead `upToDate = upToDate && (response.TemplateID == *ep.Spec.ForProvider.TemplateID)`.

(c) `Create`: in template mode skip `CreateTemplate` and pass `*spec.TemplateID` to `buildCreateEndpointRequest` (signature keeps taking `templateID string`); implicit mode unchanged but `ImageName: *spec.ImageName` (nil-guarded by CEL; still check `spec.ImageName != nil` defensively and return an error otherwise).

(d) `Update` — restructure:

```go
	spec := ep.Spec.ForProvider
	endpointPatch := runpodclient.UpdateEndpointRequest{ /* existing fields + Task 6 fields */ }
	if spec.TemplateID != nil {
		endpointPatch.TemplateID = spec.TemplateID
	}
	if err := e.client.UpdateEndpoint(ctx, externalName, endpointPatch); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateEndpoint)
	}
	if spec.TemplateID != nil {
		// Referenced template is owned elsewhere; nothing more to do.
		return managed.ExternalUpdate{}, nil
	}

	// ... existing templateID resolution from status / GET fallback ...

	// Detect real template drift BEFORE patching so workers are only
	// recycled when the template actually changed (runpod-rz0).
	templateChanged := false
	if current, found, err := e.client.GetTemplate(ctx, templateID); err == nil && found {
		templateChanged = hasTemplateDrift(spec, *current)
	}
	if !templateChanged {
		return managed.ExternalUpdate{}, nil
	}

	if err := e.client.UpdateTemplate(ctx, templateID, runpodclient.UpdateTemplateRequest{ /* existing + Task 6 fields */ }); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateTemplate)
	}

	if spec.RecycleWorkersOnTemplateChange != nil && !*spec.RecycleWorkersOnTemplateChange {
		return managed.ExternalUpdate{}, nil
	}
	// Live workers (FlashBoot standby included) never pick up template
	// changes on their own; cycle workersMax through 0 to force a rollout.
	restoreMax := spec.WorkersMax
	if restoreMax == nil {
		if response, found, err := e.client.GetEndpoint(ctx, externalName); err == nil && found {
			restoreMax = &response.WorkersMax
		}
	}
	if restoreMax == nil {
		e.log.Info("skipping worker recycle: cannot determine workersMax to restore", "endpoint-id", externalName)
		return managed.ExternalUpdate{}, nil
	}
	zero := int32(0)
	if err := e.client.UpdateEndpoint(ctx, externalName, runpodclient.UpdateEndpointRequest{WorkersMax: &zero}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errRecycleWorkers)
	}
	if err := e.client.UpdateEndpoint(ctx, externalName, runpodclient.UpdateEndpointRequest{WorkersMax: restoreMax}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errRecycleWorkers)
	}
```

CAVEAT for the workersMax:0 PATCH: `UpdateEndpointRequest` uses `omitempty`, and `*int32(0)` survives omitempty only because the field is a pointer — verify with a marshal assertion in the client test (`{"workersMax":0}` must appear in the body).

Add `errRecycleWorkers = "cannot recycle endpoint workers after template change"`.

(e) `Delete`: wrap the entire template-resolution + `DeleteTemplate` block in `if ep.Spec.ForProvider.TemplateID == nil { ... }`.

- [ ] **Step 3: Fix ripples** — every `spec.ImageName` dereference in Create/Update/hasTemplateDrift becomes `*spec.ImageName` behind nil guards; `hasTemplateDrift` first line becomes `if spec.ImageName != nil && *spec.ImageName != observed.ImageName { return true }`. Update `examples/endpoint-vllm.yaml`/`endpoint-vlm.yaml` — no changes needed (imageName still valid), but add `examples/endpoint-from-template.yaml`:

```yaml
apiVersion: runpod.crossplane.io/v1alpha1
kind: Endpoint
metadata:
  name: vllm-from-template
  namespace: default
spec:
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
  forProvider:
    templateId: <template-id>   # e.g. status.atProvider.templateId of a Template resource
    gpuTypeIds:
      - "NVIDIA GeForce RTX 3090"
    workersMin: 0
    workersMax: 2
```

- [ ] **Step 4: Generate, test, commit** — `make generate && go test ./... && make reviewable`; commit `feat(endpoint): templateId reference mode + worker recycle on template change (runpod-18b, runpod-rz0)`.

---

### Task 8: Docs, version bump, final verification

**Files:**
- Modify: `README.md` (new kinds section: NetworkVolume, ContainerRegistryAuth, Template — one short example each, pointing at `examples/`; bump install tag to `v0.5.0-pkg`)
- Modify: `docs/serverless-endpoints.md` (append an "API coverage fixes (2026-08)" note: templateId mode, worker recycle, new fields)
- Modify: `docs/runpod-api-reference.md` (append a short "Pod update & lifecycle" section documenting PATCH /pods and start/stop semantics as implemented)
- Modify: `Makefile` (`XPKG_TAG ?= v0.5.0`), `package/crossplane.yaml` (image tag `v0.5.0`)
- Verify: everything

- [ ] **Step 1: Write the docs/version changes** listed above. Keep README examples minimal (metadata + forProvider only), consistent with the existing Pod/Endpoint sections.
- [ ] **Step 2: Full verification** — run and paste output of:

```bash
make reviewable        # generate + lint, must be clean
go test ./... 2>&1 | tail -15
git status --short     # no ungenerated diffs after make generate
```

- [ ] **Step 3: Commit** — `git add -A && git commit -m "docs+chore: new kinds documented, version v0.5.0"`

---

## Out of scope (explicitly)

- Bead runpod-nt4 (billing, savings plans, data-plane job APIs) — excluded by request.
- Live e2e against real RunPod (pod PATCH/start/stop semantics, worker-recycle timing) — unit-tested only here; live smoke is a follow-up the maintainer runs (`hack/` scripts + real key).
- GraphQL API, `POST /pods/{id}/reset|restart` as imperative annotations (start/stop cover the lifecycle bead's core; reset/restart can be a future annotation-driven feature).
- Marking beads closed — done by the orchestrator after PR creation (the beads DB lives on the main checkout, not this worktree).

## Self-review notes

- Type consistency: `UpdatePodRequest`/`UpdatePod` names match between Tasks 1-2; template struct fields between Tasks 5-6-7; `stringSlicesEqual` exists already in `internal/controller/endpoint/external.go` and is ADDED to the pod controller in Task 2 (package-local copies are this repo's convention).
- Spec coverage: runpod-mt0 → T1+T2; runpod-8o0 → T1+T2; runpod-4fi → T1+T2; runpod-62n → T3 (+ Pod field in T2); runpod-rgn → T4 (+ Pod field T2, Endpoint field T6); runpod-18b → T5+T7; runpod-eov → T5+T6; runpod-rz0 → T7.
- The pod controller previously had NO `stringSlicesEqual`; Task 2 adds it — do not import across controller packages.
