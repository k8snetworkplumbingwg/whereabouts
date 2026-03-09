package v1alpha1

import (
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeSlicePoolSpec defines the desired state of NodeSlicePool.
type NodeSlicePoolSpec struct {
	// Range is a RFC 4632/4291-style string that represents an IP address and prefix length in CIDR notation.
	// This refers to the entire range where the node is allocated a subset.
	// +kubebuilder:validation:MinLength=1
	Range string `json:"range"`

	// SliceSize is the size of subnets or slices of the range that each node will be assigned.
	// The value must be a numeric prefix length, optionally preceded by a slash (e.g. "24" or "/24").
	// Semantic validation is performed by the validating webhook.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^/?[0-9]+$`
	SliceSize string `json:"sliceSize"`
}

// NodeSlicePoolStatus defines the observed state of NodeSlicePool.
type NodeSlicePoolStatus struct {
	// Allocations holds the allocations of nodes to slices.
	Allocations []NodeSliceAllocation `json:"allocations,omitempty"`

	// TotalSlices is the total number of IP slices in the pool.
	// +optional
	TotalSlices int32 `json:"totalSlices,omitempty"`

	// AssignedSlices is the number of slices currently assigned to nodes.
	// +optional
	AssignedSlices int32 `json:"assignedSlices,omitempty"`

	// FreeSlices is the number of slices available for node assignment.
	// +optional
	FreeSlices int32 `json:"freeSlices,omitempty"`

	// Conditions holds the conditions for the NodeSlicePool.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NodeSliceAllocation represents a single node-to-slice assignment.
type NodeSliceAllocation struct {
	// NodeName is the name of the node assigned to this slice. An empty node name
	// indicates that this slice is available for assignment.
	NodeName string `json:"nodeName"`

	// SliceRange is the subnet of this slice.
	SliceRange string `json:"sliceRange"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=nsp
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Range",type=string,JSONPath=`.spec.range`,description="CIDR range of the node slice pool"
// +kubebuilder:printcolumn:name="SliceSize",type=string,JSONPath=`.spec.sliceSize`,description="Size of each node slice"
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalSlices`,description="Total number of slices"
// +kubebuilder:printcolumn:name="Assigned",type=integer,JSONPath=`.status.assignedSlices`,description="Slices assigned to nodes"
// +kubebuilder:printcolumn:name="Free",type=integer,JSONPath=`.status.freeSlices`,description="Slices available for assignment"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Whether the node slice pool is ready"

// NodeSlicePool is the Schema for the nodeslicepools API.
type NodeSlicePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSlicePoolSpec   `json:"spec,omitempty"`
	Status NodeSlicePoolStatus `json:"status,omitempty"`
}

// ParseCIDR formats the Range of the NodeSlicePool.
func (in NodeSlicePool) ParseCIDR() (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(in.Spec.Range)
}

// +kubebuilder:object:root=true

// NodeSlicePoolList contains a list of NodeSlicePool.
type NodeSlicePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeSlicePool `json:"items"`
}
