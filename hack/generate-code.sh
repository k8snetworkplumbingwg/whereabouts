#!/usr/bin/env bash

controller-gen object crd:trivialVersions=true paths="./..." output:crd:artifacts:config=doc
