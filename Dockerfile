FROM golang:1.25
ADD . /usr/src/whereabouts
RUN mkdir -p $GOPATH/src/github.com/k8snetworkplumbingwg/whereabouts
WORKDIR $GOPATH/src/github.com/k8snetworkplumbingwg/whereabouts
COPY . .
RUN ./hack/build-go.sh

FROM alpine:latest
LABEL org.opencontainers.image.source=https://github.com/k8snetworkplumbingwg/whereabouts
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/whereabouts .
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/ip-control-loop .
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/node-slice-controller .
COPY script/install-cni.sh .
COPY script/lib.sh .
COPY script/token-watcher.sh .
CMD ["/install-cni.sh"]
