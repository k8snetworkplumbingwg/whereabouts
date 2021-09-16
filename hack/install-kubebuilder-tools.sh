#!/bin/bash

# install controller-gen
go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.1

# install kubebuilder tools to bin/
mkdir -p bin
containerID=$(docker create gcr.io/kubebuilder/thirdparty-linux:1.16.4)
docker cp ${containerID}:/kubebuilder_linux_amd64.tar.gz ./kubebuilder_linux_amd64.tar.gz
docker rm ${containerID}
tar -xzvf kubebuilder_linux_amd64.tar.gz
rm kubebuilder_linux_amd64.tar.gz
mv kubebuilder/bin/* bin/
rm -rf kubebuilder/
chmod +x bin/