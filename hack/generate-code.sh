#!/usr/bin/env bash

BASEDIR=$(pwd)
${BASEDIR}/bin/controller-gen object crd:crdVersions=v1,trivialVersions=false paths="./..." output:crd:artifacts:config=doc/crds
