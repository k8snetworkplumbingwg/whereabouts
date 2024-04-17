package util

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	wbtestclient "github.com/k8snetworkplumbingwg/whereabouts/e2e/client"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/entities"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbstorage "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
)

const (
	CreatePodTimeout = 10 * time.Second
)

func AllocationForPodRef(podRef string, ipPool v1alpha1.IPPool) *v1alpha1.IPAllocation {
	for _, allocation := range ipPool.Spec.Allocations {
		if allocation.PodRef == podRef {
			return &allocation
		}
	}
	return nil
}

func ClusterConfig() (*rest.Config, error) {
	const kubeconfig = "KUBECONFIG"

	kubeconfigPath, found := os.LookupEnv(kubeconfig)
	if !found {
		return nil, fmt.Errorf("must provide the path to the kubeconfig via the `KUBECONFIG` env variable")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func PodTierLabel(podTier string) map[string]string {
	const tier = "tier"
	return map[string]string{tier: podTier}
}

// This will check that the count of subnets has been created and that each node has a unique allocation
// NOTE: this requires that there are not more nodes than subnets in the nodeslicepool
func ValidateNodeSlicePoolSlicesCreatedAndNodesAssigned(nodesliceName string, nodeSliceNamespace string, expectedSubnets int, clientInfo *wbtestclient.ClientInfo) error {
	nodeSlice, err := clientInfo.GetNodeSlicePool(nodesliceName, nodeSliceNamespace)
	if err != nil {
		return err
	}
	// Should create subnets
	if len(nodeSlice.Status.Allocations) != expectedSubnets {
		return fmt.Errorf("expected allocations %v but got allocations %v", expectedSubnets, len(nodeSlice.Status.Allocations))
	}
	// Each subnet should have a unique range
	allocationMap := map[string]struct{}{}
	nodeMap := map[string]struct{}{}
	for _, allocation := range nodeSlice.Status.Allocations {
		if _, ok := allocationMap[allocation.SliceRange]; ok {
			return fmt.Errorf("error allocation has duplication in subnet %v", allocation.SliceRange)
		}
		if _, ok := allocationMap[allocation.NodeName]; allocation.NodeName != "" && ok {
			return fmt.Errorf("error allocation has duplication in nodes %v", allocation.NodeName)
		}
		allocationMap[allocation.SliceRange] = struct{}{}
		nodeMap[allocation.NodeName] = struct{}{}
	}
	// All nodes should be assigned exactly one time
	nodes, err := clientInfo.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, node := range nodes.Items {
		if _, ok := nodeMap[node.Name]; !ok {
			//TODO: CP nodes?
			return fmt.Errorf("node not assinged to slice %v", node.Name)
		}
	}
	return nil
}

// Waits for all replicas to be fully removed from replicaset, and checks that there are 0 ip pool allocations
func CheckZeroIPPoolAllocationsAndReplicas(ctx context.Context, clientInfo *wbtestclient.ClientInfo, k8sIPAM *wbstorage.KubernetesIPAM, rsName, namespace string, ipPoolCIDR string, networkNames ...string) error {
	const (
		emptyReplicaSet   = 0
		rsSteadyTimeout   = 1200 * time.Second
		zeroIPPoolTimeout = 2 * time.Minute
	)
	var err error

	replicaSet, err := clientInfo.UpdateReplicaSet(
		entities.ReplicaSetObject(
			emptyReplicaSet,
			rsName,
			namespace,
			PodTierLabel(rsName),
			entities.PodNetworkSelectionElements(networkNames...),
		))
	if err != nil {
		return err
	}

	matchingLabel := entities.ReplicaSetQuery(rsName)
	if err = wbtestclient.WaitForReplicaSetSteadyState(ctx, clientInfo.Client, namespace, matchingLabel, replicaSet, rsSteadyTimeout); err != nil {
		return err
	}

	if k8sIPAM.Config.NodeSliceSize == "" {
		if err = wbtestclient.WaitForZeroIPPoolAllocations(ctx, k8sIPAM, ipPoolCIDR, zeroIPPoolTimeout); err != nil {
			return err
		}
	} else {
		if err = wbtestclient.WaitForZeroIPPoolAllocationsAcrossNodeSlices(k8sIPAM, ipPoolCIDR, zeroIPPoolTimeout, clientInfo); err != nil {
			return err
		}
	}

	return nil
}

// Returns a network attachment definition object configured by provided parameters
func GenerateNetAttachDefSpec(name, namespace, config string) *nettypes.NetworkAttachmentDefinition {
	return &nettypes.NetworkAttachmentDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "NetworkAttachmentDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: nettypes.NetworkAttachmentDefinitionSpec{
			Config: config,
		},
	}
}

func MacvlanNetworkWithWhereaboutsIPAMNetwork(networkName string, namespaceName string, ipRange string, ipRanges []string, poolName string, enableOverlappingRanges bool) *nettypes.NetworkAttachmentDefinition {
	macvlanConfig := fmt.Sprintf(`{
        "cniVersion": "0.3.0",
        "disableCheck": true,
        "plugins": [
            {
                "type": "macvlan",
                "master": "eth0",
                "mode": "bridge",
                "ipam": {
                    "type": "whereabouts",
                    "leader_lease_duration": 1500,
                    "leader_renew_deadline": 1000,
                    "leader_retry_period": 500,
                    "range": "%s",
                    "ipRanges": %s,
                    "log_level": "debug",
                    "log_file": "/tmp/wb",
                    "network_name": "%s",
                    "enable_overlapping_ranges": %v
                }
            }
        ]
    }`, ipRange, CreateIPRanges(ipRanges), poolName, enableOverlappingRanges)
	return GenerateNetAttachDefSpec(networkName, namespaceName, macvlanConfig)
}

func MacvlanNetworkWithNodeSlice(networkName, namespaceName, ipRange, poolName, sliceSize string) *nettypes.NetworkAttachmentDefinition {
	//TODO: fails without leader timeouts set
	macvlanConfig := fmt.Sprintf(`{
        "cniVersion": "0.3.0",
        "disableCheck": true,
        "plugins": [
            {
                "type": "macvlan",
                "master": "eth0",
                "mode": "bridge",
                "ipam": {
                    "type": "whereabouts",
                    "leader_lease_duration": 1500,
                    "leader_renew_deadline": 1000,
                    "leader_retry_period": 500,
					"namespace": "%s",
                    "range": "%s",
                    "log_level": "debug",
                    "log_file": "/tmp/wb",
                    "network_name": "%s",
					"node_slice_size": "%s"
                }
            }
        ]
    }`, namespaceName, ipRange, poolName, sliceSize)
	return GenerateNetAttachDefSpec(networkName, namespaceName, macvlanConfig)
}

func InNodeRange(clientInfo *wbtestclient.ClientInfo, nodeName, sliceName, namespace, ip string) error {
	cidrRange, err := wbtestclient.GetNodeSubnet(clientInfo, nodeName, sliceName, namespace)
	if err != nil {
		return err
	}

	return InRange(cidrRange, ip)
}

func InRange(cidr string, ip string) error {
	_, cidrRange, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}

	if cidrRange.Contains(net.ParseIP(ip)) {
		return nil
	}

	return fmt.Errorf("ip [%s] is NOT in range %s", ip, cidr)
}

func CreateIPRanges(ranges []string) string {
	formattedRanges := []string{}
	for _, ipRange := range ranges {
		singleRange := fmt.Sprintf(`{"range": "%s"}`, ipRange)
		formattedRanges = append(formattedRanges, singleRange)
	}
	ipRanges := "[" + strings.Join(formattedRanges[:], ",") + "]"
	return ipRanges
}
