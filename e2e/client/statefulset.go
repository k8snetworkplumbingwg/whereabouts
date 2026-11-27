package client

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// WaitForStatefulSetGone waits until the named StatefulSet and its pods are deleted.
func WaitForStatefulSetGone(ctx context.Context, cs *kubernetes.Clientset, namespace, serviceName string, labelSelector string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, isStatefulSetGone(ctx, cs, serviceName, namespace, labelSelector))
}

func isStatefulSetGone(ctx context.Context, cs *kubernetes.Clientset, serviceName string, namespace string, labelSelector string) wait.ConditionWithContextFunc {
	return func(context.Context) (done bool, err error) {
		_, err = cs.AppsV1().StatefulSets(namespace).Get(ctx, serviceName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			// StatefulSet fully deleted — verify associated pods are also gone.
			associatedPods, listErr := cs.CoreV1().Pods(namespace).List(ctx, selectViaLabels(labelSelector))
			if listErr != nil {
				return false, listErr
			}
			return areAssociatedPodsGone(associatedPods), nil
		}
		if err != nil {
			return false, fmt.Errorf("failed to get stateful set %q: %w", serviceName, err)
		}
		// StatefulSet still exists (possibly with DeletionTimestamp) — keep waiting.
		return false, nil
	}
}

func selectViaLabels(labelSelector string) metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: labelSelector}
}

func areAssociatedPodsGone(pods *corev1.PodList) bool {
	return len(pods.Items) == 0
}

func WaitForStatefulSetCondition(ctx context.Context, cs *kubernetes.Clientset, namespace, serviceName string, expectedReplicas int, timeout time.Duration, predicate statefulSetPredicate) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, doesStatefulsetComplyWithCondition(ctx, cs, serviceName, namespace, expectedReplicas, predicate))
}

func doesStatefulsetComplyWithCondition(ctx context.Context, cs *kubernetes.Clientset, serviceName string, namespace string, expectedReplicas int, predicate statefulSetPredicate) wait.ConditionWithContextFunc {
	return func(context.Context) (bool, error) {
		statefulSet, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, serviceName, metav1.GetOptions{})
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
