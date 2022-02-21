// This code is based on code from the following repository
// https://github.com/bcreane/k8sutils

package whereabouts_e2e

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// return a condition function that indicates whether the given pod is
// currently running
func isPodRunning(cs *kubernetes.Clientset, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		fmt.Printf(".") // progress bar!

		pod, err := cs.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		switch pod.Status.Phase {
		case v1.PodRunning:
			return true, nil
		case v1.PodFailed:
			return false, errors.New("pod failed")
		case v1.PodSucceeded:
			return false, errors.New("pod succeeded")
		}

		return false, nil
	}
}

// Poll up to timeout seconds for pod to enter steady state (running or succeeded state).
// Returns an error if the pod never enters a steady state.
func WaitForPodReady(cs *kubernetes.Clientset, namespace, podName string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isPodRunning(cs, podName, namespace))
}

// Returns the list of currently scheduled or running pods in `namespace` with the given selector
func ListPods(cs *kubernetes.Clientset, namespace, selector string) (*v1.PodList, error) {
	listOptions := metav1.ListOptions{LabelSelector: selector}
	podList, err := cs.CoreV1().Pods(namespace).List(context.Background(), listOptions)

	if err != nil {
		return nil, err
	}
	return podList, nil
}

// Wait up to timeout seconds for all pods in 'namespace' with given 'selector' to enter provided state
// If no pods are found, return nil.
func WaitForPodBySelector(cs *kubernetes.Clientset, namespace, selector string, timeout int) error {
	podList, err := ListPods(cs, namespace, selector)
	if err != nil {
		return err
	}

	// if there are no pods that match
	if len(podList.Items) == 0 {
		return nil
	}

	for _, pod := range podList.Items {
		if err := WaitForPodReady(cs, namespace, pod.Name, time.Duration(timeout)*time.Second); err != nil {
			return err
		}
	}
	return nil
}
