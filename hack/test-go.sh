#!/usr/bin/env bash
# single test: go test -v ./pkg/storage/
# without cache: go test -count=1 -v ./pkg/storage/
set -e -x
echo "Linting go code..."
golint ./cmd ./pkg
echo "Running go tests..."
KUBEBUILDER_ASSETS=bin/ go test -v -covermode=count -coverprofile=coverage.out ./...
