#!/bin/bash
set -o errexit
# ensure this file is sourced to add required components to PATH

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"
VERSION="v0.10.0"
KIND_BINARY_URL="https://github.com/kubernetes-sigs/kind/releases/download/${VERSION}/kind-$(uname)-amd64"
K8_STABLE_RELEASE_URL="https://storage.googleapis.com/kubernetes-release/release/stable.txt"

if [ ! -d "${root}/bin" ]; then
    mkdir "${root}/bin"
fi

echo "retrieving kind"
curl --max-time 10 --retry 10 --retry-delay 5 --retry-max-time 60 -Lo "${root}/bin/kind" "${KIND_BINARY_URL}"
chmod +x "${root}/bin/kind"

echo "retrieving kubectl"
curl -Lo "${root}/bin/kubectl" "https://storage.googleapis.com/kubernetes-release/release/$(curl -s ${K8_STABLE_RELEASE_URL})/bin/linux/amd64/kubectl"
chmod +x "${root}/bin/kubectl"

export PATH="$PATH:$root/bin"
