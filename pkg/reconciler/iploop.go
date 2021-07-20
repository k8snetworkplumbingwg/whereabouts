package reconciler

import (
	"context"
	"fmt"
	"net"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/storage"
	"github.com/dougbtv/whereabouts/pkg/storage/kubernetes"
	"github.com/dougbtv/whereabouts/pkg/types"
	v1 "k8s.io/api/core/v1"
)

type ReconcileLooper struct {
	ctx         context.Context
	k8sClient   kubernetes.Client
	livePods    map[string]podWrapper
	orphanedIPs []OrphanedIPReservations
}

type OrphanedIPReservations struct {
	Pool        storage.IPPool
	Allocations []types.IPReservation
}

func NewReconcileLooper(kubeConfigPath string, ctx context.Context) (*ReconcileLooper, error) {
	logging.Debugf("NewReconcileLooper - Kubernetes config file located at: %s", kubeConfigPath)
	k8sClient, err := kubernetes.NewClient(kubeConfigPath)
	if err != nil {
		return nil, logging.Errorf("failed to instantiate the Kubernetes client: %+v", err)
	}
	logging.Debugf("successfully read the kubernetes configuration file located at: %s", kubeConfigPath)

	pods, err := k8sClient.ListPods()
	if err != nil {
		return nil, err
	}

	looper := &ReconcileLooper{
		ctx:       ctx,
		k8sClient: *k8sClient,
		livePods:  indexPods(pods),
	}

	if err := looper.findOrphanedIPsPerPool(); err != nil {
		return nil, err
	}
	return looper, nil
}

func (rl *ReconcileLooper) findOrphanedIPsPerPool() error {
	ipPools, err := rl.k8sClient.ListIPPools(rl.ctx)
	if err != nil {
		return logging.Errorf("failed to retrieve all IP pools: %v", err)
	}

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
			if !rl.isPodAlive(ipReservation) {
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

func (rl ReconcileLooper) isPodAlive(allocation types.IPReservation) bool {
	for livePodRef, livePod := range rl.livePods {
		if allocation.PodRef == livePodRef {
			livePodIPs := livePod.ips
			logging.Debugf(
				"pod reference %s matches allocation; Allocation IP: %s; PodIPs: %s",
				livePodRef,
				allocation.IP.String(),
				livePodIPs)
			_, isFound := livePodIPs[allocation.IP.String()]
			return isFound
		}
	}
	return false
}

func composePodRef(pod v1.Pod) string {
	return fmt.Sprintf("%s/%s", pod.GetNamespace(), pod.GetName())
}

func (rl ReconcileLooper) ReconcileIPPools() ([]net.IP, error) {
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
		if err := orphanedIP.Pool.Update(rl.ctx, currentIPReservations); err != nil {
			return nil, logging.Errorf("failed to update the reservation list: %v", err)
		}
		totalCleanedUpIps = append(totalCleanedUpIps, deallocatedIP)
	}

	return totalCleanedUpIps, nil
}

func findOutPodRefsToDeallocateIPsFrom(orphanedIP OrphanedIPReservations) []string {
	var podRefsToDeallocate []string
	for _, orphanedAllocation := range orphanedIP.Allocations {
		podRefsToDeallocate = append(podRefsToDeallocate, orphanedAllocation.PodRef)
	}
	return podRefsToDeallocate
}
