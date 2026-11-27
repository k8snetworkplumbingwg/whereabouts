// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package controller registers reconcilers with a controller-runtime Manager.
package controller

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// ReconcilerOptions holds optional feature flags for reconciler setup.
type ReconcilerOptions struct {
	// CleanupTerminating controls whether pods with a DeletionTimestamp
	// (i.e. terminating pods) are treated as orphaned. When false (default),
	// terminating pods keep their IP allocation until fully deleted. When
	// true, allocations are released immediately. Applies to both IPPool
	// and OverlappingRange reconcilers. See upstream #550.
	CleanupTerminating bool

	// CleanupDisrupted controls whether pods with a DisruptionTarget
	// condition (DeletionByTaintManager) are treated as orphaned. When true
	// (default), the reconcilers release their allocations immediately
	// because the taint manager has already decided to evict the pod.
	// Applies to both IPPool and OverlappingRange reconcilers.
	CleanupDisrupted bool

	// VerifyNetworkStatus controls whether the IPPool reconciler verifies
	// that an allocated IP is present in the pod's Multus network-status
	// annotation. When true (default), a mismatch marks the allocation as
	// orphaned. Disable this if your CNI does not populate the annotation.
	VerifyNetworkStatus bool
}

// SetupWithManager registers all reconcilers with the given manager. The
// reconcileInterval controls how often periodic re-checks of IP pools and
// related resources are triggered.
//
// The following RBAC rules are required by controller-runtime infrastructure
// (leader election and event recording) and are not tied to a specific
// reconciler:
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update;delete
// +kubebuilder:rbac:groups="",events.k8s.io,resources=events,verbs=create;patch;update;get
func SetupWithManager(mgr ctrl.Manager, reconcileInterval time.Duration, opts ReconcilerOptions) error {
	if err := SetupIPPoolReconciler(mgr, reconcileInterval, opts); err != nil {
		return err
	}

	if err := SetupNodeSliceReconciler(mgr); err != nil {
		return err
	}

	return SetupOverlappingRangeReconciler(mgr, reconcileInterval, opts)
}
