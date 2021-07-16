package reconciler

import (
	"encoding/json"

	"github.com/dougbtv/whereabouts/pkg/logging"
	k8snetworkplumbingwgv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	v1 "k8s.io/api/core/v1"
)

const (
	multusInterfaceNamePrefix     = "net"
	multusPrefixSize              = len(multusInterfaceNamePrefix)
	MultusNetworkAnnotation       = "k8s.v1.cni.cncf.io/networks"
	MultusNetworkStatusAnnotation = "k8s.v1.cni.cncf.io/networks-status"
)

type podWrapper struct {
	ips map[string]void
}

type void struct{}

func wrapPod(pod v1.Pod) *podWrapper {
	return &podWrapper{
		ips: getFlatIPSet(pod),
	}
}

func indexPods(podList []v1.Pod) map[string]podWrapper {
	podMap := map[string]podWrapper{}

	for _, pod := range podList {
		wrappedPod := wrapPod(pod)
		if wrappedPod != nil {
			podMap[composePodRef(pod)] = *wrappedPod
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
		if network.Interface[:multusPrefixSize] == multusInterfaceNamePrefix {
			for _, ip := range network.IPs {
				ipSet[ip] = empty
			}
		}
	}
	return ipSet
}
