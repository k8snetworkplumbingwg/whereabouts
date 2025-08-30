CURPATH=$(PWD)
BIN_DIR=$(CURPATH)/bin

IMAGE_NAME ?= whereabouts
IMAGE_REGISTRY ?= ghcr.io/k8snetworkplumbingwg
IMAGE_PULL_POLICY ?= Always
IMAGE_TAG ?= latest
COMPUTE_NODES ?= 2

OCI_BIN ?= docker


build:
	hack/build-go.sh

docker-build:
	$(OCI_BIN) build -t ${IMAGE_REGISTRY}/${IMAGE_NAME}:${IMAGE_TAG} -f Dockerfile --platform linux/amd64 .

generate-api:
	hack/verify-codegen.sh
	rm -rf github.com

install-tools:
	hack/install-kubebuilder-tools.sh

test: build install-tools
	hack/test-go.sh 

test-skip-static: build
	hack/test-go.sh --skip-static-check 

kind:
	hack/e2e-setup-kind-cluster.sh -n $(COMPUTE_NODES)

update-deps:
	go mod tidy
	go mod vendor
	go mod verify

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

YQ=$(BIN_DIR)/yq
YQ_VERSION=v4.44.1
$(YQ): | $(BIN_DIR); $(info installing yq)
	@curl -fsSL -o $(YQ) https://github.com/mikefarah/yq/releases/download/$(YQ_VERSION)/yq_linux_amd64 && chmod +x $(YQ)

.PHONY: chart-prepare-release
chart-prepare-release: | $(YQ) ; ## prepare chart for release
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-update.sh

.PHONY: chart-push-release
chart-push-release: ## push release chart
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-push.sh
