FROM --platform=$BUILDPLATFORM golang:1.19 as builder
ADD . /usr/src/whereabouts
RUN mkdir -p $GOPATH/src/github.com/k8snetworkplumbingwg/whereabouts
WORKDIR $GOPATH/src/github.com/k8snetworkplumbingwg/whereabouts
COPY . .
ARG TARGETOS TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH ./hack/build-go.sh

FROM alpine:latest
LABEL org.opencontainers.image.source https://github.com/k8snetworkplumbingwg/whereabouts
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/ip-control-loop .
COPY script/install-cni.sh .
CMD ["/install-cni.sh"]
