#!/bin/bash

mkdir -p bin

# install controller-gen v0.2.0
# not easy to go install different versions (without breaking this repository's go.mod), so create a new temp directory: https://github.com/golang/go/issues/40276
mkdir -p /tmp/controller-gen && cd /tmp/controller-gen && go mod init deps; go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.2.0; cd -
mv $GOPATH/bin/controller-gen bin/controller-gen-0.2.0
# install controller-gen v0.4.0
mkdir -p /tmp/controller-gen-0.4.0 && cd /tmp/controller-gen-0.4.0 && go mod init deps; go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.0; cd -
mv $GOPATH/bin/controller-gen bin/controller-gen-0.4.0

# install kubebuilder-1.14 tools
containerID=$(docker create gcr.io/kubebuilder/thirdparty-linux:1.14.1)
docker cp ${containerID}:/kubebuilder_linux_amd64.tar.gz ./kubebuilder_linux_amd64.tar.gz
docker rm ${containerID}
tar -xzvf kubebuilder_linux_amd64.tar.gz
rm kubebuilder_linux_amd64.tar.gz
mv kubebuilder kubebuilder-1.14
chmod +x kubebuilder-1.14/bin/

# install kubebuilder-1.16 tools
containerID=$(docker create gcr.io/kubebuilder/thirdparty-linux:1.16.4)
docker cp ${containerID}:/kubebuilder_linux_amd64.tar.gz ./kubebuilder_linux_amd64.tar.gz
docker rm ${containerID}
tar -xzvf kubebuilder_linux_amd64.tar.gz
rm kubebuilder_linux_amd64.tar.gz
mv kubebuilder kubebuilder-1.16
chmod +x kubebuilder-1.16/bin/

chmod +x bin/