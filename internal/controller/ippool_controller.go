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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/telekom/whereabouts/pkg/iphelpers"
)

// IPPoolReconciler reconciles IPPool resources by removing allocations whose
// pods no longer exist. It replaces the legacy CronJob-based reconciler and
// the DaemonSet pod controller.
type IPPoolReconciler struct {
	client            client.Client
	recorder          events.EventRecorder
	reconcileInterval time.Duration
	serviceCIDRs      []string

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
	seenIPs := make(map[string]struct{})
	for i := range reservations.Items {
		res := &reservations.Items[i]
		resIP := denormalizeIPName(res.Name)
		if resIP == nil {
			continue
		}
		resIPStr := resIP.String()
		if _, ok := seenIPs[resIPStr]; ok {
			continue
		}
		// Check if this reservation's IP matches any allocation in the pool.
		for key := range pool.Spec.Allocations {
			poolIP := allocationKeyToIP(pool, key)
			if poolIP != nil && poolIP.Equal(resIP) {
				count++
				seenIPs[resIPStr] = struct{}{}
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

func newIPPoolReconciler(k8sClient client.Client, recorder events.EventRecorder, reconcileInterval time.Duration, opts ReconcilerOptions) *IPPoolReconciler {
	return &IPPoolReconciler{
		client:              k8sClient,
		recorder:            recorder,
		reconcileInterval:   reconcileInterval,
		serviceCIDRs:        append([]string(nil), opts.ServiceCIDRs...),
		cleanupTerminating:  opts.CleanupTerminating,
		cleanupDisrupted:    opts.CleanupDisrupted,
		verifyNetworkStatus: opts.VerifyNetworkStatus,
	}
}

// SetupIPPoolReconciler creates and registers the IPPoolReconciler with the
// manager. The reconcileInterval controls the periodic re-queue interval.
func SetupIPPoolReconciler(mgr ctrl.Manager, reconcileInterval time.Duration, opts ReconcilerOptions) error {
	r := newIPPoolReconciler(mgr.GetClient(), mgr.GetEventRecorder("ippool-controller"), reconcileInterval, opts)

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
		if apierrors.IsNotFound(err) {
			deleteIPPoolMetrics(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting IPPool: %w", err)
	}

	// Handle deletion: cleanup overlapping reservations, then remove finalizer.
	//
	// During finalization we pass the pool's current allocations to
	// cleanupOverlappingReservations. The PodRef guard inside that
	// function intentionally skips reservations whose PodRef differs
	// from the allocation's PodRef — this is safe because such
	// reservations have already been claimed by a new pod via a
	// different pool, and the OverlappingRangeReconciler will manage
	// their lifecycle independently.
	if !pool.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&pool, ippoolFinalizer) {
			logger.Info("IPPool being deleted, cleaning up overlapping reservations", "pool", pool.Name)
			if len(pool.Spec.Allocations) > 0 {
				if err := r.cleanupOverlappingReservations(ctx, &pool, pool.Spec.Allocations); err != nil {
					logger.Error(err, "failed to clean up overlapping reservations during finalization")
					return ctrl.Result{RequeueAfter: retryRequeueInterval}, nil
				}
			}

			controllerutil.RemoveFinalizer(&pool, ippoolFinalizer)
			if err := r.client.Update(ctx, &pool); err != nil {
				return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
			}
			deleteIPPoolMetrics(pool.Name)
			logger.Info("finalizer removed, IPPool can be deleted", "pool", pool.Name)
		}
		return ctrl.Result{}, nil
	}

	r.recordServiceCIDROverlap(ctx, &pool)

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

	if len(pool.Spec.Allocations) == 0 {
		r.computePoolStats(ctx, &pool, 0, 0)
		recordIPPoolMetrics(pool.Name, pool.Status.TotalIPs, pool.Status.UsedIPs, pool.Status.FreeIPs)
		markReady(&pool, ReasonReconciled, "no allocations to reconcile")
		if err := patchHelper.Patch(ctx, &pool); err != nil {
			logger.Error(err, "failed to patch status")
		}
		return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
	}

	// Collect orphaned allocations.
	orphanedAllocs := make(map[string]whereaboutsv1alpha1.IPAllocation)
	var pendingCount int32

	for key, alloc := range pool.Spec.Allocations {
		if alloc.PodRef == "" {
			logger.Info("allocation missing podRef, marking orphaned", "key", key)
			orphanedAllocs[key] = alloc
			continue
		}

		podNS, podName, ok := parsePodRef(alloc.PodRef)
		if !ok {
			logger.Info("invalid podRef format, marking orphaned", "key", key, "podRef", alloc.PodRef)
			orphanedAllocs[key] = alloc
			continue
		}

		var pod corev1.Pod
		err := r.client.Get(ctx, types.NamespacedName{Namespace: podNS, Name: podName}, &pod)
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("pod not found, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedAllocs[key] = alloc
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
			orphanedAllocs[key] = alloc
			continue
		}

		// Pod is terminating (DeletionTimestamp set) — this covers graceful
		// node shutdown and standard pod deletion. Gated behind the
		// cleanupTerminating flag because the IP may still be in use by
		// the container until it fully terminates. See upstream #550.
		if r.cleanupTerminating && pod.DeletionTimestamp != nil {
			logger.V(1).Info("pod is terminating, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef)
			orphanedAllocs[key] = alloc
			continue
		}

		if isPodCompleted(pod.Status.Phase) {
			logger.V(1).Info("pod is completed, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef, "phase", pod.Status.Phase)
			orphanedAllocs[key] = alloc
			continue
		}

		if isPodLostToNodeFailure(pod.Status) {
			logger.V(1).Info("pod is lost with its node, marking allocation orphaned",
				"key", key, "podRef", alloc.PodRef, "phase", pod.Status.Phase, "reason", pod.Status.Reason)
			orphanedAllocs[key] = alloc
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
				orphanedAllocs[key] = alloc
				continue
			}
		}
	}

	// Remove orphaned allocations (in-memory; PatchHelper persists later).
	orphanedKeys := make([]string, 0, len(orphanedAllocs))
	for k := range orphanedAllocs {
		orphanedKeys = append(orphanedKeys, k)
	}
	if len(orphanedAllocs) > 0 {
		removeAllocations(&pool, orphanedKeys)
		ippoolOrphansCleaned.WithLabelValues(pool.Name).Add(float64(len(orphanedAllocs)))
		logger.Info("cleaned up orphaned allocations",
			"pool", pool.Name, "count", len(orphanedAllocs))
		r.recorder.Eventf(&pool, nil, corev1.EventTypeNormal, "OrphanedAllocationsCleaned", "Reconcile",
			"removed %d orphaned IP allocation(s)", len(orphanedAllocs))

		// Also clean up any corresponding OverlappingRangeIPReservation CRDs.
		if err := r.cleanupOverlappingReservations(ctx, &pool, orphanedAllocs); err != nil {
			logger.Error(err, "failed to clean up some overlapping reservations, will retry")
			r.recorder.Eventf(&pool, nil, corev1.EventTypeWarning, "OverlappingReservationCleanupFailed", "Reconcile",
				"failed to clean up overlapping reservations: %s", err)
			return ctrl.Result{RequeueAfter: retryRequeueInterval}, nil
		}
	}

	// Requeue sooner if pending pods exist.
	if pendingCount > 0 {
		r.computePoolStats(ctx, &pool, int32(len(orphanedAllocs)), pendingCount)
		recordIPPoolMetrics(pool.Name, pool.Status.TotalIPs, pool.Status.UsedIPs, pool.Status.FreeIPs)
		markReconciling(&pool, "waiting for pending pods to be scheduled")
		if err := patchHelper.Patch(ctx, &pool); err != nil {
			if len(orphanedAllocs) > 0 {
				// Spec changes (orphan removal) failed to persist — return error
				// so controller-runtime retries quickly via exponential backoff.
				return ctrl.Result{}, fmt.Errorf("patching pool after orphan cleanup: %w", err)
			}
			// Status-only patch failure is non-critical.
			logger.Error(err, "failed to patch status")
		}
		return ctrl.Result{RequeueAfter: pendingPodRequeueInterval}, nil
	}

	// Mark as ready after successful reconciliation.
	r.computePoolStats(ctx, &pool, int32(len(orphanedAllocs)), pendingCount)
	recordIPPoolMetrics(pool.Name, pool.Status.TotalIPs, pool.Status.UsedIPs, pool.Status.FreeIPs)
	if len(orphanedAllocs) > 0 {
		markReady(&pool, ReasonOrphansCleaned, fmt.Sprintf("cleaned %d orphaned allocation(s)", len(orphanedAllocs)))
	} else {
		markReady(&pool, ReasonReconciled, "all allocations verified")
	}
	if err := patchHelper.Patch(ctx, &pool); err != nil {
		if len(orphanedAllocs) > 0 {
			// When spec changes (orphan removal) failed to persist, return the
			// error so controller-runtime retries quickly via exponential backoff.
			// Silently swallowing the error here would delay the next attempt by
			// reconcileInterval (default 30s), which is long enough to cause the
			// pool-exhaustion recovery test to time out.
			return ctrl.Result{}, fmt.Errorf("patching pool after orphan cleanup: %w", err)
		}
		// Status-only patch failures are non-critical: IP management is unaffected.
		logger.Error(err, "failed to patch status")
	}

	return ctrl.Result{RequeueAfter: r.reconcileInterval}, nil
}

func (r *IPPoolReconciler) recordServiceCIDROverlap(ctx context.Context, pool *whereaboutsv1alpha1.IPPool) {
	if len(r.serviceCIDRs) == 0 || strings.TrimSpace(pool.Spec.Range) == "" {
		return
	}

	overlapping, found, err := iphelpers.CIDROverlapsAny(pool.Spec.Range, r.serviceCIDRs)
	if err != nil {
		log.FromContext(ctx).V(1).Info("failed to check service CIDR overlap",
			"pool", pool.Name, "range", pool.Spec.Range, "error", err)
		return
	}
	if !found {
		return
	}

	log.FromContext(ctx).Info("IPPool range overlaps Kubernetes service CIDR",
		"pool", pool.Name, "range", pool.Spec.Range, "serviceCIDR", overlapping)
	r.recorder.Eventf(pool, nil, corev1.EventTypeWarning, "ServiceCIDROverlap", "Reconcile",
		"IPPool range %s overlaps Kubernetes service CIDR %s", pool.Spec.Range, overlapping)
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
// for IPs present in the provided allocation set. When an allocation carries a
// PodRef and IfName, the reservation must reference the same pod and interface
// before it is deleted — this prevents accidentally removing a reservation that
// already belongs to a new pod or a different Multus network that was rapidly
// assigned the same IP.
//
// To close the TOCTOU window between List() and Delete(), each matching
// reservation is re-fetched immediately before deletion. If the live object no
// longer exists (already cleaned up) or its PodRef changed (a new pod reclaimed
// the same IP), the deletion is skipped. A UID precondition is passed to
// Delete() so the API server rejects the request if the object was replaced
// between our Get() and Delete() calls.
func (r *IPPoolReconciler) cleanupOverlappingReservations(ctx context.Context, pool *whereaboutsv1alpha1.IPPool, orphaned map[string]whereaboutsv1alpha1.IPAllocation) error {
	logger := log.FromContext(ctx)
	var lastErr error

	// List all overlapping reservations in the pool namespace once and reuse for all keys.
	var reservations whereaboutsv1alpha1.OverlappingRangeIPReservationList
	if err := r.client.List(ctx, &reservations, client.InNamespace(pool.Namespace)); err != nil {
		logger.V(1).Info("failed to list overlapping reservations", "error", err)
		return fmt.Errorf("listing overlapping reservations: %w", err)
	}

	for key, alloc := range orphaned {
		ip := allocationKeyToIP(pool, key)
		if ip == nil {
			continue
		}

		for i := range reservations.Items {
			res := &reservations.Items[i]
			resIP := denormalizeIPName(res.Name)
			if resIP == nil || !resIP.Equal(ip) {
				continue
			}
			if alloc.PodRef == "" || res.Spec.PodRef != alloc.PodRef ||
				alloc.IfName == "" || res.Spec.IfName != alloc.IfName {
				logger.V(1).Info("skipping overlapping reservation: podRef/ifName unverifiable or mismatch",
					"name", res.Name,
					"allocPodRef", alloc.PodRef, "resPodRef", res.Spec.PodRef,
					"allocIfName", alloc.IfName, "resIfName", res.Spec.IfName)
				continue
			}

			// Re-fetch the reservation immediately before deletion to close the
			// TOCTOU window: another controller or CNI ADD may have deleted and
			// recreated an ORIP with the same name (and a different pod) between
			// our List() call above and this point.
			var fresh whereaboutsv1alpha1.OverlappingRangeIPReservation
			if err := r.client.Get(ctx, types.NamespacedName{Namespace: pool.Namespace, Name: res.Name}, &fresh); err != nil {
				if apierrors.IsNotFound(err) {
					// Already deleted by another actor — nothing to do.
					logger.V(1).Info("overlapping reservation already gone, skipping",
						"name", res.Name)
					continue
				}
				logger.Error(err, "failed to re-fetch overlapping reservation before delete",
					"name", res.Name)
				lastErr = fmt.Errorf("re-fetching overlapping reservation %s: %w", res.Name, err)
				continue
			}

			// Guard: verify the live object still belongs to the same pod and interface.
			// If the PodRef or IfName changed the IP was already claimed by a new pod
			// or a different network interface on the same pod.
			if fresh.Spec.PodRef != alloc.PodRef || fresh.Spec.IfName != alloc.IfName {
				logger.V(1).Info("overlapping reservation podRef/ifName changed between list and delete, skipping to avoid TOCTOU delete",
					"name", res.Name,
					"expectedPodRef", alloc.PodRef, "currentPodRef", fresh.Spec.PodRef,
					"expectedIfName", alloc.IfName, "currentIfName", fresh.Spec.IfName)
				continue
			}

			// Use a UID precondition so the API server rejects the delete if
			// the object was replaced between our Get() and Delete().
			uid := fresh.UID
			if err := r.client.Delete(ctx, &fresh, client.Preconditions{UID: &uid}); err != nil {
				if apierrors.IsNotFound(err) {
					// Deleted between re-fetch and delete call — treat as success.
					overlappingReservationsCleaned.Inc()
					logger.V(1).Info("overlapping reservation deleted concurrently, treating as success",
						"name", res.Name)
					continue
				}
				logger.Error(err, "failed to delete overlapping reservation",
					"name", res.Name)
				lastErr = fmt.Errorf("deleting overlapping reservation %s: %w", res.Name, err)
			} else {
				overlappingReservationsCleaned.Inc()
				logger.V(1).Info("deleted overlapping reservation", "name", res.Name)
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

func isPodCompleted(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed
}

func isPodLostToNodeFailure(status corev1.PodStatus) bool {
	return status.Phase == corev1.PodUnknown && status.Reason == "NodeLost"
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
