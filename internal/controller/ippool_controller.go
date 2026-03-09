// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/iphelpers"
)

// IPPoolReconciler reconciles IPPool resources by removing allocations whose
// pods no longer exist. It replaces the legacy CronJob-based reconciler and
// the DaemonSet pod controller.
type IPPoolReconciler struct {
	client            client.Client
	recorder          events.EventRecorder
	reconcileInterval time.Duration

	// cleanupTerminating controls whether pods with a DeletionTimestamp
	// (i.e. terminating pods) are treated as orphaned. When false (default),
	// terminating pods keep their IP allocation until fully deleted.
	// When true, their allocations are released immediately.
	cleanupTerminating bool

	// cleanupDisrupted controls whether pods with a DisruptionTarget
	// condition (DeletionByTaintManager) are treated as orphaned. When true
	// (default), these pods are cleaned up immediately because the taint
	// manager has already decided to evict them.
	cleanupDisrupted bool

	// verifyNetworkStatus controls whether the reconciler verifies that an
	// allocated IP is still present in the pod's Multus network-status
	// annotation. When true (default), a mismatch marks the allocation as
	// orphaned. Disable this if your environment uses a CNI that does not
	// populate the k8s.v1.cni.cncf.io/network-status annotation.
	verifyNetworkStatus bool
}

// computePoolStats populates the IPPool's status with total, used, free IPs,
// resolved allocations, first/last IPs, orphaned and pending counts, and the
// count of related overlapping range reservations. Best-effort: parse errors
// leave the respective field at zero.
func (r *IPPoolReconciler) computePoolStats(ctx context.Context, pool *whereaboutsv1alpha1.IPPool, orphanedCount, pendingCount int32) {
	// Parse CIDR to get first and last usable IPs.
	_, ipNet, err := net.ParseCIDR(pool.Spec.Range)
	if err == nil {
		if first, fErr := iphelpers.FirstUsableIP(*ipNet); fErr == nil {
			pool.Status.FirstIP = first.String()
		}
		if last, lErr := iphelpers.LastUsableIP(*ipNet); lErr == nil {
			pool.Status.LastIP = last.String()
		}
	}

	// Count total usable IPs from the CIDR range.
	totalIPs, err := iphelpers.CountUsableIPs(pool.Spec.Range)
	if err != nil {
		log.FromContext(ctx).V(1).Info("failed to count usable IPs", "range", pool.Spec.Range, "error", err)
	}
	pool.Status.TotalIPs = totalIPs

	usedIPs := int32(len(pool.Spec.Allocations))
	pool.Status.UsedIPs = usedIPs
	pool.Status.FreeIPs = totalIPs - usedIPs
	if pool.Status.FreeIPs < 0 {
		pool.Status.FreeIPs = 0
	}

	pool.Status.OrphanedIPs = orphanedCount
	pool.Status.PendingPods = pendingCount

	// Build resolved allocation list from the offset map.
	allocatedIPs := make([]whereaboutsv1alpha1.IPAddressAllocation, 0, len(pool.Spec.Allocations))
	for key, alloc := range pool.Spec.Allocations {
		ip := allocationKeyToIP(pool, key)
		ipStr := key // fallback to offset key if IP resolution fails
		if ip != nil {
			ipStr = ip.String()
		}
		allocatedIPs = append(allocatedIPs, whereaboutsv1alpha1.IPAddressAllocation{
			IP:     ipStr,
			PodRef: alloc.PodRef,
			IfName: alloc.IfName,
		})
	}
	pool.Status.AllocatedIPs = allocatedIPs

	// Count overlapping range reservations that belong to this pool's allocations.
	var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
	if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
		log.FromContext(ctx).V(1).Info("failed to list overlapping reservations for stats", "error", err)
		return
	}

	var count int32
	for i := range reservations.Items {
		res := &reservations.Items[i]
		resIP := denormalizeIPName(res.Name)
		if resIP == nil {
			continue
		}
		// Check if this reservation's IP matches any allocation in the pool.
		for key := range pool.Spec.Allocations {
			poolIP := allocationKeyToIP(pool, key)
			if poolIP != nil && poolIP.Equal(resIP) {
				count++
				break
			}
		}
	}
	pool.Status.OverlappingReservations = count
}

const (
	// ippoolFinalizer is used to ensure overlapping range reservations are
	// cleaned up before an IPPool is deleted.
	ippoolFinalizer = "whereabouts.cni.cncf.io/ippool-cleanup"

	// retryRequeueInterval is the interval to retry when transient errors
	// occur (e.g. overlapping reservation cleanup failure).
	retryRequeueInterval = 5 * time.Second

	// pendingPodRequeueInterval is the interval to recheck allocations for
	// pods still in the Pending phase.
	pendingPodRequeueInterval = 5 * time.Second
)

// SetupIPPoolReconciler creates and registers the IPPoolReconciler with the
// manager. The reconcileInterval controls the periodic re-queue interval.
func SetupIPPoolReconciler(mgr ctrl.Manager, reconcileInterval time.Duration, opts ReconcilerOptions) error {
	r := &IPPoolReconciler{
		client:              mgr.GetClient(),
		recorder:            mgr.GetEventRecorder("ippool-controller"),
		reconcileInterval:   reconcileInterval,
		cleanupTerminating:  opts.CleanupTerminating,
		cleanupDisrupted:    opts.CleanupDisrupted,
		verifyNetworkStatus: opts.VerifyNetworkStatus,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&whereaboutsv1alpha1.IPPool{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			// Allow finalizer-triggered events (deletion) through.
			deletionPredicate,
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Named("ippool").
		Complete(r)
}

//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=ippools,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=ippools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile checks all allocations in the IPPool against live pods and removes
// orphaned entries.
func (r *IPPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling IPPool", "name", req.Name, "namespace", req.Namespace)

	var pool whereaboutsv1alpha1.IPPool
	if err := r.client.Get(ctx, req.NamespacedName, &pool); err != nil {
		if errors.IsNotFound(err) {
			ippoolAllocationsGauge.DeleteLabelValues(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting IPPool: %w", err)
	}

	// Handle deletion: cleanup overlapping reservations, then remove finalizer.
	if !pool.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&pool, ippoolFinalizer) {
			logger.Info("IPPool being deleted, cleaning up overlapping reservations", "pool", pool.Name)
			allKeys := make([]string, 0, len(pool.Spec.Allocations))
			for key := range pool.Spec.Allocations {
				allKeys = append(allKeys, key)
			}
			if len(allKeys) > 0 {
				if err := r.cleanupOverlappingReservations(ctx, &pool, allKeys); err != nil {
					logger.Error(err, "failed to clean up overlapping reservations during finalization")
					return ctrl.Result{RequeueAfter: retryRequeueInterval}, nil
				}
			}

			controllerutil.RemoveFinalizer(&pool, ippoolFinalizer)
			if err := r.client.Update(ctx, &pool); err != nil {
				return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
			}
			logger.Info("finalizer removed, IPPool can be deleted", "pool", pool.Name)
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is present on active pools.
	if !controllerutil.ContainsFinalizer(&pool, ippoolFinalizer) {
		controllerutil.AddFinalizer(&pool, ippoolFinalizer)
		if err := r.client.Update(ctx, &pool); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		// Re-queue after finalizer is added to proceed with reconciliation.
		return ctrl.Result{Requeue: true}, nil
	}

	// Snapshot the pool so we can send a single deferred status patch.
	patchHelper, err := NewPatchHelper(&pool, r.client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating patch helper: %w", err)
	}

	markReconciling(&pool, "checking allocations for orphaned entries")

	// Report current allocation count.
	ippoolAllocationsGauge.WithLabelValues(pool.Name).Set(float64(len(pool.Spec.Allocations)))

	if len(pool.Spec.Allocations) == 0 {
		r.computePoolStats(ctx, &pool, 0, 0)
		markReady(&pool, ReasonReconciled, "no allocations to reconcile")
		if err := patchHelper.Patch(ctx, &pool); err != nil {
			logger.Error(err, "failed to patch status")
		}
		return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
	}

	// Collect orphaned allocation keys.
	var orphanedKeys []string
	var pendingCount int32

	for key, alloc := range pool.Spec.Allocations {
		if alloc.PodRef == "" {
			logger.Info("allocation missing podRef, marking orphaned", "key", key)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		podNS, podName, ok := parsePodRef(alloc.PodRef)
		if !ok {
			logger.Info("invalid podRef format, marking orphaned", "key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		var pod corev1.Pod
		err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)
		if errors.IsNotFound(err) {
			logger.V(1).Info("pod not found, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting pod %s: %w", alloc.PodRef, err)
		}

		// Pod marked for deletion by taint manager — treat as orphaned.
		// Gated behind cleanupDisrupted (default true) because the taint
		// manager has already decided to evict the pod.
		if r.cleanupDisrupted && isPodMarkedForDeletion(pod.Status.Conditions) {
			logger.V(1).Info("pod marked for deletion, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		// Pod is terminating (DeletionTimestamp set) — this covers graceful
		// node shutdown and standard pod deletion. Gated behind the
		// cleanupTerminating flag because the IP may still be in use by
		// the container until it fully terminates. See upstream #550.
		if r.cleanupTerminating && pod.DeletionTimestamp != nil {
			logger.V(1).Info("pod is terminating, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedKeys = append(orphanedKeys, key)
			continue
		}

		// Pending pods may not have network-status annotation yet.
		if pod.Status.Phase == corev1.PodPending {
			pendingCount++
			continue
		}

		// Verify the IP is actually present on the pod (Multus network-status).
		// Gated behind verifyNetworkStatus (default true). Disable this if
		// your CNI does not populate the network-status annotation.
		if r.verifyNetworkStatus {
			poolIP := allocationKeyToIP(&pool, key)
			if poolIP != nil && !isPodUsingIP(&pod, poolIP) {
				logger.V(1).Info("IP not found on pod, marking allocation orphaned",
					"key", key, "podRef", alloc.PodRef, "ip", poolIP)
				orphanedKeys = append(orphanedKeys, key)
				continue
			}
		}
	}

	// Remove orphaned allocations (in-memory; PatchHelper persists later).
	if len(orphanedKeys) > 0 {
		removeAllocations(&pool, orphanedKeys)
		ippoolOrphansCleaned.WithLabelValues(pool.Name).Add(float64(len(orphanedKeys)))
		logger.Info("cleaned up orphaned allocations",
			"pool", pool.Name, "count", len(orphanedKeys))
		r.recorder.Eventf(&pool, nil, corev1.EventTypeNormal, "OrphanedAllocationsCleaned", "Reconcile",
			"removed %d orphaned IP allocation(s)", len(orphanedKeys))

		// Also clean up any corresponding OverlappingRangeIPReservation CRDs.
		if err := r.cleanupOverlappingReservations(ctx, &pool, orphanedKeys); err != nil {
			logger.Error(err, "failed to clean up some overlapping reservations, will retry")
			r.recorder.Eventf(&pool, nil, corev1.EventTypeWarning, "OverlappingReservationCleanupFailed", "Reconcile",
				"failed to clean up overlapping reservations: %s", err)
			return ctrl.Result{RequeueAfter: retryRequeueInterval}, nil
		}
	}

	// Requeue sooner if pending pods exist.
	if pendingCount > 0 {
		r.computePoolStats(ctx, &pool, int32(len(orphanedKeys)), pendingCount)
		markReconciling(&pool, "waiting for pending pods to be scheduled")
		if err := patchHelper.Patch(ctx, &pool); err != nil {
			logger.Error(err, "failed to patch status")
		}
		return ctrl.Result{RequeueAfter: pendingPodRequeueInterval}, nil
	}

	// Update allocation gauge after cleanup.
	ippoolAllocationsGauge.WithLabelValues(pool.Name).Set(float64(len(pool.Spec.Allocations)))

	// Mark as ready after successful reconciliation.
	r.computePoolStats(ctx, &pool, int32(len(orphanedKeys)), pendingCount)
	if len(orphanedKeys) > 0 {
		markReady(&pool, ReasonOrphansCleaned, fmt.Sprintf("cleaned %d orphaned allocation(s)", len(orphanedKeys)))
	} else {
		markReady(&pool, ReasonReconciled, "all allocations verified")
	}
	if err := patchHelper.Patch(ctx, &pool); err != nil {
		logger.Error(err, "failed to patch status")
	}

	return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
}

// removeAllocations removes the specified allocation keys from the IPPool
// in-memory.  The caller is responsible for persisting the change (e.g. via
// PatchHelper).
func removeAllocations(pool *whereaboutsv1alpha1.IPPool, keys []string) {
	newAllocations := make(map[string]whereaboutsv1alpha1.IPAllocation, len(pool.Spec.Allocations)-len(keys))
	removeSet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		removeSet[k] = struct{}{}
	}
	for k, v := range pool.Spec.Allocations {
		if _, remove := removeSet[k]; !remove {
			newAllocations[k] = v
		}
	}

	pool.Spec.Allocations = newAllocations
}

// cleanupOverlappingReservations deletes OverlappingRangeIPReservation CRDs
// for IPs that were in the orphaned allocations. Returns an error if any
// deletion fails (excluding NotFound).
func (r *IPPoolReconciler) cleanupOverlappingReservations(ctx context.Context, pool *whereaboutsv1alpha1.IPPool, keys []string) error {
	logger := log.FromContext(ctx)
	var lastErr error

	// List all overlapping reservations in the pool namespace once and reuse for all keys.
	var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
	if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
		logger.V(1).Info("failed to list overlapping reservations", "error", err)
		return err
	}

	for _, key := range keys {
		ip := allocationKeyToIP(pool, key)
		if ip == nil {
			continue
		}

		for i := range reservations.Items {
			res := &reservations.Items[i]
			resIP := denormalizeIPName(res.Name)
			if resIP != nil && resIP.Equal(ip) {
				if err := r.client.Delete(ctx, res); err != nil && !errors.IsNotFound(err) {
					logger.Error(err, "failed to delete overlapping reservation",
						"name", res.Name)
					lastErr = err
				} else if err == nil {
					overlappingReservationsCleaned.Inc()
					logger.V(1).Info("deleted overlapping reservation", "name", res.Name)
				}
			}
		}
	}

	return lastErr
}

// allocationKeyToIP converts an allocation map key (decimal offset) to an IP
// address using the pool's CIDR range. Supports arbitrarily large offsets via
// big.Int to handle wide IPv6 ranges (e.g. /64 or wider).
func allocationKeyToIP(pool *whereaboutsv1alpha1.IPPool, key string) net.IP {
	_, ipNet, err := net.ParseCIDR(pool.Spec.Range)
	if err != nil {
		return nil
	}

	// Parse offset as big.Int — must be non-negative for a valid allocation key.
	offset, ok := new(big.Int).SetString(key, 10)
	if !ok || offset.Sign() < 0 {
		return nil
	}

	return iphelpers.IPAddOffset(ipNet.IP, offset)
}

// denormalizeIPName converts a normalized IP name (dashes for colons) back to
// a net.IP. Handles optional network-name prefix.
//
// Names may have a network-name prefix: "mynet-10.0.0.5" or just "10.0.0.5".
// For IPv6, colons are replaced with dashes: "fd00--1" for "fd00::1".
func denormalizeIPName(name string) net.IP {
	// Try parsing as-is first (IPv4 with dots preserved).
	if ip := net.ParseIP(name); ip != nil {
		return ip
	}

	// Try full dash→colon replacement (IPv6 normalization).
	if ip := net.ParseIP(strings.ReplaceAll(name, "-", ":")); ip != nil {
		return ip
	}

	// Iteratively strip leading dash-separated prefix segments.
	// e.g. "mynet-10.0.0.5" → try "10.0.0.5",
	// "mynet-fd00--1" → try "fd00--1" → replace → "fd00::1".
	for i := strings.IndexByte(name, '-'); i >= 0; {
		suffix := name[i+1:]
		if ip := net.ParseIP(suffix); ip != nil {
			return ip
		}
		if ip := net.ParseIP(strings.ReplaceAll(suffix, "-", ":")); ip != nil {
			return ip
		}
		next := strings.IndexByte(suffix, '-')
		if next < 0 {
			break
		}
		i = i + 1 + next
	}

	return nil
}

// parsePodRef splits "namespace/name" into its components.
func parsePodRef(podRef string) (namespace, name string, ok bool) {
	parts := strings.SplitN(podRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// isPodMarkedForDeletion returns true if the pod has a DisruptionTarget
// condition indicating it's being evicted.
func isPodMarkedForDeletion(conditions []corev1.PodCondition) bool {
	for _, c := range conditions {
		if c.Type == corev1.DisruptionTarget &&
			c.Status == corev1.ConditionTrue &&
			c.Reason == "DeletionByTaintManager" {
			return true
		}
	}
	return false
}

// isPodUsingIP checks whether the pod's Multus network-status annotation
// contains the given IP address. Uses net.IP.Equal for proper IPv6 comparison.
func isPodUsingIP(pod *corev1.Pod, ip net.IP) bool {
	annotation, ok := pod.Annotations[nadv1.NetworkStatusAnnot]
	if !ok || annotation == "" {
		// No annotation — cannot confirm; assume still valid to avoid
		// false-positive cleanup.
		return true
	}

	var statuses []nadv1.NetworkStatus
	if err := json.Unmarshal([]byte(annotation), &statuses); err != nil {
		// Malformed annotation — skip this pod, don't treat as orphan (P11-2).
		return true
	}

	for i := range statuses {
		if statuses[i].Default {
			continue
		}
		for _, ipStr := range statuses[i].IPs {
			podIP := net.ParseIP(ipStr)
			if podIP != nil && podIP.Equal(ip) {
				return true
			}
		}
	}

	return false
}

// deletionPredicate accepts delete events and update events where the
// deletion timestamp is newly set. Used with predicate.Or to ensure
// finalizer-driven reconciliation proceeds alongside GenerationChangedPredicate.
var deletionPredicate = predicate.Funcs{
	CreateFunc:  func(event.CreateEvent) bool { return false },
	DeleteFunc:  func(event.DeleteEvent) bool { return true },
	GenericFunc: func(event.GenericEvent) bool { return false },
	UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectNew == nil {
			return false
		}
		return !e.ObjectNew.GetDeletionTimestamp().IsZero()
	},
}

var _ reconcile.Reconciler = &IPPoolReconciler{}
