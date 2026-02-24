#!/bin/sh
set -xe
BASEDIR=$(pwd)

K8S_API_VERSION="v0.34.1"
CONTROLLER_GEN_VERSION="v0.20.0"

# install controller-gen
GOBIN=${BASEDIR}/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}

mkdir -p ${BASEDIR}/bin/code-generator
wget https://github.com/kubernetes/code-generator/archive/refs/tags/${K8S_API_VERSION}.tar.gz
tar -xf ${K8S_API_VERSION}.tar.gz -C ${BASEDIR}/bin/code-generator --strip-components=1
rm ${K8S_API_VERSION}.tar.gz
