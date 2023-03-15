//go:build test
// +build test

package controlloop

import (
	"encoding/json"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nad "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
)

func dummyNetSpec(networkName string, ipRange string) string {
	return fmt.Sprintf(`{
      "cniVersion": "0.3.0",
      "name": "%s",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "%s"
      }
    }`, networkName, ipRange)
}

func dummyNonWhereaboutsIPAMNetSpec(networkName string) string {
	return fmt.Sprintf(`{
      "cniVersion": "0.3.0",
      "name": "%s",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "static",
        "addresses": [
          {
			"address": "10.10.0.1/24",
			"gateway": "10.10.0.254"
		  }
        ]
      }
    }`, networkName)
}

func nodeSpec(name string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func podSpec(name string, namespace string, nodeName string, networks ...string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: podNetworkSelectionElements(networks...),
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
}

func netAttachDef(netName string, namespace string, config string) nad.NetworkAttachmentDefinition {
	return nad.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      netName,
			Namespace: namespace,
		},
		Spec: nad.NetworkAttachmentDefinitionSpec{
			Config: config,
		},
	}
}

func podNetworkSelectionElements(networkNames ...string) map[string]string {
	return map[string]string{
		nad.NetworkAttachmentAnnot: strings.Join(networkNames, ","),
		nad.NetworkStatusAnnot:     podNetworkStatusAnnotations("default", networkNames...),
	}
}

func podNetworkStatusAnnotations(namespace string, networkNames ...string) string {
	var netStatus []nad.NetworkStatus
	for i, networkName := range networkNames {
		netStatus = append(
			netStatus,
			nad.NetworkStatus{
				Name:      fmt.Sprintf("%s/%s", namespace, networkName),
				Interface: fmt.Sprintf("net%d", i),
			})
	}
	serelizedNetStatus, err := json.Marshal(netStatus)
	if err != nil {
		return ""
	}
	return string(serelizedNetStatus)
}

func ipPool(ipRange string, namespace string, podReferences ...string) *v1alpha1.IPPool {
	return &v1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubernetes.NormalizeRange(ipRange),
			Namespace: namespace,
		},
		Spec: v1alpha1.IPPoolSpec{
			Range:       ipRange,
			Allocations: allocations(podReferences...),
		},
	}
}

func allocations(podReferences ...string) map[string]v1alpha1.IPAllocation {
	poolAllocations := map[string]v1alpha1.IPAllocation{}
	for i, podRef := range podReferences {
		poolAllocations[fmt.Sprintf("%d", i)] = v1alpha1.IPAllocation{
			ContainerID: "",
			PodRef:      podRef,
		}
	}
	return poolAllocations
}
