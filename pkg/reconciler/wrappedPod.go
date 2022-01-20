package reconciler

import (
	"encoding/json"

	k8snetworkplumbingwgv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"

	v1 "k8s.io/api/core/v1"
)

const (
	multusInterfaceNamePrefix     = "net"
	multusPrefixSize              = len(multusInterfaceNamePrefix)
	MultusNetworkAnnotation       = "k8s.v1.cni.cncf.io/networks"
	MultusNetworkStatusAnnotation = "k8s.v1.cni.cncf.io/networks-status"
)

type podWrapper struct {
	ips   map[string]void
	phase v1.PodPhase
}

type void struct{}

func wrapPod(pod v1.Pod) *podWrapper {
	return &podWrapper{
		ips: getFlatIPSet(pod),
		phase: pod.Status.Phase,
	}
}

func getPodRefsServedByWhereabouts(ipPools []storage.IPPool) map[string]void {
	whereaboutsPodRefs := map[string]void{}
	for _, pool := range ipPools {
		for _, ipReservation := range pool.Allocations() {
			whereaboutsPodRefs[ipReservation.PodRef] = void{}
		}
	}
	return whereaboutsPodRefs
}

func indexPods(livePodList []v1.Pod, whereaboutsPodNames map[string]void) map[string]podWrapper {
	podMap := map[string]podWrapper{}

	for _, pod := range livePodList {
		podRef := composePodRef(pod)
		if _, isWhereaboutsPod := whereaboutsPodNames[podRef]; !isWhereaboutsPod {
			continue
		}
		wrappedPod := wrapPod(pod)
		if wrappedPod != nil {
			podMap[podRef] = *wrappedPod
		}
	}
	return podMap
}

func getFlatIPSet(pod v1.Pod) map[string]void {
	var empty void
	ipSet := map[string]void{}
	networkStatusAnnotationValue := []byte(pod.Annotations[MultusNetworkStatusAnnotation])
	var networkStatusList []k8snetworkplumbingwgv1.NetworkStatus
	if err := json.Unmarshal(networkStatusAnnotationValue, &networkStatusList); err != nil {
		_ = logging.Errorf("could not parse network annotation %s for pod: %s; error: %v", networkStatusAnnotationValue, composePodRef(pod), err)
		return ipSet
	}

	for _, network := range networkStatusList {
		// we're only after multus secondary interfaces
		if network.Default {
			continue
		}

		for _, ip := range network.IPs {
			ipSet[ip] = empty
			logging.Debugf("Added IP %s for pod %s", ip, composePodRef(pod))
		}
	}
	return ipSet
}
