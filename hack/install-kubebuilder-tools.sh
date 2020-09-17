#!/bin/bash

mkdir -p bin

# install controller-gen
go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.2.0
mv $GOPATH/bin/controller-gen bin/controller-gen-0.2.0

go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.0

# install kubebuilder tools to bin/
containerID=$(docker create gcr.io/kubebuilder/thirdparty-linux:1.14.1)
docker cp ${containerID}:/kubebuilder_linux_amd64.tar.gz ./kubebuilder_linux_amd64.tar.gz
docker rm ${containerID}
tar -xzvf kubebuilder_linux_amd64.tar.gz
rm kubebuilder_linux_amd64.tar.gz
mv kubebuilder/bin/* bin/
rm -rf kubebuilder/
chmod +x bin/