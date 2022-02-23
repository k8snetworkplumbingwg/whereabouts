package controlloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	v1 "k8s.io/api/core/v1"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nadlister "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/listers/k8s.cni.cncf.io/v1"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wblister "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/listers/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

type handler struct {
	netAttachDefLister nadlister.NetworkAttachmentDefinitionLister
	ipPoolsLister      wblister.IPPoolLister
}

func (h *handler) deletePodHandler(obj interface{}) {
	oldPod := obj.(*v1.Pod)
	if oldPod == nil {
		_ = logging.Errorf("pod deleted but could not unmarshall into struct: %v", obj)
		return
	}

	podNamespace := oldPod.GetNamespace()
	podName := oldPod.GetName()
	logging.Verbosef("pod [%s] deleted", podID(podNamespace, podName))

	ifaceStatuses, err := podNetworkStatus(oldPod)
	if err != nil {
		logging.Errorf("failed to access the network status for pod [%s/%s]: %v", podName, podNamespace, err)
		return
	}

	for _, ifaceStatus := range ifaceStatuses {
		if ifaceStatus.Default {
			logging.Verbosef("skipped net-attach-def for default network")
			continue
		}
		nad, err := h.ifaceNetAttachDef(ifaceStatus)
		if err != nil {
			logging.Errorf("failed to get network-attachment-definition for iface %s: %+v", ifaceStatus.Name, err)
			return
		}

		logging.Verbosef("the NAD's config: %s", nad.Spec)
		ipamConfig, err := ipamConfiguration(nad, podNamespace, podName)
		if err != nil {
			logging.Errorf("failed to create an IPAM configuration for the pod %s iface %s: %+v", podID(podNamespace, podName), ifaceStatus.Name, err)
			return
		}

		pool, err := h.ipPool(ipamConfig.Range)
		if err != nil {
			logging.Errorf("failed to get the IPPool data: %+v", err)
			return
		}

		logging.Verbosef("pool range [%s]", pool.Spec.Range)
		for _, allocation := range pool.Spec.Allocations {
			if err := removeStaleIPAllocation(allocation, podNamespace, podName, ipamConfig); err != nil {
				logging.Errorf("failed to remove the allocation %v: %v", allocation, err)
				return
			}
		}
	}
}

func (h *handler) ifaceNetAttachDef(ifaceStatus nadv1.NetworkStatus) (*nadv1.NetworkAttachmentDefinition, error) {
	const (
		namespaceIndex = 0
		nameIndex      = 1
	)

	logging.Debugf("pod's network status: %+v", ifaceStatus)
	ifaceInfo := strings.Split(ifaceStatus.Name, "/")
	if len(ifaceInfo) < 2 {
		return nil, fmt.Errorf("pod %s name does not feature namespace/pod name syntax", ifaceStatus.Name)
	}

	netNamespaceName := ifaceInfo[namespaceIndex]
	netName := ifaceInfo[nameIndex]

	nad, err := h.netAttachDefLister.NetworkAttachmentDefinitions(netNamespaceName).Get(netName)
	if err != nil {
		return nil, err
	}
	return nad, nil
}

func (h *handler) ipPool(cidr string) (*whereaboutsv1alpha1.IPPool, error) {
	pool, err := h.ipPoolsLister.IPPools(ipPoolsNamespace()).Get(wbclient.NormalizeRange(cidr))
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func podNetworkStatus(pod *v1.Pod) ([]nadv1.NetworkStatus, error) {
	var ifaceStatuses []nadv1.NetworkStatus
	networkStatus, found := pod.Annotations[nadv1.NetworkStatusAnnot]
	if found {
		if err := json.Unmarshal([]byte(networkStatus), &ifaceStatuses); err != nil {
			return nil, err
		}
	}
	return ifaceStatuses, nil
}

func ipamConfiguration(nad *nadv1.NetworkAttachmentDefinition, podNamespace string, podName string) (*types.IPAMConfig, error) {
	const mountPath = "/host"
	const mounterWhereaboutsConfigFilePath = mountPath + "/etc/cni/net.d/whereabouts.d/whereabouts.conf"

	ipamConfig, err := config.LoadIPAMConfiguration([]byte(nad.Spec.Config), "", mounterWhereaboutsConfigFilePath)
	if err != nil {
		return nil, err
	}
	ipamConfig.PodName = podName
	ipamConfig.PodNamespace = podNamespace
	ipamConfig.Kubernetes.KubeConfigPath = mountPath + ipamConfig.Kubernetes.KubeConfigPath // must use the mount path

	return ipamConfig, nil
}

func removeStaleIPAllocation(allocation whereaboutsv1alpha1.IPAllocation, podNamespace string, podName string, ipamConfig *types.IPAMConfig) error {
	if allocation.PodRef == podID(podNamespace, podName) {
		logging.Verbosef("stale allocation to cleanup: %+v", allocation)

		if _, err := wbclient.IPManagement(context.TODO(), types.Deallocate, *ipamConfig, allocation.ContainerID, podID(podNamespace, podName)); err != nil {
			return err
		}
	}
	return nil
}

func ipPoolsNamespace() string {
	const wbNamespaceEnvVariableName = "WHEREABOUTS_NAMESPACE"
	if wbNamespace, found := os.LookupEnv(wbNamespaceEnvVariableName); found {
		return wbNamespace
	}

	const wbDefaultNamespace = "kube-system"
	return wbDefaultNamespace
}
