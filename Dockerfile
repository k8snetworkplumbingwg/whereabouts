FROM golang:1.17
ADD . /usr/src/whereabouts
RUN mkdir -p $GOPATH/src/github.com/k8snetworkplumbingwg/whereabouts
WORKDIR $GOPATH/src/github.com/k8snetworkplumbingwg/whereabouts
COPY . .
RUN ./hack/build-go.sh

FROM alpine:latest
LABEL org.opencontainers.image.source https://github.com/k8snetworkplumbingwg/whereabouts
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/whereabouts .
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/ip-control-loop .
COPY script/install-cni.sh .
CMD ["/install-cni.sh"]
