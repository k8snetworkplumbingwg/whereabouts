module github.com/k8snetworkplumbingwg/whereabouts

go 1.15

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/containernetworking/cni v0.7.1
	github.com/containernetworking/plugins v0.8.2
	github.com/coreos/etcd v3.3.13+incompatible
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.11.1 // indirect
	github.com/imdario/mergo v0.3.10
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20191119172530-79f836b90111
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/pkg/errors v0.9.1
	gomodules.xyz/jsonpatch/v2 v2.1.0
	inet.af/netaddr v0.0.0-20211027220019-c74959edd3b6
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	sigs.k8s.io/controller-runtime v0.8.2
)

replace github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
