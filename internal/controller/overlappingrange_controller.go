// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

// OverlappingRangeReconciler reconciles OverlappingRangeIPReservation CRDs by
// deleting reservations whose pods no longer exist. This provides a secondary
// cleanup path in addition to the IPPoolReconciler's inline cleanup.
type OverlappingRangeReconciler struct {
	client            client.Client
	recorder          events.EventRecorder
	reconcileInterval time.Duration

	// cleanupTerminating controls whether terminating pods (DeletionTimestamp
	// set) are treated as orphaned. See IPPoolReconciler for details.
	cleanupTerminating bool

	// cleanupDisrupted controls whether pods with a DisruptionTarget
	// condition are treated as orphaned. See IPPoolReconciler for details.
	cleanupDisrupted bool
}

// SetupOverlappingRangeReconciler creates and registers the reconciler.
func SetupOverlappingRangeReconciler(mgr ctrl.Manager, reconcileInterval time.Duration, opts ReconcilerOptions) error {
	r := &OverlappingRangeReconciler{
		client:             mgr.GetClient(),
		recorder:           mgr.GetEventRecorder("overlappingrange-controller"),
		reconcileInterval:  reconcileInterval,
		cleanupTerminating: opts.CleanupTerminating,
		cleanupDisrupted:   opts.CleanupDisrupted,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
		// GenerationChangedPredicate passes Create events (including initial cache sync)
		// and only filters Update events where .metadata.generation is unchanged (e.g.
		// status-only updates). Periodic orphan checking is driven by RequeueAfter, not
		// by watch events, so this predicate does not prevent detection of deleted pods.
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Named("overlappingrange").
		Complete(r)
}

//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile checks whether the pod referenced by the OverlappingRangeIPReservation
// still exists. If not, the reservation is deleted.
func (r *OverlappingRangeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling OverlappingRangeIPReservation", "name", req.Name, "namespace", req.Namespace)

	var reservation whereaboutsv1alpha1.OverlappingRangeIPReservation
	if err := r.client.Get(ctx, req.NamespacedName, &reservation); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting OverlappingRangeIPReservation: %w", err)
	}

	// Skip if no podRef — nothing to check.
	if reservation.Spec.PodRef == "" {
		return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
	}

	podNS, podName, ok := parsePodRef(reservation.Spec.PodRef)
	if !ok {
		logger.Info("invalid podRef format, deleting reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}

	var pod corev1.Pod
	err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)
	if errors.IsNotFound(err) {
		logger.V(1).Info("pod not found, deleting overlapping reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting pod %s: %w", reservation.Spec.PodRef, err)
	}

	// Pod marked for deletion by taint manager. Gated behind
	// cleanupDisrupted (default true).
	if r.cleanupDisrupted && isPodMarkedForDeletion(pod.Status.Conditions) {
		logger.V(1).Info("pod marked for deletion, deleting overlapping reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}

	// Pod is terminating (DeletionTimestamp set). Gated behind
	// cleanupTerminating (default false). See upstream #550.
	if r.cleanupTerminating && pod.DeletionTimestamp != nil {
		logger.V(1).Info("pod is terminating, deleting overlapping reservation",
			"name", reservation.Name, "podRef", reservation.Spec.PodRef)
		return r.deleteReservation(ctx, &reservation)
	}

	// Pod exists and is healthy — mark reservation as ready.
	patchHelper, pErr := NewPatchHelper(&reservation, r.client)
	if pErr != nil {
		return ctrl.Result{}, fmt.Errorf("creating patch helper: %w", pErr)
	}
	markReady(&reservation, ReasonValidated, "referenced pod exists")
	if pErr = patchHelper.Patch(ctx, &reservation); pErr != nil {
		logger.Error(pErr, "failed to patch ready status")
		return ctrl.Result{}, fmt.Errorf("patching OverlappingRangeIPReservation status: %w", pErr)
	}

	return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
}

// deleteReservation removes the ORIP CR.
func (r *OverlappingRangeReconciler) deleteReservation(ctx context.Context, reservation *whereaboutsv1alpha1.OverlappingRangeIPReservation) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if err := r.client.Delete(ctx, reservation); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("deleting OverlappingRangeIPReservation %s: %w", reservation.Name, err)
	}
	overlappingReservationsCleaned.Inc()
	logger.Info("deleted orphaned overlapping reservation",
		"name", reservation.Name, "podRef", reservation.Spec.PodRef)
	r.recorder.Eventf(reservation, reservation, corev1.EventTypeNormal, "OrphanedReservationDeleted", "Reconcile",
		fmt.Sprintf("deleted orphaned reservation for pod %s", reservation.Spec.PodRef))
	return ctrl.Result{}, nil
}

var _ reconcile.Reconciler = &OverlappingRangeReconciler{}
