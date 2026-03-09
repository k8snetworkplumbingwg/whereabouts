# ---------------------------------------------------------------
# Variables
# ---------------------------------------------------------------
CURPATH=$(PWD)
BIN_DIR=$(CURPATH)/bin

IMAGE_NAME ?= whereabouts
IMAGE_REGISTRY ?= ghcr.io/telekom
IMAGE_PULL_POLICY ?= Always
IMAGE_TAG ?= latest
COMPUTE_NODES ?= 2

OCI_BIN ?= docker
GO ?= go

# Tool versions
CONTROLLER_GEN_VERSION ?= v0.20.0
STATICCHECK_VERSION ?= v0.6.0
KUSTOMIZE_VERSION ?= v5.6.0
GOLANGCI_LINT_VERSION ?= v2.1.5

# Resolved tool paths
CONTROLLER_GEN := $(BIN_DIR)/controller-gen
STATICCHECK := $(BIN_DIR)/staticcheck
KUSTOMIZE := $(BIN_DIR)/kustomize
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint

# ---------------------------------------------------------------
# Version information
# ---------------------------------------------------------------
GIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_TREE_STATE := $(shell test -n "$$(git status --porcelain --untracked-files=no 2>/dev/null)" && echo dirty || echo clean)
GIT_TAG := $(shell git describe --tags --abbrev=0 --exact-match 2>/dev/null)
GIT_TAG_LAST := $(shell git describe --tags --abbrev=0 2>/dev/null)
VERSION ?= $(GIT_TAG_LAST)
RELEASE_STATUS := $(if $(strip $(VERSION)$(GIT_TAG)),released,unreleased)
VERSION_PKG := github.com/k8snetworkplumbingwg/whereabouts/pkg/version
LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) \
           -X $(VERSION_PKG).GitSHA=$(GIT_SHA) \
           -X $(VERSION_PKG).GitTreeState=$(GIT_TREE_STATE) \
           -X $(VERSION_PKG).ReleaseStatus=$(RELEASE_STATUS)

# ---------------------------------------------------------------
# General
# ---------------------------------------------------------------

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate CRDs, RBAC, and webhook manifests (config/).
	$(CONTROLLER_GEN) rbac:roleName=whereabouts-operator \
		crd:crdVersions=v1 \
		webhook \
		paths="./api/...;./internal/..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac \
		output:webhook:artifacts:config=config/webhook
	cp config/crd/bases/whereabouts.cni.cncf.io_*.yaml deployment/whereabouts-chart/crds/

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate deepcopy and clientsets/informers/listers.
	$(CONTROLLER_GEN) object paths="./api/whereabouts.cni.cncf.io/..."
	hack/update-codegen.sh
	rm -rf github.com

.PHONY: generate-api
generate-api: manifests generate ## Generate all API artifacts (CRDs + deepcopy + clientsets).

.PHONY: verify-codegen
verify-codegen: ## Verify generated code is up to date.
	hack/verify-codegen.sh

.PHONY: fmt
fmt: ## Run go fmt against code.
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	$(GO) vet ./cmd/... ./pkg/... ./internal/... ./api/whereabouts.cni.cncf.io/...

##@ Testing

.PHONY: test
test: build manifests generate vet lint-staticcheck ## Run unit tests (includes build, vet, staticcheck).
	$(GO) test -v -race -covermode=atomic -coverprofile=coverage.out \
		$$($(GO) list ./... | grep -v e2e | tr "\n" " ")

.PHONY: test-skip-static
test-skip-static: build vet ## Run tests without staticcheck (faster iteration).
	$(GO) test -v -race -covermode=atomic -coverprofile=coverage.out \
		$$($(GO) list ./... | grep -v e2e | tr "\n" " ")

##@ Linting

.PHONY: lint-staticcheck
lint-staticcheck: $(STATICCHECK) ## Run staticcheck.
	$(STATICCHECK) ./...

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint.
	$(GOLANGCI_LINT) run --timeout=5m ./...

.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT) ## Run golangci-lint with auto-fix.
	$(GOLANGCI_LINT) run --timeout=5m --fix ./...

##@ Build

.PHONY: build
build: ## Build CNI plugin, operator, and install-cni binaries.
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/whereabouts ./cmd/whereabouts/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/whereabouts-operator ./cmd/operator/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/install-cni ./cmd/install-cni/

.PHONY: run
run: ## Run the operator controller locally against the configured cluster.
	$(GO) run ./cmd/operator/ controller

.PHONY: docker-build
docker-build: ## Build container image.
	$(OCI_BIN) build --build-arg VERSION=$(IMAGE_TAG) --build-arg GIT_SHA=$(GIT_SHA) -t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .

.PHONY: docker-push
docker-push: ## Push container image.
	$(OCI_BIN) push $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: docker-buildx
docker-buildx: ## Build and push container image for cross-platform support.
	$(OCI_BIN) buildx build --platform linux/amd64,linux/arm64 --push --build-arg VERSION=$(IMAGE_TAG) --build-arg GIT_SHA=$(GIT_SHA) -t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .

##@ Deployment

.PHONY: install
install: manifests $(KUSTOMIZE) ## Install CRDs into the cluster.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests $(KUSTOMIZE) ## Uninstall CRDs from the cluster.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found -f -

.PHONY: deploy
deploy: manifests $(KUSTOMIZE) ## Deploy controller to the cluster.
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: $(KUSTOMIZE) ## Undeploy controller from the cluster.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found -f -

.PHONY: kind
kind: ## Create a KinD cluster with whereabouts installed (kustomize).
	hack/e2e-setup-kind-cluster.sh -n $(COMPUTE_NODES)

.PHONY: kind-helm
kind-helm: ## Create a KinD cluster with whereabouts installed (Helm).
	hack/e2e-setup-kind-cluster-helm.sh -n $(COMPUTE_NODES)

##@ Dependencies

.PHONY: update-deps
update-deps: ## Update Go dependencies.
	$(GO) mod tidy
	$(GO) mod vendor
	$(GO) mod verify

# ---------------------------------------------------------------
# Tool installation
# ---------------------------------------------------------------
$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

$(CONTROLLER_GEN): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

$(STATICCHECK): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

$(KUSTOMIZE): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Install kustomize binary.

$(GOLANGCI_LINT): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

YQ=$(BIN_DIR)/yq
YQ_VERSION=v4.44.1
$(YQ): | $(BIN_DIR); $(info installing yq)
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]') && ARCH=$$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/' | sed 's/arm64/arm64/') && \
	curl -fsSL -o $(YQ) https://github.com/mikefarah/yq/releases/download/$(YQ_VERSION)/yq_$${OS}_$${ARCH} && chmod +x $(YQ)

# ---------------------------------------------------------------
# Release
# ---------------------------------------------------------------

##@ Release

.PHONY: chart-prepare-release
chart-prepare-release: | $(YQ) ; ## Prepare chart for release.
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-update.sh

.PHONY: chart-push-release
chart-push-release: ## Push release chart.
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-push.sh
