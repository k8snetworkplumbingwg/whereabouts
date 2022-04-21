package entities

import (
	"strings"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
)

func ReplicaSetQuery(rsName string) string {
	return "tier=" + rsName
}

func PodNetworkSelectionElements(networkNames ...string) map[string]string {
	return map[string]string{
		nettypes.NetworkAttachmentAnnot: strings.Join(networkNames, ","),
	}
}
