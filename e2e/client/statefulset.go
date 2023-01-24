package client

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// WaitForStatefulSetGone ...
func WaitForStatefulSetGone(cs *kubernetes.Clientset, namespace, serviceName string, labelSelector string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isStatefulSetGone(cs, serviceName, namespace, labelSelector))
}

func isStatefulSetGone(cs *kubernetes.Clientset, serviceName string, namespace string, labelSelector string) wait.ConditionFunc {
	return func() (done bool, err error) {
		statefulSet, err := cs.AppsV1().StatefulSets(namespace).Get(context.Background(), serviceName, metav1.GetOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("something weird happened with the stateful set whose status is: [%s]. Errors: %w", statefulSet.Status.String(), err)
		}

		associatedPods, err := cs.CoreV1().Pods(namespace).List(context.TODO(), selectViaLabels(labelSelector))
		if err != nil {
			return false, err
		}

		return isStatefulSetEmpty(statefulSet) && areAssociatedPodsGone(associatedPods), nil
	}
}

func selectViaLabels(labelSelector string) metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: labelSelector}
}

func isStatefulSetEmpty(statefulSet *appsv1.StatefulSet) bool {
	return statefulSet.Status.CurrentReplicas == int32(0)
}

func areAssociatedPodsGone(pods *corev1.PodList) bool {
	return len(pods.Items) == 0
}

func WaitForStatefulSetCondition(cs *kubernetes.Clientset, namespace, serviceName string, expectedReplicas int, timeout time.Duration, predicate statefulSetPredicate) error {
	return wait.PollImmediate(time.Second, timeout, doesStatefulsetComplyWithCondition(cs, serviceName, namespace, expectedReplicas, predicate))
}

func doesStatefulsetComplyWithCondition(cs *kubernetes.Clientset, serviceName string, namespace string, expectedReplicas int, predicate statefulSetPredicate) wait.ConditionFunc {
	return func() (bool, error) {
		statefulSet, err := cs.AppsV1().StatefulSets(namespace).Get(context.Background(), serviceName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return predicate(statefulSet, expectedReplicas), nil
	}
}

func IsStatefulSetReadyPredicate(statefulSet *appsv1.StatefulSet, expectedReplicas int) bool {
	return statefulSet.Status.ReadyReplicas == int32(expectedReplicas)
}

func IsStatefulSetDegradedPredicate(statefulSet *appsv1.StatefulSet, expectedReplicas int) bool {
	return statefulSet.Status.ReadyReplicas < int32(expectedReplicas)
}
