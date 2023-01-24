#!/bin/bash
BASEDIR=$(pwd)

# install controller-gen
GOBIN=${BASEDIR}/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.1

