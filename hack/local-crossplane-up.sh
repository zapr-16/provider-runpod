#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-provider-runpod-local}"
CROSSPLANE_NAMESPACE="${CROSSPLANE_NAMESPACE:-crossplane-system}"
CROSSPLANE_RELEASE_NAME="${CROSSPLANE_RELEASE_NAME:-crossplane}"
CROSSPLANE_HELM_REPO_NAME="${CROSSPLANE_HELM_REPO_NAME:-crossplane-stable}"
CROSSPLANE_HELM_REPO_URL="${CROSSPLANE_HELM_REPO_URL:-https://charts.crossplane.io/stable}"
CROSSPLANE_HELM_CHART="${CROSSPLANE_HELM_CHART:-crossplane-stable/crossplane}"
CROSSPLANE_HELM_VERSION="${CROSSPLANE_HELM_VERSION:-}"
PROVIDER_IMAGE="${PROVIDER_IMAGE:-provider-runpod:local}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-300s}"
PROVIDER_GOARCH="${PROVIDER_GOARCH:-}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd kind
require_cmd kubectl
require_cmd docker
require_cmd helm
require_cmd make

echo "==> Ensuring kind cluster ${KIND_CLUSTER_NAME} exists"
if ! kind get clusters | grep -qx "${KIND_CLUSTER_NAME}"; then
  kind create cluster --name "${KIND_CLUSTER_NAME}" --wait 120s
fi

echo "==> Installing Crossplane into ${CROSSPLANE_NAMESPACE}"
helm repo add "${CROSSPLANE_HELM_REPO_NAME}" "${CROSSPLANE_HELM_REPO_URL}" >/dev/null 2>&1 || true
helm repo update >/dev/null

HELM_ARGS=(
  upgrade
  --install
  "${CROSSPLANE_RELEASE_NAME}"
  "${CROSSPLANE_HELM_CHART}"
  --namespace
  "${CROSSPLANE_NAMESPACE}"
  --create-namespace
)

if [[ -n "${CROSSPLANE_HELM_VERSION}" ]]; then
  HELM_ARGS+=(--version "${CROSSPLANE_HELM_VERSION}")
fi

helm "${HELM_ARGS[@]}"

echo "==> Waiting for Crossplane core deployments"
kubectl rollout status "deployment/${CROSSPLANE_RELEASE_NAME}" -n "${CROSSPLANE_NAMESPACE}" --timeout="${WAIT_TIMEOUT}"
kubectl rollout status "deployment/${CROSSPLANE_RELEASE_NAME}-rbac-manager" -n "${CROSSPLANE_NAMESPACE}" --timeout="${WAIT_TIMEOUT}"

echo "==> Building provider binary"
if [[ -z "${PROVIDER_GOARCH}" ]]; then
  PROVIDER_GOARCH="$(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')"
fi

CGO_ENABLED=0 GOOS=linux GOARCH="${PROVIDER_GOARCH}" go build -o "${ROOT_DIR}/provider" "${ROOT_DIR}/cmd/provider"

echo "==> Building provider image ${PROVIDER_IMAGE}"
docker build -t "${PROVIDER_IMAGE}" "${ROOT_DIR}"

echo "==> Loading provider image into kind"
kind load docker-image "${PROVIDER_IMAGE}" --name "${KIND_CLUSTER_NAME}"

echo "==> Applying CRDs"
for crd in "${ROOT_DIR}"/package/crds/*.yaml; do
  kubectl apply -f "${crd}"
done

echo "==> Applying local RBAC and provider deployment"
kubectl apply -f "${ROOT_DIR}/deploy/local/rbac.yaml"
kubectl apply -f "${ROOT_DIR}/deploy/local/provider.yaml"
kubectl set image deployment/provider-runpod provider="${PROVIDER_IMAGE}" -n "${CROSSPLANE_NAMESPACE}"
kubectl rollout restart deployment/provider-runpod -n "${CROSSPLANE_NAMESPACE}"
kubectl rollout status deployment/provider-runpod -n "${CROSSPLANE_NAMESPACE}" --timeout="${WAIT_TIMEOUT}"

echo "==> Local Crossplane environment is ready"
