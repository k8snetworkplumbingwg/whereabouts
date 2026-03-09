#!/bin/bash
# SPDX-FileCopyrightText: 2025 Deutsche Telekom AG
# SPDX-License-Identifier: Apache-2.0

# Sets up a KinD cluster and installs whereabouts via Helm chart.
# Usage: hack/e2e-setup-kind-cluster-helm.sh -n <number-of-compute-nodes>
set -eo pipefail

NUMBER_OF_COMPUTE_NODES=""

while [ $# -gt 0 ]; do
  case "$1" in
    -n|--number-of-compute)
      if [ -z "${2:-}" ]; then
        echo "option '$1' requires an argument: -n <number-of-compute-nodes>" >&2
        exit 1
      fi
      NUMBER_OF_COMPUTE_NODES=$2
      shift 2
      ;;
    --)
      shift
      break
      ;;
    *)
      echo "Usage: $0 -n <number-of-compute-nodes>" >&2
      exit 1
      ;;
  esac
done

if [ -z "${NUMBER_OF_COMPUTE_NODES:-}" ]; then
  echo "Missing required argument: -n <number-of-compute-nodes>" >&2
  exit 1
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
MULTUS_DAEMONSET_URL="https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/deployments/multus-daemonset.yml"
CNIS_DAEMONSET_PATH="$ROOT/hack/cni-install.yml"
RETRY_MAX=10
INTERVAL=10
TIMEOUT=120
TIMEOUT_K8="${TIMEOUT}s"
KIND_CLUSTER_NAME="whereabouts"
OCI_BIN="${OCI_BIN:-"docker"}"
IMG_PROJECT="whereabouts"
IMG_REGISTRY="ghcr.io/telekom"
IMG_TAG="latest"
IMG_NAME="$IMG_REGISTRY/$IMG_PROJECT:$IMG_TAG"
HELM_RELEASE_NAME="whereabouts"
HELM_NAMESPACE="kube-system"
CHART_DIR="$ROOT/deployment/whereabouts-chart"

create_cluster() {
  workers="$(for i in $(seq $NUMBER_OF_COMPUTE_NODES); do echo "  - role: worker"; done)"
  # deploy cluster with kind
  cat <<EOF | kind create cluster --name $KIND_CLUSTER_NAME --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
$workers
EOF
}


check_requirements() {
  for cmd in "$OCI_BIN" kind kubectl helm; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "$cmd is not available"
      exit 1
    fi
  done
  # Use GNU timeout (gtimeout on macOS via coreutils)
  if command -v timeout &> /dev/null; then
    TIMEOUT_CMD="timeout"
  elif command -v gtimeout &> /dev/null; then
    TIMEOUT_CMD="gtimeout"
  else
    echo "timeout (or gtimeout) is not available; install coreutils"
    exit 1
  fi
}

retry() {
  local status=0
  local retries=${RETRY_MAX:-5}
  local delay=${INTERVAL:-5}
  local to=${TIMEOUT:-20}
  cmd="$*"

  while [ $retries -gt 0 ]
  do
    status=0
    $TIMEOUT_CMD $to bash -c "echo $cmd && $cmd" || status=$?
    if [ $status -eq 0 ]; then
      break;
    fi
    echo "Exit code: '$status'. Sleeping '$delay' seconds before retrying"
    sleep $delay
    retries=$((retries - 1))
  done
  return $status
}

echo "## checking requirements"
check_requirements
echo "## delete existing KinD cluster if it exists"
kind delete clusters $KIND_CLUSTER_NAME
echo "## start KinD cluster"
create_cluster
kind export kubeconfig --name $KIND_CLUSTER_NAME
echo "## wait for coreDNS"
kubectl -n kube-system wait --for=condition=available deploy/coredns --timeout=$TIMEOUT_K8
echo "## install multus"
retry kubectl create -f "${MULTUS_DAEMONSET_URL}"
retry kubectl -n kube-system wait --for=condition=ready -l name="multus" pod --timeout=$TIMEOUT_K8
echo "## install CNIs"
retry kubectl create -f "${CNIS_DAEMONSET_PATH}"
retry kubectl -n kube-system wait --for=condition=ready -l name="cni-plugins" pod --timeout=$TIMEOUT_K8
echo "## build whereabouts"
pushd "$ROOT"
$OCI_BIN build --load -t "$IMG_NAME" -f Dockerfile .
popd

echo "## load image into KinD"
trap "rm -f /tmp/whereabouts-img.tar" EXIT
"$OCI_BIN" save -o /tmp/whereabouts-img.tar "$IMG_NAME"
kind load image-archive --name "$KIND_CLUSTER_NAME" /tmp/whereabouts-img.tar

echo "## install whereabouts via Helm"
helm install "$HELM_RELEASE_NAME" "$CHART_DIR" \
  --namespace "$HELM_NAMESPACE" \
  --set image.repository="$IMG_REGISTRY/$IMG_PROJECT" \
  --set image.tag="$IMG_TAG" \
  --set image.pullPolicy=Never \
  --set operator.enabled=true \
  --set operator.replicas=1 \
  --wait --timeout "${TIMEOUT}s"

retry kubectl wait -n "$HELM_NAMESPACE" --for=condition=ready -l name=whereabouts pod --timeout=$TIMEOUT_K8
retry kubectl wait -n "$HELM_NAMESPACE" --for=condition=ready -l control-plane=controller-manager pod --timeout=$TIMEOUT_K8
echo "## done"
