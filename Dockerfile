FROM golang:1.26.1@sha256:e2ddb153f786ee6210bf8c40f7f35490b3ff7d38be70d1a0d358ba64225f6428 AS builder
WORKDIR /go/src/github.com/k8snetworkplumbingwg/whereabouts
# Version information injected at build time via --build-arg
ARG VERSION=""
ARG GIT_SHA=""
ARG GIT_TREE_STATE="clean"
ARG RELEASE_STATUS="unreleased"
# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
COPY vendor/ vendor/
# Copy source code
COPY . .
RUN VERSION_LDFLAGS="-X github.com/k8snetworkplumbingwg/whereabouts/pkg/version.Version=${VERSION} \
    -X github.com/k8snetworkplumbingwg/whereabouts/pkg/version.GitSHA=${GIT_SHA} \
    -X github.com/k8snetworkplumbingwg/whereabouts/pkg/version.GitTreeState=${GIT_TREE_STATE} \
    -X github.com/k8snetworkplumbingwg/whereabouts/pkg/version.ReleaseStatus=${RELEASE_STATUS}" && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o bin/whereabouts ./cmd/whereabouts/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o bin/whereabouts-operator ./cmd/operator/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o bin/install-cni ./cmd/install-cni/

FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source=https://github.com/k8snetworkplumbingwg/whereabouts
WORKDIR /
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/whereabouts-operator .
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/install-cni .
# Default to non-root; the DaemonSet overrides this via securityContext.
USER 65532:65532
CMD ["/install-cni"]
