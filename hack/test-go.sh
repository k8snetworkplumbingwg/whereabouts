#!/usr/bin/env bash
# single test: go test -v ./pkg/storage/
# without cache: go test -count=1 -v ./pkg/storage/
set -e -x
echo "Running go vet ..."
go vet ./cmd/... ./pkg/...

echo "Running golang staticcheck ..."
staticcheck ./...

echo "Running go tests..."
KUBEBUILDER_ASSETS="$(pwd)/bin" go test -v -covermode=count -coverprofile=coverage.out $(go list ./... | grep -v e2e | tr "\n" " ")
