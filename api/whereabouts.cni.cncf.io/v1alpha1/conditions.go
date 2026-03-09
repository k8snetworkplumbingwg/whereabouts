package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// IPPool conditions interface implementations.

// GetConditions returns the status conditions of the IPPool.
func (in *IPPool) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the IPPool.
func (in *IPPool) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// NodeSlicePool conditions interface implementations.

// GetConditions returns the status conditions of the NodeSlicePool.
func (in *NodeSlicePool) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the NodeSlicePool.
func (in *NodeSlicePool) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// OverlappingRangeIPReservation conditions interface implementations.

// GetConditions returns the status conditions of the OverlappingRangeIPReservation.
func (in *OverlappingRangeIPReservation) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the OverlappingRangeIPReservation.
func (in *OverlappingRangeIPReservation) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}
