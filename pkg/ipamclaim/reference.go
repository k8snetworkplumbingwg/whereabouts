// Package ipamclaim helpers for the IPAMClaim persistent-IP contract
// (k8snetworkplumbingwg/ipamclaims), used with Multus network-selection elements
// and kubevirt/ipam-extensions.
package ipamclaim

import (
	ipamclaimsv1alpha1 "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1"
	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
)

// ReferenceField is the NetworkSelectionElement / CNI JSON field name for the claim.
const ReferenceField = "ipam-claim-reference"

// GroupVersion is the IPAMClaim API group/version used by this plugin.
var GroupVersion = ipamclaimsv1alpha1.SchemeGroupVersion

// ReferenceFromNetworkSelectionElements returns the IPAMClaim name from the
// network-selection element matching ifName and/or networkName.
// This mirrors how OVN-Kubernetes / ipam-extensions wire persistent IPs: the
// claim reference lives on the pod annotation NSE, not (today) in Multus-injected
// CNI stdin for delegate IPAM plugins.
func ReferenceFromNetworkSelectionElements(elements []*nadv1.NetworkSelectionElement, networkName, ifName string) string {
	if len(elements) == 0 {
		return ""
	}

	for _, el := range elements {
		if el == nil || el.IPAMClaimReference == "" {
			continue
		}
		if ifName != "" && el.InterfaceRequest != "" && el.InterfaceRequest == ifName {
			return el.IPAMClaimReference
		}
	}

	for _, el := range elements {
		if el == nil || el.IPAMClaimReference == "" {
			continue
		}
		if networkName != "" && el.Name == networkName {
			return el.IPAMClaimReference
		}
	}

	// Single-element annotation with a claim: use it when ifName/network filters
	// did not uniquely match (common for a single secondary attachment).
	var match string
	count := 0
	for _, el := range elements {
		if el == nil || el.IPAMClaimReference == "" {
			continue
		}
		match = el.IPAMClaimReference
		count++
	}
	if count == 1 {
		return match
	}
	return ""
}

// ReferenceFromCNIArgs extracts ipam-claim-reference from Multus-injected args.cni.
func ReferenceFromCNIArgs(cniArgs *map[string]interface{}) string {
	if cniArgs == nil {
		return ""
	}
	raw, ok := (*cniArgs)[ReferenceField]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return s
}
