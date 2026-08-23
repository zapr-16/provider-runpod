#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

RUNPOD_API_KEY="${RUNPOD_API_KEY:-}"
CROSSPLANE_NAMESPACE="${CROSSPLANE_NAMESPACE:-crossplane-system}"
LOCAL_TEST_NAMESPACE="${LOCAL_TEST_NAMESPACE:-default}"
LOCAL_TEST_ENDPOINT_NAME="${LOCAL_TEST_ENDPOINT_NAME:-vllm-small}"
LOCAL_TEST_CONN_SECRET_NAME="${LOCAL_TEST_CONN_SECRET_NAME:-vllm-small-conn}"
KEEP_RESOURCES="${KEEP_RESOURCES:-0}"
WAIT_TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-600}"
DELETE_WAIT_SECONDS="${DELETE_WAIT_SECONDS:-300}"
HELD_WAIT_SECONDS="${HELD_WAIT_SECONDS:-30}"
POLL_SECONDS="${POLL_SECONDS:-10}"
SECRET_NAME="${SECRET_NAME:-runpod-api-key}"
PROVIDER_CONFIG_NAME="${PROVIDER_CONFIG_NAME:-default}"
RUNPOD_API_BASE="${RUNPOD_API_BASE:-https://rest.runpod.io/v1}"
IN_USE_FINALIZER="in-use.crossplane.io"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "missing required command: kubectl" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "missing required command: curl" >&2
  exit 1
fi

if [[ -z "${RUNPOD_API_KEY}" ]]; then
  echo "RUNPOD_API_KEY not set; skipping local Crossplane endpoint smoke test"
  exit 0
fi

ENDPOINT_ID=""
TEMPLATE_ID=""
CPC_DELETED_WITHOUT_WAIT=0

cleanup() {
  if [[ "${KEEP_RESOURCES}" == "1" ]]; then
    return
  fi

  kubectl delete "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl wait --for=delete "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --timeout="${DELETE_WAIT_SECONDS}s" >/dev/null 2>&1 || true

  if [[ "${CPC_DELETED_WITHOUT_WAIT}" == "1" ]]; then
    kubectl wait --for=delete "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" --timeout="${DELETE_WAIT_SECONDS}s" >/dev/null 2>&1 || true
  else
    kubectl delete -f "${ROOT_DIR}/examples/providerconfig.yaml" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  fi

  kubectl delete secret "${SECRET_NAME}" -n "${CROSSPLANE_NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
}

trap cleanup EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

wait_for_endpoint_ready() {
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local ready=""
  local conn_secret_present=""

  echo "==> Waiting for Endpoint ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_ENDPOINT_NAME} to become Ready"
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    ready="$(kubectl get "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || true)"

    if [[ "${ready}" == "True" ]]; then
      conn_secret_present="$(kubectl get secret "${LOCAL_TEST_CONN_SECRET_NAME}" -n "${LOCAL_TEST_NAMESPACE}" >/dev/null 2>&1 && echo yes || echo no)"
      if [[ "${conn_secret_present}" == "yes" ]]; then
        break
      fi
    fi

    sleep "${POLL_SECONDS}"
  done

  if [[ "${ready}" != "True" ]]; then
    kubectl get "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o yaml || true
    fail "Endpoint ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_ENDPOINT_NAME} did not become Ready within ${WAIT_TIMEOUT_SECONDS}s"
  fi

  if [[ "${conn_secret_present}" != "yes" ]]; then
    fail "connection secret ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_CONN_SECRET_NAME} was not written"
  fi

  ENDPOINT_ID="$(kubectl get "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{.status.atProvider.endpointId}' 2>/dev/null || true)"
  TEMPLATE_ID="$(kubectl get "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" -o jsonpath='{.status.atProvider.templateId}' 2>/dev/null || true)"

  if [[ -z "${ENDPOINT_ID}" ]]; then
    fail "Endpoint ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_ENDPOINT_NAME} has no status.atProvider.endpointId"
  fi
  if [[ -z "${TEMPLATE_ID}" ]]; then
    fail "Endpoint ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_ENDPOINT_NAME} has no status.atProvider.templateId"
  fi

  echo "==> Endpoint ready: endpointId=${ENDPOINT_ID} templateId=${TEMPLATE_ID}"
}

assert_cluster_provider_config_held() {
  local deadline=$((SECONDS + HELD_WAIT_SECONDS))
  local deletion_ts=""
  local finalizers=""
  local users=""

  echo "==> Deleting ClusterProviderConfig ${PROVIDER_CONFIG_NAME} (--wait=false) and expecting it to be HELD"
  kubectl delete "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" --wait=false
  CPC_DELETED_WITHOUT_WAIT=1

  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    deletion_ts="$(kubectl get "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" -o jsonpath='{.metadata.deletionTimestamp}' 2>/dev/null || true)"
    finalizers="$(kubectl get "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" -o jsonpath='{.metadata.finalizers}' 2>/dev/null || true)"
    users="$(kubectl get "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" -o jsonpath='{.status.users}' 2>/dev/null || true)"

    if [[ -n "${deletion_ts}" && "${finalizers}" == *"${IN_USE_FINALIZER}"* && "${users}" == "1" ]]; then
      echo "==> ClusterProviderConfig ${PROVIDER_CONFIG_NAME} is HELD: deletionTimestamp=${deletion_ts} finalizers=${finalizers} users=${users}"
      return 0
    fi

    sleep "${POLL_SECONDS}"
  done

  kubectl get "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" -o yaml || true
  fail "ClusterProviderConfig ${PROVIDER_CONFIG_NAME} was not HELD (deletionTimestamp=${deletion_ts} finalizers=${finalizers} users=${users}) within ${HELD_WAIT_SECONDS}s"
}

assert_cluster_provider_config_deleted_after_endpoint_gone() {
  local deadline=$((SECONDS + DELETE_WAIT_SECONDS))

  echo "==> Deleting Endpoint ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_ENDPOINT_NAME}"
  kubectl delete "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --ignore-not-found --wait=false
  kubectl wait --for=delete "endpoint.runpod.crossplane.io/${LOCAL_TEST_ENDPOINT_NAME}" -n "${LOCAL_TEST_NAMESPACE}" --timeout="${DELETE_WAIT_SECONDS}s" \
    || fail "Endpoint ${LOCAL_TEST_NAMESPACE}/${LOCAL_TEST_ENDPOINT_NAME} did not finish deleting within ${DELETE_WAIT_SECONDS}s"

  echo "==> Waiting for held ClusterProviderConfig ${PROVIDER_CONFIG_NAME} deletion to complete"
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    if ! kubectl get "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" >/dev/null 2>&1; then
      CPC_DELETED_WITHOUT_WAIT=0
      echo "==> ClusterProviderConfig ${PROVIDER_CONFIG_NAME} deletion completed"
      return 0
    fi
    sleep "${POLL_SECONDS}"
  done

  kubectl get "clusterproviderconfig.runpod.crossplane.io/${PROVIDER_CONFIG_NAME}" -o yaml || true
  fail "ClusterProviderConfig ${PROVIDER_CONFIG_NAME} did not finish deleting within ${DELETE_WAIT_SECONDS}s after Endpoint deletion"
}

assert_runpod_resources_gone() {
  local endpoint_status=""
  local template_status=""

  echo "==> Confirming RunPod endpoint ${ENDPOINT_ID} and template ${TEMPLATE_ID} are gone"
  endpoint_status="$(curl -fsS -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer ${RUNPOD_API_KEY}" \
    "${RUNPOD_API_BASE}/endpoints/${ENDPOINT_ID}" || true)"
  template_status="$(curl -fsS -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer ${RUNPOD_API_KEY}" \
    "${RUNPOD_API_BASE}/templates/${TEMPLATE_ID}" || true)"

  if [[ "${endpoint_status}" != "404" ]]; then
    fail "expected RunPod endpoint ${ENDPOINT_ID} to 404 after deletion, got HTTP ${endpoint_status}"
  fi
  if [[ "${template_status}" != "404" ]]; then
    fail "expected RunPod template ${TEMPLATE_ID} to 404 after deletion, got HTTP ${template_status}"
  fi

  echo "==> Confirmed: endpoint and template both 404 on RunPod's REST API"
}

echo "==> Creating RunPod API key secret"
# The key is fed to kubectl via stdin (not --from-literal) so it never
# appears on a command line visible in `ps` output.
kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${SECRET_NAME}
  namespace: ${CROSSPLANE_NAMESPACE}
type: Opaque
stringData:
  apiKey: "${RUNPOD_API_KEY}"
EOF

echo "==> Applying ClusterProviderConfig"
kubectl apply -f "${ROOT_DIR}/examples/providerconfig.yaml"

echo "==> Applying sample Endpoint (workersMin=0, scale-to-zero)"
kubectl apply -f "${ROOT_DIR}/examples/endpoint-vllm.yaml"

wait_for_endpoint_ready
assert_cluster_provider_config_held
assert_cluster_provider_config_deleted_after_endpoint_gone
assert_runpod_resources_gone

echo "==> In-use protection smoke test passed"
