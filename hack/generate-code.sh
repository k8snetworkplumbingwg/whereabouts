#!/usr/bin/env bash

controller-gen object crd:crdVersions=v1,trivialVersions=false paths="./..." output:crd:artifacts:config=doc
