#!/usr/bin/env bash
# single test: go test -v ./pkg/storage/
# without cache: go test -count=1 -v ./pkg/storage/
set -e -x

GO=${GO:-go}

echo "Running go vet ..."
${GO} vet --tags=test ./cmd/... ./pkg/...


echo "Running golang staticcheck ..."
staticcheck --tags=test ./...

echo "random print"

echo "Running go tests..."
KUBEBUILDER_ASSETS="$(pwd)/bin" ${GO} test \
    --tags=test \
    -v \
    -covermode=count \
    -coverprofile=coverage.out \
    $(${GO} list ./... | grep -v e2e | tr "\n" " ")
