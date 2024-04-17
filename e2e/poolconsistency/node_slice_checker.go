package poolconsistency

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/e2e/retrievers"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
)

type NodeSliceChecker struct {
	ipPools []storage.IPPool
	podList []corev1.Pod
}

func NewNodeSliceConsistencyCheck(ipPools []storage.IPPool, podList []corev1.Pod) *NodeSliceChecker {
	return &NodeSliceChecker{
		ipPools: ipPools,
		podList: podList,
	}
}

func (pc *NodeSliceChecker) MissingIPs() []string {
	var mismatchedIPs []string
	for _, pod := range pc.podList {
		podIPs, err := retrievers.SecondaryIfaceIPValue(&pod)
		podIP := podIPs[len(podIPs)-1]
		if err != nil {
			return []string{}
		}

		var found bool
		for _, pool := range pc.ipPools {
			for _, allocation := range pool.Allocations() {
				reservedIP := allocation.IP.String()

				if reservedIP == podIP {
					found = true
					break
				}
			}
		}
		if !found {
			mismatchedIPs = append(mismatchedIPs, podIP)
		}
	}
	return mismatchedIPs
}

func (pc *NodeSliceChecker) StaleIPs() []string {
	var staleIPs []string
	for _, pool := range pc.ipPools {
		for _, allocation := range pool.Allocations() {
			reservedIP := allocation.IP.String()
			found := false
			for _, pod := range pc.podList {
				podIPs, err := retrievers.SecondaryIfaceIPValue(&pod)
				podIP := podIPs[len(podIPs)-1]
				if err != nil {
					continue
				}

				if reservedIP == podIP {
					found = true
					break
				}
			}

			if !found {
				staleIPs = append(staleIPs, allocation.IP.String())
			}
		}
	}

	return staleIPs
}
