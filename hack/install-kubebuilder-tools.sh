#!/bin/bash
OCI_BIN=${OCI_BIN:-docker}

VERSION=1.22.1
BASEDIR=$(pwd)
OSTYPE=$(uname -s | tr '[:upper:]' '[:lower:]')

# install controller-gen
GOBIN=${BASEDIR}/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.1

# install kubebuilder tools to bin/
mkdir -p bin
containerID=$("$OCI_BIN" create gcr.io/kubebuilder/thirdparty-${OSTYPE}-amd64:${VERSION})
"$OCI_BIN" cp ${containerID}:/kubebuilder_${OSTYPE}_amd64.tar.gz ./kubebuilder_${OSTYPE}_amd64.tar.gz
"$OCI_BIN" rm ${containerID}
tar -xzvf kubebuilder_${OSTYPE}_amd64.tar.gz
rm kubebuilder_${OSTYPE}_amd64.tar.gz
mv kubebuilder/bin/* bin/
rm -rf kubebuilder/
chmod +x bin/

