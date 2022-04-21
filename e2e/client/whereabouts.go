package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/typed/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/e2e/entities"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
)

const (
	createTimeout   = 10 * time.Second
	deleteTimeout   = 2 * createTimeout
	rsCreateTimeout = 600 * time.Second
)

type statefulSetPredicate func(statefulSet *appsv1.StatefulSet, expectedReplicas int) bool

type ClientInfo struct {
	Client    *kubernetes.Clientset
	NetClient netclient.K8sCniCncfIoV1Interface
	WbClient  wbclient.Interface
}

func NewClientInfo(config *rest.Config) (*ClientInfo, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	netClient, err := netclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	wbClient, err := wbclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &ClientInfo{
		Client:    clientSet,
		NetClient: netClient,
		WbClient:  wbClient,
	}, nil
}

func (c *ClientInfo) AddNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) (*nettypes.NetworkAttachmentDefinition, error) {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Create(context.TODO(), netattach, metav1.CreateOptions{})
}

func (c *ClientInfo) DelNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) error {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Delete(context.TODO(), netattach.Name, metav1.DeleteOptions{})
}

func (c *ClientInfo) ProvisionPod(podName string, namespace string, label, annotations map[string]string) (*corev1.Pod, error) {
	pod := entities.PodObject(podName, namespace, label, annotations)
	pod, err := c.Client.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	const podCreateTimeout = 10 * time.Second
	if err := WaitForPodReady(c.Client, pod.Namespace, pod.Name, podCreateTimeout); err != nil {
		return nil, err
	}

	pod, err = c.Client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

func (c *ClientInfo) DeletePod(pod *corev1.Pod) error {
	if err := c.Client.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	const podDeleteTimeout = 20 * time.Second
	if err := WaitForPodToDisappear(c.Client, pod.GetNamespace(), pod.GetName(), podDeleteTimeout); err != nil {
		return err
	}
	return nil
}

func (c *ClientInfo) ProvisionReplicaSet(rsName string, namespace string, replicaCount int32, labels, annotations map[string]string) (*appsv1.ReplicaSet, error) {
	replicaSet, err := c.Client.AppsV1().ReplicaSets(namespace).Create(
		context.Background(),
		entities.ReplicaSetObject(replicaCount, rsName, namespace, labels, annotations),
		metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	const rsCreateTimeout = 600 * time.Second
	if err := WaitForPodBySelector(c.Client, namespace, entities.ReplicaSetQuery(rsName), rsCreateTimeout); err != nil {
		return nil, err
	}

	replicaSet, err = c.Client.AppsV1().ReplicaSets(namespace).Get(context.Background(), replicaSet.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return replicaSet, nil
}

func (c *ClientInfo) UpdateReplicaSet(replicaSet *appsv1.ReplicaSet) (*appsv1.ReplicaSet, error) {
	replicaSet, err := c.Client.AppsV1().ReplicaSets(replicaSet.GetNamespace()).Update(context.Background(), replicaSet, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}
	return replicaSet, nil
}

func (c *ClientInfo) DeleteReplicaSet(replicaSet *appsv1.ReplicaSet) error {
	const rsDeleteTimeout = 2 * rsCreateTimeout
	if err := c.Client.AppsV1().ReplicaSets(replicaSet.GetNamespace()).Delete(context.Background(), replicaSet.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	if err := WaitForReplicaSetToDisappear(c.Client, replicaSet.GetNamespace(), replicaSet.GetName(), rsDeleteTimeout); err != nil {
		return err
	}
	return nil
}

func (c *ClientInfo) ProvisionStatefulSet(statefulSetName string, namespace string, serviceName string, replicas int, networkNames ...string) (*appsv1.StatefulSet, error) {
	const statefulSetCreateTimeout = 60 * createTimeout
	statefulSet, err := c.Client.AppsV1().StatefulSets(namespace).Create(
		context.TODO(),
		entities.StatefulSetSpec(statefulSetName, namespace, serviceName, replicas, entities.PodNetworkSelectionElements(networkNames...)),
		metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := WaitForStatefulSetCondition(
		c.Client,
		namespace,
		serviceName,
		replicas,
		statefulSetCreateTimeout,
		IsStatefulSetReadyPredicate); err != nil {
		return nil, err
	}
	return statefulSet, nil
}

func (c *ClientInfo) DeleteStatefulSet(namespace string, serviceName string, labelSelector string) error {
	const statefulSetDeleteTimeout = 6 * deleteTimeout

	if err := c.Client.AppsV1().StatefulSets(namespace).Delete(
		context.TODO(), serviceName, deleteRightNowAndBlockUntilAssociatedPodsAreGone()); err != nil {
		return err
	}

	return WaitForStatefulSetGone(c.Client, namespace, serviceName, labelSelector, statefulSetDeleteTimeout)
}

func (c *ClientInfo) ScaleStatefulSet(statefulSetName string, namespace string, deltaInstance int) error {
	statefulSet, err := c.Client.AppsV1().StatefulSets(namespace).Get(context.TODO(), statefulSetName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	newReplicas := *statefulSet.Spec.Replicas + int32(deltaInstance)
	statefulSet.Spec.Replicas = &newReplicas

	if _, err := c.Client.AppsV1().StatefulSets(namespace).Update(context.TODO(), statefulSet, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

func deleteRightNowAndBlockUntilAssociatedPodsAreGone() metav1.DeleteOptions {
	var (
		blockUntilAssociatedPodsAreGone = metav1.DeletePropagationForeground
		rightNow                        = int64(0)
	)
	return metav1.DeleteOptions{GracePeriodSeconds: &rightNow, PropagationPolicy: &blockUntilAssociatedPodsAreGone}
}

// WaitForPodReady polls up to timeout seconds for pod to enter steady state (running or succeeded state).
// Returns an error if the pod never enters a steady state.
func WaitForPodReady(cs *kubernetes.Clientset, namespace, podName string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isPodRunning(cs, podName, namespace))
}

// WaitForPodToDisappear polls up to timeout seconds for pod to be gone from the Kubernetes cluster.
// Returns an error if the pod is never deleted, or if GETing it returns an error other than `NotFound`.
func WaitForPodToDisappear(cs *kubernetes.Clientset, namespace, podName string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isPodGone(cs, podName, namespace))
}

// WaitForPodBySelector waits up to timeout seconds for all pods in 'namespace' with given 'selector' to enter provided state
// If no pods are found, return nil.
func WaitForPodBySelector(cs *kubernetes.Clientset, namespace, selector string, timeout time.Duration) error {
	podList, err := ListPods(cs, namespace, selector)
	if err != nil {
		return err
	}

	if len(podList.Items) == 0 {
		return nil
	}

	for _, pod := range podList.Items {
		if err := WaitForPodReady(cs, namespace, pod.Name, timeout); err != nil {
			return err
		}
	}
	return nil
}

// WaitForReplicaSetSteadyState only plays nice with the replicaSet it's being used with.
// Any pods that might be up still from a previous test may cause unexpected results.
func WaitForReplicaSetSteadyState(cs *kubernetes.Clientset, namespace, label string, replicaSet *appsv1.ReplicaSet, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isReplicaSetSteady(cs, replicaSet.Name, namespace, label))
}

// WaitForReplicaSetToDisappear polls up to timeout seconds for replicaset to be gone from the Kubernetes cluster.
// Returns an error if the replicaset is never deleted, or if GETing it returns an error other than `NotFound`.
func WaitForReplicaSetToDisappear(cs *kubernetes.Clientset, namespace, rsName string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isReplicaSetGone(cs, rsName, namespace))
}

// WaitForStatefulSetGone ...
func WaitForStatefulSetGone(cs *kubernetes.Clientset, namespace, serviceName string, labelSelector string, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, isStatefulSetGone(cs, serviceName, namespace, labelSelector))
}

// ListPods returns the list of currently scheduled or running pods in `namespace` with the given selector
func ListPods(cs *kubernetes.Clientset, namespace, selector string) (*corev1.PodList, error) {
	listOptions := metav1.ListOptions{LabelSelector: selector}
	podList, err := cs.CoreV1().Pods(namespace).List(context.Background(), listOptions)

	if err != nil {
		return nil, err
	}
	return podList, nil
}

func isPodRunning(cs *kubernetes.Clientset, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := cs.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return true, nil
		case corev1.PodFailed:
			return false, errors.New("pod failed")
		case corev1.PodSucceeded:
			return false, errors.New("pod succeeded")
		}

		return false, nil
	}
}

func isPodGone(cs *kubernetes.Clientset, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := cs.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil && k8serrors.IsNotFound(err) {
			return true, nil
		} else if err != nil {
			return false, fmt.Errorf("something weird happened with the pod, which is in state: [%s]. Errors: %w", pod.Status.Phase, err)
		}

		return false, nil
	}
}

func isReplicaSetSteady(cs *kubernetes.Clientset, replicaSetName, namespace, label string) wait.ConditionFunc {
	return func() (bool, error) {
		podList, err := ListPods(cs, namespace, label)
		if err != nil {
			return false, err
		}

		replicaSet, err := cs.AppsV1().ReplicaSets(namespace).Get(context.Background(), replicaSetName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if isReplicaSetSynchronized(replicaSet, podList) {
			return true, nil
		} else {
			return false, nil
		}
	}
}

// check two things:
// 1. number of pods that are ready should equal that of spec
// 2. number of pods matching replicaSet's selector should equal that of spec
//    (in 0 replicas case, replicas should finish terminating before this comes true)
func isReplicaSetSynchronized(replicaSet *appsv1.ReplicaSet, podList *corev1.PodList) bool {
	return replicaSet.Status.ReadyReplicas == (*replicaSet.Spec.Replicas) && int32(len(podList.Items)) == (*replicaSet.Spec.Replicas)
}

func isReplicaSetGone(cs *kubernetes.Clientset, rsName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		replicaSet, err := cs.AppsV1().ReplicaSets(namespace).Get(context.Background(), rsName, metav1.GetOptions{})
		if err != nil && k8serrors.IsNotFound(err) {
			return true, nil
		} else if err != nil {
			return false, fmt.Errorf("something weird happened with the replicaset, which is in state: [%s]. Errors: %w", replicaSet.Status.Conditions, err)
		}

		return false, nil
	}
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
