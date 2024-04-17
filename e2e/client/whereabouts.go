package client

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/typed/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/e2e/entities"
	whereaboutscnicncfiov1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
)

const (
	createTimeout          = 10 * time.Second
	deleteTimeout          = 2 * createTimeout
	rsCreateTimeout        = 600 * time.Second
	nodeSliceCreateTimeout = 5 * time.Second
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

func (c *ClientInfo) GetNodeSlicePool(name string, namespace string) (*whereaboutscnicncfiov1alpha1.NodeSlicePool, error) {
	err := WaitForNodeSliceReady(context.TODO(), c, namespace, name, nodeSliceCreateTimeout)
	if err != nil {
		return nil, err
	}
	nodeslice, err := c.WbClient.WhereaboutsV1alpha1().NodeSlicePools(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return nodeslice, nil
}

func (c *ClientInfo) AddNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) (*nettypes.NetworkAttachmentDefinition, error) {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Create(context.TODO(), netattach, metav1.CreateOptions{})
}

func (c *ClientInfo) DelNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) error {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Delete(context.TODO(), netattach.Name, metav1.DeleteOptions{})
}

func (c *ClientInfo) NodeSliceDeleted(name, namespace string) error {
	_, err := c.WbClient.WhereaboutsV1alpha1().NodeSlicePools(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err == nil || !errors.IsNotFound(err) {
		return fmt.Errorf("expected not found nodeslice")
	}
	return nil
}

func (c *ClientInfo) ProvisionPod(podName string, namespace string, label, annotations map[string]string) (*corev1.Pod, error) {
	ctx := context.Background()
	pod := entities.PodObject(podName, namespace, label, annotations)
	pod, err := c.Client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	const podCreateTimeout = 10 * time.Second
	if err := WaitForPodReady(ctx, c.Client, pod.Namespace, pod.Name, podCreateTimeout); err != nil {
		return nil, err
	}

	pod, err = c.Client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

func (c *ClientInfo) DeletePod(pod *corev1.Pod) error {
	ctx := context.Background()
	if err := c.Client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	const podDeleteTimeout = 20 * time.Second
	if err := WaitForPodToDisappear(ctx, c.Client, pod.GetNamespace(), pod.GetName(), podDeleteTimeout); err != nil {
		return err
	}
	return nil
}

func (c *ClientInfo) ProvisionReplicaSet(rsName string, namespace string, replicaCount int32, labels, annotations map[string]string) (*appsv1.ReplicaSet, error) {
	ctx := context.Background()
	replicaSet, err := c.Client.AppsV1().ReplicaSets(namespace).Create(
		ctx,
		entities.ReplicaSetObject(replicaCount, rsName, namespace, labels, annotations),
		metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	const rsCreateTimeout = 600 * time.Second
	if err := WaitForPodBySelector(ctx, c.Client, namespace, entities.ReplicaSetQuery(rsName), rsCreateTimeout); err != nil {
		return nil, err
	}

	replicaSet, err = c.Client.AppsV1().ReplicaSets(namespace).Get(ctx, replicaSet.Name, metav1.GetOptions{})
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
	ctx := context.Background()
	const rsDeleteTimeout = 2 * rsCreateTimeout
	if err := c.Client.AppsV1().ReplicaSets(replicaSet.GetNamespace()).Delete(ctx, replicaSet.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	if err := WaitForReplicaSetToDisappear(ctx, c.Client, replicaSet.GetNamespace(), replicaSet.GetName(), rsDeleteTimeout); err != nil {
		return err
	}
	return nil
}

func (c *ClientInfo) ProvisionStatefulSet(statefulSetName string, namespace string, serviceName string, replicas int, networkNames ...string) (*appsv1.StatefulSet, error) {
	const statefulSetCreateTimeout = 60 * createTimeout
	ctx := context.Background()
	statefulSet, err := c.Client.AppsV1().StatefulSets(namespace).Create(
		ctx,
		entities.StatefulSetSpec(statefulSetName, namespace, serviceName, replicas, entities.PodNetworkSelectionElements(networkNames...)),
		metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := WaitForStatefulSetCondition(
		ctx,
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
	ctx := context.Background()

	if err := c.Client.AppsV1().StatefulSets(namespace).Delete(
		ctx, serviceName, deleteRightNowAndBlockUntilAssociatedPodsAreGone()); err != nil {
		return err
	}

	return WaitForStatefulSetGone(ctx, c.Client, namespace, serviceName, labelSelector, statefulSetDeleteTimeout)
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
