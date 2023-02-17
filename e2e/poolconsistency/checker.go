package poolconsistency

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/e2e/retrievers"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
)

type Checker struct {
	ipPool  storage.IPPool
	podList []corev1.Pod
}

func NewPoolConsistencyCheck(ipPool storage.IPPool, podList []corev1.Pod) *Checker {
	return &Checker{
		ipPool:  ipPool,
		podList: podList,
	}
}

func (pc *Checker) MissingIPs() []string {
	var mismatchedIPs []string
	for _, pod := range pc.podList {
		podIPs, err := retrievers.SecondaryIfaceIPValue(&pod)
		podIP := podIPs[len(podIPs)-1]
		if err != nil {
			return []string{}
		}

		var found bool
		for _, allocation := range pc.ipPool.Allocations() {
			reservedIP := allocation.IP.String()

			if reservedIP == podIP {
				found = true
				break
			}
		}

		if !found {
			mismatchedIPs = append(mismatchedIPs, podIP)
		}
	}
	return mismatchedIPs
}

func (pc *Checker) StaleIPs() []string {
	var staleIPs []string
	for _, allocation := range pc.ipPool.Allocations() {
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
	return staleIPs
}
