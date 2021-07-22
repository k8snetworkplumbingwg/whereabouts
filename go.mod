module github.com/dougbtv/whereabouts

go 1.12

require (
	github.com/containernetworking/cni v0.7.1
	github.com/containernetworking/plugins v0.8.2
	github.com/coreos/bbolt v1.3.3 // indirect
	github.com/coreos/etcd v3.3.15+incompatible
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/gorilla/websocket v1.4.1 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.11.1 // indirect
	github.com/imdario/mergo v0.3.8
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20191119172530-79f836b90111
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/pkg/errors v0.8.1
	github.com/tmc/grpc-websocket-proxy v0.0.0-20190109142713-0ad062ec5ee5 // indirect
	go.uber.org/atomic v1.4.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.0.1
	k8s.io/api v0.18.2
	k8s.io/apiextensions-apiserver v0.18.2 // indirect
	k8s.io/apimachinery v0.18.2
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	sigs.k8s.io/controller-runtime v0.2.2
)

replace github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
