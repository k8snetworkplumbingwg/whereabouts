module github.com/k8snetworkplumbingwg/whereabouts

go 1.16

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/containernetworking/cni v0.7.1
	github.com/containernetworking/plugins v0.8.2
	github.com/coreos/etcd v3.3.13+incompatible
	github.com/grpc-ecosystem/grpc-gateway v1.11.1 // indirect
	github.com/imdario/mergo v0.3.10
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v1.1.1-0.20210510153419-66a699ae3b05
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.1
	golang.org/x/net v0.0.0-20220225172249-27dd8689420f // indirect
	golang.org/x/sys v0.0.0-20220227234510-4e6760a101f9 // indirect
	golang.org/x/tools v0.1.9 // indirect
	gomodules.xyz/jsonpatch/v2 v2.1.0
	k8s.io/api v0.22.6
	k8s.io/apimachinery v0.22.6
	k8s.io/client-go v0.22.6
	k8s.io/code-generator v0.20.1
	k8s.io/klog/v2 v2.10.0 // indirect
	k8s.io/kube-openapi v0.0.0-20220124234850-424119656bbf
	sigs.k8s.io/controller-runtime v0.8.2
	sigs.k8s.io/yaml v1.3.0 // indirect
)

replace (
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
	google.golang.org/grpc => google.golang.org/grpc v1.29.0
)
