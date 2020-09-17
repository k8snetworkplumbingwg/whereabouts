#!/usr/bin/env bash
# single test: go test -v ./pkg/storage/
# without cache: go test -count=1 -v ./pkg/storage/
set -e -x
echo "Linting go code..."
golint ./cmd ./pkg
echo "Running go tests..."
WHEREABOUTS_CRD_DIR="doc" KUBEBUILDER_ASSETS="$(pwd)/kubebuilder-1.16/bin" go test -mod=vendor -v -covermode=count -coverprofile=coverage.out ./...
WHEREABOUTS_CRD_DIR="doc/v1beta1" KUBEBUILDER_ASSETS="$(pwd)/kubebuilder-1.14/bin" go test -v -covermode=count -coverprofile=coverage.out ./...
