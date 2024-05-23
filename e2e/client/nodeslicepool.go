package client

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func GetNodeSubnet(cs *ClientInfo, nodeName, sliceName, namespace string) (string, error) {
	slice, err := cs.WbClient.WhereaboutsV1alpha1().NodeSlicePools(namespace).Get(context.TODO(), sliceName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	for _, allocation := range slice.Status.Allocations {
		if allocation.NodeName == nodeName {
			return allocation.SliceRange, nil
		}
	}
	return "", fmt.Errorf("slice range not found for node")
}

func WaitForNodeSliceReady(ctx context.Context, cs *ClientInfo, namespace, nodeSliceName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, isNodeSliceReady(ctx, cs, namespace, nodeSliceName))
}

func isNodeSliceReady(ctx context.Context, cs *ClientInfo, namespace, nodeSliceName string) wait.ConditionWithContextFunc {
	return func(context.Context) (bool, error) {
		_, err := cs.WbClient.WhereaboutsV1alpha1().NodeSlicePools(namespace).Get(ctx, nodeSliceName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		return true, nil
	}
}
