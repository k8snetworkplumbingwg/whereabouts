package reconciler

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/allocate"
	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

type ReconcileLooper struct {
	k8sClient              kubernetes.Client
	liveWhereaboutsPods    map[string]podWrapper
	orphanedIPs            []OrphanedIPReservations
	orphanedClusterWideIPs []whereaboutsv1alpha1.OverlappingRangeIPReservation
	requestTimeout         int
}

type OrphanedIPReservations struct {
	Pool        storage.IPPool
	Allocations []types.IPReservation
}

func NewReconcileLooperWithKubeconfig(ctx context.Context, kubeconfigPath string, timeout int) (*ReconcileLooper, error) {
	logging.Debugf("NewReconcileLooper - Kubernetes config file located at: %s", kubeconfigPath)
	k8sClient, err := kubernetes.NewClientViaKubeconfig(kubeconfigPath, time.Duration(timeout)*time.Second)
	if err != nil {
		return nil, logging.Errorf("failed to instantiate the Kubernetes client: %+v", err)
	}
	return NewReconcileLooperWithClient(ctx, k8sClient, timeout)
}

func NewReconcileLooper(ctx context.Context, timeout int) (*ReconcileLooper, error) {
	logging.Debugf("NewReconcileLooper - inferred connection data")
	k8sClient, err := kubernetes.NewClient(time.Duration(timeout) * time.Second)
	if err != nil {
		return nil, logging.Errorf("failed to instantiate the Kubernetes client: %+v", err)
	}
	return NewReconcileLooperWithClient(ctx, k8sClient, timeout)
}

func NewReconcileLooperWithClient(ctx context.Context, k8sClient *kubernetes.Client, timeout int) (*ReconcileLooper, error) {
	ipPools, err := k8sClient.ListIPPools(ctx)
	if err != nil {
		return nil, logging.Errorf("failed to retrieve all IP pools: %v", err)
	}

	pods, err := k8sClient.ListPods(ctx)
	if err != nil {
		return nil, err
	}

	whereaboutsPodRefs := getPodRefsServedByWhereabouts(ipPools)
	looper := &ReconcileLooper{
		k8sClient:           *k8sClient,
		liveWhereaboutsPods: indexPods(pods, whereaboutsPodRefs),
		requestTimeout:      timeout,
	}

	if err := looper.findOrphanedIPsPerPool(ipPools); err != nil {
		return nil, err
	}

	if err := looper.findClusterWideIPReservations(ctx); err != nil {
		return nil, err
	}
	return looper, nil
}

func (rl *ReconcileLooper) findOrphanedIPsPerPool(ipPools []storage.IPPool) error {
	for _, pool := range ipPools {
		orphanIP := OrphanedIPReservations{
			Pool: pool,
		}
		for _, ipReservation := range pool.Allocations() {
			logging.Debugf("the IP reservation: %s", ipReservation)
			if ipReservation.PodRef == "" {
				_ = logging.Errorf("pod ref missing for Allocations: %s", ipReservation)
				continue
			}
			if !rl.isPodAlive(ipReservation.PodRef, ipReservation.IP.String()) {
				logging.Debugf("pod ref %s is not listed in the live pods list", ipReservation.PodRef)
				orphanIP.Allocations = append(orphanIP.Allocations, ipReservation)
			}
		}
		if len(orphanIP.Allocations) > 0 {
			rl.orphanedIPs = append(rl.orphanedIPs, orphanIP)
		}
	}

	return nil
}

func (rl ReconcileLooper) isPodAlive(podRef string, ip string) bool {
	for livePodRef, livePod := range rl.liveWhereaboutsPods {
		if podRef == livePodRef {
			livePodIPs := livePod.ips
			logging.Debugf(
				"pod reference %s matches allocation; Allocation IP: %s; PodIPs: %s",
				livePodRef,
				ip,
				livePodIPs)
			_, isFound := livePodIPs[ip]
			return isFound || livePod.phase == v1.PodPending
		}
	}
	return false
}

func composePodRef(pod v1.Pod) string {
	return fmt.Sprintf("%s/%s", pod.GetNamespace(), pod.GetName())
}

func (rl ReconcileLooper) ReconcileIPPools(ctx context.Context) ([]net.IP, error) {
	matchByPodRef := func(reservations []types.IPReservation, podRef string) int {
		foundidx := -1
		for idx, v := range reservations {
			if v.PodRef == podRef {
				return idx
			}
		}
		return foundidx
	}

	var err error
	var totalCleanedUpIps []net.IP
	for _, orphanedIP := range rl.orphanedIPs {
		currentIPReservations := orphanedIP.Pool.Allocations()
		podRefsToDeallocate := findOutPodRefsToDeallocateIPsFrom(orphanedIP)
		var deallocatedIP net.IP
		for _, podRef := range podRefsToDeallocate {
			currentIPReservations, deallocatedIP, err = allocate.IterateForDeallocation(currentIPReservations, podRef, matchByPodRef)
			if err != nil {
				return nil, err
			}
		}

		logging.Debugf("Going to update the reserve list to: %+v", currentIPReservations)
		if err := orphanedIP.Pool.Update(ctx, currentIPReservations); err != nil {
			return nil, logging.Errorf("failed to update the reservation list: %v", err)
		}
		totalCleanedUpIps = append(totalCleanedUpIps, deallocatedIP)
	}

	return totalCleanedUpIps, nil
}

func (rl *ReconcileLooper) findClusterWideIPReservations(ctx context.Context) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Duration(rl.requestTimeout)*time.Second)
	defer cancel()

	clusterWideIPReservations, err := rl.k8sClient.ListOverlappingIPs(ctxWithTimeout)
	if err != nil {
		return logging.Errorf("failed to list all OverLappingIPs: %v", err)
	}

	for _, clusterWideIPReservation := range clusterWideIPReservations {
		ip := clusterWideIPReservation.GetName()
		// De-normalize the IP
		// In the UpdateOverlappingRangeAllocation function, the IP address is created with a "normalized" name to comply with the k8s api.
		// We must denormalize here in order to properly look up the IP address in the regular format, which pods use.
		denormalizedip := strings.ReplaceAll(ip, "-", ":")

		podRef := clusterWideIPReservation.Spec.PodRef

		if !rl.isPodAlive(podRef, denormalizedip) {
			logging.Debugf("pod ref %s is not listed in the live pods list", podRef)
			rl.orphanedClusterWideIPs = append(rl.orphanedClusterWideIPs, clusterWideIPReservation)
		}
	}

	return nil
}

func (rl ReconcileLooper) ReconcileOverlappingIPAddresses(ctx context.Context) error {
	var failedReconciledClusterWideIPs []string

	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Duration(rl.requestTimeout)*time.Second)
	defer cancel()

	for _, overlappingIPStruct := range rl.orphanedClusterWideIPs {
		if err := rl.k8sClient.DeleteOverlappingIP(ctxWithTimeout, &overlappingIPStruct); err != nil {
			logging.Errorf("failed to remove cluster wide IP: %s", overlappingIPStruct.GetName())
			failedReconciledClusterWideIPs = append(failedReconciledClusterWideIPs, overlappingIPStruct.GetName())
			continue
		}
		logging.Verbosef("removed stale overlappingIP allocation [%s]", overlappingIPStruct.GetName())
	}

	if len(failedReconciledClusterWideIPs) != 0 {
		return logging.Errorf("could not reconcile cluster wide IPs: %v", failedReconciledClusterWideIPs)
	}
	return nil
}

func findOutPodRefsToDeallocateIPsFrom(orphanedIP OrphanedIPReservations) []string {
	var podRefsToDeallocate []string
	for _, orphanedAllocation := range orphanedIP.Allocations {
		podRefsToDeallocate = append(podRefsToDeallocate, orphanedAllocation.PodRef)
	}
	return podRefsToDeallocate
}
