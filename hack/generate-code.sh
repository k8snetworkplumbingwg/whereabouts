#!/bin/sh
set -xe

BASEDIR=$(pwd)
${BASEDIR}/bin/controller-gen object crd:crdVersions=v1 paths="./pkg/api/..." output:crd:artifacts:config=deploy/crds output:crd:artifacts:config=deploy/charts/whereabouts/crds
