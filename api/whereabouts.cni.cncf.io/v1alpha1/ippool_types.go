package v1alpha1

import (
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IPPoolSpec defines the desired state of IPPool.
type IPPoolSpec struct {
	// Range is a RFC 4632/4291-style string that represents an IP address and prefix length in CIDR notation.
	// +kubebuilder:validation:MinLength=1
	Range string `json:"range"`

	// Allocations is the set of allocated IPs for the given range. Its indices are a direct mapping to the
	// IP with the same index/offset for the pool's range.
	Allocations map[string]IPAllocation `json:"allocations"`
}

// IPPoolStatus defines the observed state of IPPool.
type IPPoolStatus struct {
	// FirstIP is the first usable IP address in the pool's range.
	// +optional
	FirstIP string `json:"firstIP,omitempty"`

	// LastIP is the last usable IP address in the pool's range.
	// +optional
	LastIP string `json:"lastIP,omitempty"`

	// TotalIPs is the total number of usable IPs in the pool's CIDR range
	// (excluding network and broadcast addresses).
	// +optional
	TotalIPs int32 `json:"totalIPs,omitempty"`

	// UsedIPs is the number of IPs currently allocated from the pool.
	// +optional
	UsedIPs int32 `json:"usedIPs,omitempty"`

	// FreeIPs is the number of IPs available for allocation (totalIPs - usedIPs).
	// +optional
	FreeIPs int32 `json:"freeIPs,omitempty"`

	// OrphanedIPs is the number of allocations whose pods no longer exist,
	// as detected during the last reconciliation.
	// +optional
	OrphanedIPs int32 `json:"orphanedIPs,omitempty"`

	// PendingPods is the number of allocations whose pods are still in the
	// Pending phase, as detected during the last reconciliation.
	// +optional
	PendingPods int32 `json:"pendingPods,omitempty"`

	// OverlappingReservations is the number of OverlappingRangeIPReservation CRDs
	// that correspond to allocations in this pool.
	// +optional
	OverlappingReservations int32 `json:"overlappingReservations,omitempty"`

	// AllocatedIPs is a list of resolved IP-to-pod assignments derived from the
	// spec allocations offset map. Provides a human-readable view of the pool's
	// current allocations.
	// +optional
	AllocatedIPs []IPAddressAllocation `json:"allocatedIPs,omitempty"`

	// Conditions holds the conditions for the IPPool.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// IPAddressAllocation represents a single resolved IP allocation with its
// actual IP address (computed from the CIDR base and offset key).
type IPAddressAllocation struct {
	// IP is the resolved IP address for this allocation.
	IP string `json:"ip"`

	// PodRef is the namespace/name reference of the pod that owns this allocation.
	PodRef string `json:"podRef"`

	// IfName is the network interface name inside the pod.
	// +optional
	IfName string `json:"ifName,omitempty"`
}

// IPAllocation represents metadata about the pod/container owner of a specific IP.
type IPAllocation struct {
	// ContainerID is the identifier of the container that owns this allocation.
	ContainerID string `json:"id"`

	// PodRef is the namespace/name reference of the pod that owns this allocation.
	PodRef string `json:"podref"`

	// IfName is the network interface name inside the pod for this allocation.
	IfName string `json:"ifname,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=ipp
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Range",type=string,JSONPath=`.spec.range`,description="CIDR range of the IP pool"
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalIPs`,description="Total usable IPs in the range"
// +kubebuilder:printcolumn:name="Used",type=integer,JSONPath=`.status.usedIPs`,description="Number of IPs currently allocated"
// +kubebuilder:printcolumn:name="Free",type=integer,JSONPath=`.status.freeIPs`,description="Number of IPs available for allocation"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Whether the IP pool is ready"

// IPPool is the Schema for the ippools API.
type IPPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IPPoolSpec   `json:"spec,omitempty"`
	Status IPPoolStatus `json:"status,omitempty"`
}

// ParseCIDR formats the Range of the IPPool.
func (in IPPool) ParseCIDR() (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(in.Spec.Range)
}

// +kubebuilder:object:root=true

// IPPoolList contains a list of IPPool.
type IPPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPPool `json:"items"`
}
