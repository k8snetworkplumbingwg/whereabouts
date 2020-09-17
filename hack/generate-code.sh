#!/usr/bin/env bash

controller-gen object crd:trivialVersions=true paths="./..." output:crd:artifacts:config=doc
mkdir -p doc/v1beta1
./bin/controller-gen-0.2.0 object crd:trivialVersions=true paths="./..." output:crd:artifacts:config=doc/v1beta1
