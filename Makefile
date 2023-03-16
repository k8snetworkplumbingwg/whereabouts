IMAGE_NAME ?= whereabouts
IMAGE_REGISTRY ?= ghcr.io/k8snetworkplumbingwg
IMAGE_PULL_POLICY ?= Always
IMAGE_TAG ?= latest
COMPUTE_NODES ?= 2

OCI_BIN ?= docker

build:
	hack/build-go.sh

docker-build:
	$(OCI_BIN) build -t ${IMAGE_REGISTRY}/${IMAGE_NAME}:${IMAGE_TAG} -f Dockerfile .

generate-api:
	hack/verify-codegen.sh
	rm -rf github.com

install-tools:
	hack/install-kubebuilder-tools.sh

test: build install-tools
	hack/test-go.sh

kind:
	hack/e2e-setup-kind-cluster.sh -n $(COMPUTE_NODES)
