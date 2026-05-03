package client

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// WaitForDeploymentAvailable polls up to timeout seconds for the given Deployment to have
// at least one available replica. Returns an error if the condition is never met.
func WaitForDeploymentAvailable(ctx context.Context, cs *kubernetes.Clientset, namespace, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, isDeploymentAvailable(ctx, cs, namespace, name))
}

func isDeploymentAvailable(ctx context.Context, cs *kubernetes.Clientset, namespace, name string) wait.ConditionWithContextFunc {
	return func(context.Context) (bool, error) {
		deployment, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return deployment.Status.AvailableReplicas > 0, nil
	}
}
