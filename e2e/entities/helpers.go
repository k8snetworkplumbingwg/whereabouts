package entities

import (
	"encoding/json"
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

// PodNetworkSelectionElementsWithIPAMClaim builds a Multus networks annotation that
// references a single NAD interface and associates it with an IPAMClaim.
func PodNetworkSelectionElementsWithIPAMClaim(networkName, ifName, claimName string) map[string]string {
	elements := []nettypes.NetworkSelectionElement{{
		Name:               networkName,
		InterfaceRequest:   ifName,
		IPAMClaimReference: claimName,
	}}
	raw, err := json.Marshal(elements)
	if err != nil {
		return PodNetworkSelectionElements(networkName)
	}
	return map[string]string{
		nettypes.NetworkAttachmentAnnot: string(raw),
	}
}
