// This code is based on code from the following repository
// https://github.com/bcreane/k8sutils

package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	kubeClient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"k8s.io/apimachinery/pkg/util/wait"
)

func isIPPoolAllocationsEmpty(k8sIPAM *kubeClient.KubernetesIPAM, ipPoolCIDR string) wait.ConditionFunc {
	return func() (bool, error) {
		ipPool, err := k8sIPAM.GetIPPool(context.Background(), kubeClient.PoolIdentifier{IpRange: ipPoolCIDR, NetworkName: kubeClient.UnnamedNetwork})
		noPoolError := fmt.Errorf("k8s pool initialized")
		if errors.Is(err, noPoolError) {
			return true, nil
		} else if err != nil {
			return false, err
		}

		if len(ipPool.Allocations()) != 0 {
			return false, nil
		}

		return true, nil
	}
}

// WaitForZeroIPPoolAllocations polls up to timeout seconds for IP pool allocations to be gone from the Kubernetes cluster.
// Returns an error if any IP pool allocations remain after time limit, or if GETing IP pools causes an error.
func WaitForZeroIPPoolAllocations(k8sIPAM *kubeClient.KubernetesIPAM, ipPoolCIDR string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isIPPoolAllocationsEmpty(k8sIPAM, ipPoolCIDR))
}
