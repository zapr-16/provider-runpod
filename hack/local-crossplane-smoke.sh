#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

RUNPOD_API_KEY="${RUNPOD_API_KEY:-}"
RUNPOD_GPU_TYPE_ID="${RUNPOD_GPU_TYPE_ID:-}"
RUNPOD_IMAGE_NAME="${RUNPOD_IMAGE_NAME:-python:3.11-slim}"
RUNPOD_GPU_COUNT="${RUNPOD_GPU_COUNT:-1}"
RUNPOD_CLOUD_TYPE="${RUNPOD_CLOUD_TYPE:-SECURE}"
RUNPOD_SUPPORT_PUBLIC_IP="${RUNPOD_SUPPORT_PUBLIC_IP:-false}"
RUNPOD_SMOKE_CANDIDATES="${RUNPOD_SMOKE_CANDIDATES:-}"
CROSSPLANE_NAMESPACE="${CROSSPLANE_NAMESPACE:-crossplane-system}"
LOCAL_TEST_NAMESPACE="${LOCAL_TEST_NAMESPACE:-default}"
LOCAL_TEST_POD_NAME="${LOCAL_TEST_POD_NAME:-provider-runpod-local-smoke}"
KEEP_RESOURCES="${KEEP_RESOURCES:-0}"
WAIT_TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-300}"
DELETE_WAIT_SECONDS="${DELETE_WAIT_SECONDS:-300}"
POLL_SECONDS="${POLL_SECONDS:-10}"
SECRET_NAME="${SECRET_NAME:-runpod-api-key}"
PROVIDER_CONFIG_NAME="${PROVIDER_CONFIG_NAME:-default}"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "missing required command: kubectl" >&2
  exit 1
fi

if [[ -z "${RUNPOD_API_KEY}" ]]; then
  echo "RUNPOD_API_KEY not set; skipping local Crossplane smoke test"
  exit 0
fi

"${ROOT_DIR}/hack/local-crossplane-up.sh"

TEMP_MANIFEST="$(mktemp)"
candidate_specs=()
cleanup() {
  if [[ "${KEEP_RESOURCES}" == "1" ]]; then
    rm -f "${TEMP_MANIFEST}"
    return
  fi

  kubectl delete -f "${TEMP_MANIFEST}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl wait --for=delete "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --timeout="${DELETE_WAIT_SECONDS}s" >/dev/null 2>&1 || true
  if kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" >/dev/null 2>&1; then
    echo "managed Pod ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_POD_NAME} is still finalizing; leaving ProviderConfig and Secret in place for controller cleanup" >&2
  else
    kubectl delete -f "${ROOT_DIR}/examples/providerconfig.yaml" --ignore-not-found >/dev/null 2>&1 || true
    kubectl delete secret "${SECRET_NAME}" -n "${CROSSPLANE_NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
  fi
  rm -f "${TEMP_MANIFEST}"
}

trap cleanup EXIT

add_candidate() {
  local cloud_type="$1"
  local gpu_type_id="$2"
  local support_public_ip="$3"

  if [[ -z "${cloud_type}" || -z "${gpu_type_id}" ]]; then
    return
  fi

  candidate_specs+=("${cloud_type}|${gpu_type_id}|${support_public_ip}")
}

render_manifest() {
  local cloud_type="$1"
  local gpu_type_id="$2"
  local support_public_ip="$3"

  cat >"${TEMP_MANIFEST}" <<EOF
apiVersion: runpod.crossplane.io/v1alpha1
kind: Pod
metadata:
  name: ${LOCAL_TEST_POD_NAME}
  namespace: ${LOCAL_TEST_NAMESPACE}
  labels:
    app.kubernetes.io/name: provider-runpod-local-smoke
spec:
  providerConfigRef:
    name: ${PROVIDER_CONFIG_NAME}
  forProvider:
    imageName: ${RUNPOD_IMAGE_NAME}
    gpuTypeIds:
      - ${gpu_type_id}
    gpuCount: ${RUNPOD_GPU_COUNT}
    cloudType: ${cloud_type}
    supportPublicIp: ${support_public_ip}
    dockerStartCmd:
      - python
      - -m
      - http.server
      - "8888"
    ports:
      - number: 8888
        protocol: http
EOF
}

wait_for_delete() {
  kubectl delete "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl wait --for=delete "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --timeout="${DELETE_WAIT_SECONDS}s" >/dev/null 2>&1 || true

  if kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" >/dev/null 2>&1; then
    echo "managed Pod ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_POD_NAME} is still present after delete wait" >&2
    return 1
  fi

  return 0
}

attempt_smoke() {
  local cloud_type="$1"
  local gpu_type_id="$2"
  local support_public_ip="$3"
  local deadline=""
  local external_name=""
  local desired_status=""
  local public_ip=""
  local port_mapping=""
  local synced_message=""

  wait_for_delete
  render_manifest "${cloud_type}" "${gpu_type_id}" "${support_public_ip}"

  echo "==> Trying smoke candidate cloud=${cloud_type} gpu=${gpu_type_id} supportPublicIp=${support_public_ip}"
  kubectl apply -f "${TEMP_MANIFEST}"

  deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))

  echo "==> Waiting for provider controller to create an external Pod"
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    external_name="$(kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{.metadata.annotations.crossplane\.io/external-name}' 2>/dev/null || true)"
    if [[ -n "${external_name}" && "${external_name}" != "${LOCAL_TEST_POD_NAME}" ]]; then
      break
    fi
    sleep "${POLL_SECONDS}"
  done

  if [[ -z "${external_name}" || "${external_name}" == "${LOCAL_TEST_POD_NAME}" ]]; then
    echo "timed out waiting for external-name on ${LOCAL_TEST_POD_NAME}" >&2
    kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o yaml || true
    return 1
  fi

  echo "==> Waiting for Pod networking readiness"
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    desired_status="$(kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{.status.atProvider.desiredStatus}' 2>/dev/null || true)"
    public_ip="$(kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{.status.atProvider.publicIp}' 2>/dev/null || true)"
    port_mapping="$(kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{.status.atProvider.portMappings.8888\/http}' 2>/dev/null || true)"
    synced_message="$(kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{range .status.conditions[?(@.type=="Synced")]}{.message}{end}' 2>/dev/null || true)"

    if [[ "${desired_status}" == "RUNNING" && -n "${public_ip}" && -n "${port_mapping}" ]]; then
      echo "==> Crossplane observed pod successfully"
      echo "external name: ${external_name}"
      echo "cloud type: ${cloud_type}"
      echo "gpu type id: ${gpu_type_id}"
      echo "support public ip: ${support_public_ip}"
      echo "desired status: ${desired_status}"
      echo "public ip: ${public_ip}"
      echo "mapped http port: ${port_mapping}"
      return 0
    fi

    if [[ "${synced_message}" == *"does not have the resources to deploy your pod"* ]]; then
      echo "candidate failed fast due to RunPod capacity: ${synced_message}" >&2
      kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o yaml || true
      return 2
    fi

    sleep "${POLL_SECONDS}"
  done

  echo "pod ${external_name} did not become network-ready within ${WAIT_TIMEOUT_SECONDS}s" >&2
  kubectl get "pod.runpod.crossplane.io/${LOCAL_TEST_POD_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o yaml || true
  return 1
}

echo "==> Creating RunPod API key secret"
kubectl create secret generic "${SECRET_NAME}" \
  --namespace "${CROSSPLANE_NAMESPACE}" \
  --from-literal=apiKey="${RUNPOD_API_KEY}" \
  --dry-run=client \
  -o yaml | kubectl apply -f -

echo "==> Applying ProviderConfig and sample Pod"
kubectl apply -f "${ROOT_DIR}/examples/providerconfig.yaml"

if [[ -n "${RUNPOD_SMOKE_CANDIDATES}" ]]; then
  IFS=';' read -r -a custom_candidates <<<"${RUNPOD_SMOKE_CANDIDATES}"
  for candidate in "${custom_candidates[@]}"; do
    IFS='|' read -r cloud_type gpu_type_id support_public_ip <<<"${candidate}"
    add_candidate "${cloud_type}" "${gpu_type_id}" "${support_public_ip:-false}"
  done
else
  add_candidate "${RUNPOD_CLOUD_TYPE}" "${RUNPOD_GPU_TYPE_ID:-NVIDIA RTX A4000}" "${RUNPOD_SUPPORT_PUBLIC_IP}"
  add_candidate "SECURE" "NVIDIA RTX A4500" "false"
  add_candidate "SECURE" "NVIDIA GeForce RTX 3090" "false"
  add_candidate "SECURE" "NVIDIA L4" "false"
  add_candidate "COMMUNITY" "NVIDIA RTX A4000" "true"
fi

for candidate in "${candidate_specs[@]}"; do
  IFS='|' read -r cloud_type gpu_type_id support_public_ip <<<"${candidate}"
  if attempt_smoke "${cloud_type}" "${gpu_type_id}" "${support_public_ip}"; then
    exit 0
  fi
done

echo "all smoke candidates failed" >&2
exit 1
