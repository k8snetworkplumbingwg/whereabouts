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
TIMEOUT_K8="5000s"
RETRY_MAX=10
INTERVAL=10
TIMEOUT=300
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
  local retries=${RETRY_MAX:=5}
  local delay=${INTERVAL:=5}
  local to=${TIMEOUT:=20}
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

# Insert imagePullPolicy under a container image line. BSD sed (macOS) rejects
# GNU sed's same-line `a\` form, so use the POSIX append (backslash, newline).
insert_image_pull_policy() {
  local indent=$1
  local file=$2
  sed "/${indent}image:/a\\
${indent}imagePullPolicy: Never
" "$file"
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
$OCI_BIN build . -t "$IMG_NAME"
popd

echo "## load image into KinD"
trap "rm /tmp/whereabouts-img.tar || true" EXIT
"$OCI_BIN" save -o /tmp/whereabouts-img.tar "$IMG_NAME"
kind load image-archive --name "$KIND_CLUSTER_NAME" /tmp/whereabouts-img.tar

echo "## install whereabouts"
for file in "daemonset-install.yaml" "whereabouts.cni.cncf.io_ippools.yaml" "whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml" "whereabouts.cni.cncf.io_nodeslicepools.yaml" "reconciler-deployment.yaml"; do
  # insert 'imagePullPolicy: Never' under the container 'image' so it is certain that the image used
  # by the daemonset is the one loaded into KinD and not one pulled from a repo
  insert_image_pull_policy '        ' "$ROOT/doc/crds/$file" | retry kubectl apply -f -
done
# deployment has an extra indent, so handle it outside the loop
insert_image_pull_policy '          ' "$ROOT/doc/crds/node-slice-controller.yaml" | retry kubectl apply -f -
retry kubectl wait -n kube-system --for=condition=ready -l app=whereabouts pod --timeout=$TIMEOUT_K8
retry kubectl wait -n kube-system --for=condition=ready -l app=whereabouts-controller pod --timeout=$TIMEOUT_K8
retry kubectl wait -n kube-system --for=condition=ready -l app=whereabouts-reconciler pod --timeout=$TIMEOUT_K8
echo "## done"
