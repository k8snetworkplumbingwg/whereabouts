package reconciler

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/allocate"
	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"

	v1 "k8s.io/api/core/v1"
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
	return newReconcileLooper(ctx, k8sClient, timeout)
}

func NewReconcileLooper(ctx context.Context, timeout int) (*ReconcileLooper, error) {
	logging.Debugf("NewReconcileLooper - inferred connection data")
	k8sClient, err := kubernetes.NewClient(time.Duration(timeout) * time.Second)
	if err != nil {
		return nil, logging.Errorf("failed to instantiate the Kubernetes client: %+v", err)
	}
	return newReconcileLooper(ctx, k8sClient, timeout)
}

func newReconcileLooper(ctx context.Context, k8sClient *kubernetes.Client, timeout int) (*ReconcileLooper, error) {
	pods, err := k8sClient.ListPods(ctx)
	if err != nil {
		return nil, err
	}

	ipPools, err := k8sClient.ListIPPools(ctx)
	if err != nil {
		return nil, logging.Errorf("failed to retrieve all IP pools: %v", err)
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
			isFound := isIpOnPod(&livePod, podRef, ip)
			if !isFound && (livePod.phase == v1.PodPending) {
				/* Sometimes pods are still coming up, and may not yet have Multus
				 * annotation added to it yet. We don't want to check the IPs yet
				 * so re-fetch the Pod 5x
				 */
				podToMatch := &livePod
				retries := 0

				logging.Debugf("Re-fetching Pending Pod: %s IP-to-match: %s", livePodRef, ip)

				for retries < storage.PodRefreshRetries {
					retries += 1
					podToMatch = rl.refreshPod(livePodRef)
					if podToMatch == nil {
						logging.Debugf("Cleaning up...")
						return false
					} else if podToMatch.phase != v1.PodPending {
						logging.Debugf("Pending Pod is now in phase: %s", podToMatch.phase)
						break
					} else {
						isFound = isIpOnPod(podToMatch, podRef, ip)
						// Short-circuit - Pending Pod may have IP now
						if isFound {
							logging.Debugf("Pod now has IP annotation while in Pending")
							return true
						}
						time.Sleep(time.Duration(500) * time.Millisecond)
					}
				}
				isFound = isIpOnPod(podToMatch, podRef, ip)
			}

			return isFound
		}
	}
	return false
}

func (rl ReconcileLooper) refreshPod(podRef string) *podWrapper {
	namespace, podName := splitPodRef(podRef)
	if namespace == "" || podName == "" {
		logging.Errorf("Invalid podRef format: %s", podRef)
		return nil
	}

	pod, err := rl.k8sClient.GetPod(namespace, podName)
	if err != nil {
		logging.Errorf("Failed to refresh Pod %s: %s\n", podRef, err)
		return nil
	}

	wrappedPod := wrapPod(*pod)
	logging.Debugf("Got refreshed pod: %v", wrappedPod)
	return wrappedPod
}

func splitPodRef(podRef string) (string, string) {
	namespacedName := strings.Split(podRef, "/")
	if len(namespacedName) != 2 {
		logging.Errorf("Failed to split podRef %s", podRef)
		return "", ""
	}

	return namespacedName[0], namespacedName[1]
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
		podRef := clusterWideIPReservation.Spec.PodRef

		if !rl.isPodAlive(podRef, ip) {
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
