#!/usr/bin/env bash

GO=${GO:-go}

${GO} run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen object \
    crd:crdVersions=v1,trivialVersions=false \
    paths="./..." \
    output:crd:artifacts:config=doc/crds

