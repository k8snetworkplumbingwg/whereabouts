FROM golang:1.13
ADD . /usr/src/whereabouts
RUN mkdir -p $GOPATH/src/github.com/dougbtv/whereabouts
WORKDIR $GOPATH/src/github.com/dougbtv/whereabouts
COPY . .
RUN ./hack/build-go.sh

FROM alpine:latest
LABEL org.opencontainers.image.source https://github.com/k8snetworkplumbingwg/whereabouts
COPY --from=0 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts .
COPY --from=0 /go/src/github.com/dougbtv/whereabouts/bin/ip-reconciler .
COPY script/install-cni.sh .
CMD ["/install-cni.sh"]
