// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// We intentionally reuse FluxCD's generic kstatus-compatible condition helpers
// (meta and runtime/conditions) instead of Cluster-API's util/conditions or
// hand-rolled metav1.Condition updates. This keeps the common
// Reconciling/Ready/Stalled pattern well-tested and consistent without
// introducing a dependency on Cluster-API itself.

// Kstatus-compatible condition reasons used across all controllers.
const (
	// ReasonReconciling indicates the controller is actively processing.
	ReasonReconciling = "Reconciling"

	// ReasonReconciled indicates successful reconciliation.
	ReasonReconciled = "Reconciled"

	// ReasonOrphansCleaned indicates orphaned allocations were removed.
	ReasonOrphansCleaned = "OrphansCleaned"

	// ReasonPoolCreated indicates a new pool was created.
	ReasonPoolCreated = "PoolCreated"

	// ReasonPoolUpdated indicates a pool spec was updated.
	ReasonPoolUpdated = "PoolUpdated"

	// ReasonPoolFull indicates no available slots for node assignment.
	ReasonPoolFull = "PoolFull"

	// ReasonValidated indicates the resource passed validation checks.
	ReasonValidated = "Validated"

	// ReasonError indicates an error during reconciliation.
	ReasonError = "Error"
)

// markReconciling sets the Reconciling condition to True and Ready to Unknown,
// indicating that the controller is actively processing the resource.
func markReconciling(obj conditions.Setter, message string) {
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.ReconcilingCondition,
		Status:  metav1.ConditionTrue,
		Reason:  ReasonReconciling,
		Message: message,
	})
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.ReadyCondition,
		Status:  metav1.ConditionUnknown,
		Reason:  ReasonReconciling,
		Message: message,
	})
}

// markReady sets the Ready condition to True, and clears Reconciling and
// Stalled conditions, indicating that reconciliation completed successfully.
func markReady(obj conditions.Setter, reason, message string) {
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.ReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.ReconcilingCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.StalledCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// markStalled sets the Stalled condition to True and Ready to False,
// indicating that the controller cannot make further progress.
func markStalled(obj conditions.Setter, reason, message string) {
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.StalledCondition,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	conditions.Set(obj, &metav1.Condition{
		Type:    fluxmeta.ReconcilingCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}
