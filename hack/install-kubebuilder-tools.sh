#!/bin/bash
OCI_BIN=${OCI_BIN:-docker}

# install controller-gen
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.1

# install kubebuilder tools to bin/
mkdir -p bin
containerID=$("$OCI_BIN" create gcr.io/kubebuilder/thirdparty-linux:1.16.4)
"$OCI_BIN" cp ${containerID}:/kubebuilder_linux_amd64.tar.gz ./kubebuilder_linux_amd64.tar.gz
"$OCI_BIN" rm ${containerID}
tar -xzvf kubebuilder_linux_amd64.tar.gz
rm kubebuilder_linux_amd64.tar.gz
mv kubebuilder/bin/* bin/
rm -rf kubebuilder/
chmod +x bin/

