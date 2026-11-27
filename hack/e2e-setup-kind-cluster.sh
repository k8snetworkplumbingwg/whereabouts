#!/bin/bash
set -eo pipefail

while true; do
  case "$1" in
    -n|--number-of-compute)
      NUMBER_OF_COMPUTE_NODES=$2
      break
      ;;
    *)
      echo "define argument -n (number of compute nodes)"
      exit 1
  esac
done

# macOS/BSD `readlink` does not support GNU `--canonicalize`, so compute paths
# portably using `realpath` (preferred) or Python.
HERE="$(
  if command -v realpath >/dev/null 2>&1; then
    dirname "$(realpath "${BASH_SOURCE[0]}")"
  else
    dirname "$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "${BASH_SOURCE[0]}")"
  fi
)"
ROOT="$(
  if command -v realpath >/dev/null 2>&1; then
    realpath "$HERE/.."
  else
    python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$HERE/.."
  fi
)"
MULTUS_DAEMONSET_URL="https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/deployments/multus-daemonset.yml"
CNIS_DAEMONSET_PATH="$ROOT/hack/cni-install.yml"
RETRY_MAX=10
INTERVAL=10
TIMEOUT=120
TIMEOUT_K8="${TIMEOUT}s"
KIND_CLUSTER_NAME="whereabouts"
OCI_BIN="${OCI_BIN:-"docker"}"
IMG_PROJECT="whereabouts"
IMG_REGISTRY="ghcr.io/k8snetworkplumbingwg"
IMG_TAG="latest"
IMG_NAME="$IMG_REGISTRY/$IMG_PROJECT:$IMG_TAG"

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
  for cmd in "$OCI_BIN" kind kubectl; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "$cmd is not available"
      exit 1
    fi
  done
  # GNU timeout is not part of stock macOS; perl is the fallback below.
  if ! command -v timeout >/dev/null 2>&1 && ! command -v perl >/dev/null 2>&1; then
    echo "timeout or perl is required"
    exit 1
  fi
}

# Run a command with a deadline. Prefer GNU timeout, which is missing from
# stock macOS, and fall back to perl (available at /usr/bin/perl).
with_timeout() {
  local seconds=$1
  shift
  if command -v timeout >/dev/null 2>&1; then
    timeout "$seconds" "$@"
    return $?
  fi

  perl -e '
    my $seconds = shift @ARGV;
    my $pid = fork();
    die "fork: $!\n" unless defined $pid;
    if ($pid == 0) {
      setpgrp(0, 0);
      exec @ARGV or die "exec: $!\n";
    }
    local $SIG{ALRM} = sub {
      kill "TERM", -$pid;
      exit 124;
    };
    alarm $seconds;
    waitpid($pid, 0);
    alarm 0;
    my $status = $?;
    if ($status & 127) {
      exit 128 + ($status & 127);
    }
    exit($status >> 8);
  ' "$seconds" "$@"
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
    with_timeout "$to" bash -c "echo $cmd && $cmd" || status=$?
    if [ $status -eq 0 ]; then
      break;
    fi
    echo "Exit code: '$status'. Sleeping '$delay' seconds before retrying"
    sleep $delay
    let retries--
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

echo "## install CRDs"
for crd in "$ROOT/config/crd/bases/whereabouts.cni.cncf.io_"*.yaml; do
  retry kubectl apply -f "$crd"
done

echo "## install whereabouts"
# Build kustomize output, substitute the locally built image, and add imagePullPolicy: Never
pushd "$ROOT"
make kustomize
bin/kustomize build config/default | \
  sed "s|ghcr.io/k8snetworkplumbingwg/whereabouts:[^ \"]*|$IMG_NAME|g" | \
  awk '/^[[:space:]]*image:/{print; indent=substr($0,1,match($0,/image:/)-1); print indent "imagePullPolicy: Never"; next}1' | \
  retry kubectl apply -f -
popd
retry kubectl wait -n kube-system --for=condition=ready -l name=whereabouts pod --timeout=$TIMEOUT_K8
retry kubectl wait -n kube-system --for=condition=ready -l control-plane=controller-manager pod --timeout=$TIMEOUT_K8
echo "## done"
