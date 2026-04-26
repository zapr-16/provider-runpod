#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-provider-runpod-local}"
CROSSPLANE_NAMESPACE="${CROSSPLANE_NAMESPACE:-crossplane-system}"
DELETE_CLUSTER="${DELETE_CLUSTER:-1}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-300s}"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "missing required command: kubectl" >&2
  exit 1
fi

if ! command -v kind >/dev/null 2>&1; then
  echo "missing required command: kind" >&2
  exit 1
fi

if kind get clusters | grep -qx "${KIND_CLUSTER_NAME}"; then
  echo "==> Deleting managed Pod resources to trigger remote cleanup"
  kubectl delete pods.runpod.crossplane.io --all --all-namespaces --ignore-not-found --wait=false || true
  kubectl wait --for=delete pods.runpod.crossplane.io --all --all-namespaces --timeout="${WAIT_TIMEOUT}" || true

  echo "==> Removing local provider deployment"
  kubectl delete -f "${ROOT_DIR}/deploy/local/provider.yaml" --ignore-not-found || true
  kubectl delete -f "${ROOT_DIR}/deploy/local/rbac.yaml" --ignore-not-found || true
  kubectl delete providerconfig.runpod.crossplane.io/default --ignore-not-found || true
  kubectl delete secret/runpod-api-key -n "${CROSSPLANE_NAMESPACE}" --ignore-not-found || true
fi

if [[ "${DELETE_CLUSTER}" == "1" ]]; then
  echo "==> Deleting kind cluster ${KIND_CLUSTER_NAME}"
  kind delete cluster --name "${KIND_CLUSTER_NAME}" || true
else
  echo "==> Keeping kind cluster ${KIND_CLUSTER_NAME}"
fi
