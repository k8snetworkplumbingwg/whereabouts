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

// PodNetworkSelectionWithAnnotations merges network selection annotations with
// additional pod-level annotations (e.g. whereabouts.cni.cncf.io/preferred-ip).
func PodNetworkSelectionWithAnnotations(extra map[string]string, networkNames ...string) map[string]string {
	m := PodNetworkSelectionElements(networkNames...)
	for k, v := range extra {
		m[k] = v
	}
	return m
}
