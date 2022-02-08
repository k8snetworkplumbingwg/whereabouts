module github.com/k8snetworkplumbingwg/whereabouts

go 1.16

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/containernetworking/cni v0.7.1
	github.com/containernetworking/plugins v0.8.2
	github.com/coreos/etcd v3.3.13+incompatible
	github.com/grpc-ecosystem/grpc-gateway v1.11.1 // indirect
	github.com/imdario/mergo v0.3.10
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20191119172530-79f836b90111
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/pkg/errors v0.9.1
	gomodules.xyz/jsonpatch/v2 v2.1.0
	k8s.io/api v0.22.6
	k8s.io/apimachinery v0.22.6
	k8s.io/client-go v0.22.6
	sigs.k8s.io/controller-runtime v0.8.2
	sigs.k8s.io/controller-tools v0.4.1 // indirect
)

replace github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
