package kubernetes

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
)

// Client has info on how to connect to the kubernetes cluster
type Client struct {
	client    wbclient.Interface
	clientSet kubernetes.Interface
	retries   int
	timeout   time.Duration
}

func NewClient(timeout time.Duration) (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return newClient(config, timeout)
}

func NewClientViaKubeconfig(kubeconfigPath string, timeout time.Duration) (*Client, error) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{}).ClientConfig()

	if err != nil {
		return nil, err
	}

	return newClient(config, timeout)
}

func newClient(config *rest.Config, timeout time.Duration) (*Client, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	c, err := wbclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return NewKubernetesClient(c, clientSet, timeout), nil
}

func NewKubernetesClient(k8sClient wbclient.Interface, k8sClientSet kubernetes.Interface, timeout time.Duration) *Client {
	if timeout == time.Duration(0) {
		timeout = storage.RequestTimeout
	}
	return &Client{
		client:    k8sClient,
		clientSet: k8sClientSet,
		retries:   storage.DatastoreRetries,
		timeout:   timeout,
	}
}

func (i *Client) ListIPPools(ctx context.Context) ([]storage.IPPool, error) {
	logging.Debugf("listing IP pools")

	ctxWithTimeout, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	ipPoolList, err := i.client.WhereaboutsV1alpha1().IPPools("").List(ctxWithTimeout, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var whereaboutsApiIPPoolList []storage.IPPool
	for idx, pool := range ipPoolList.Items {
		firstIP, _, err := pool.ParseCIDR()
		if err != nil {
			return nil, err
		}
		whereaboutsApiIPPoolList = append(
			whereaboutsApiIPPoolList,
			&KubernetesIPPool{client: i.client, firstIP: firstIP, pool: &ipPoolList.Items[idx]})
	}
	return whereaboutsApiIPPoolList, nil
}

func (i *Client) ListPods(ctx context.Context) ([]v1.Pod, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	podList, err := i.clientSet.CoreV1().Pods(metav1.NamespaceAll).List(ctxWithTimeout, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return podList.Items, nil
}

func (i *Client) GetPod(namespace, name string) (*v1.Pod, error) {
	pod, err := i.clientSet.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

func (i *Client) ListOverlappingIPs(ctx context.Context) ([]whereaboutsv1alpha1.OverlappingRangeIPReservation, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, storage.RequestTimeout)
	defer cancel()

	listOptions := metav1.ListOptions{
		Limit: 50,
	}
	var overlappingIPsList []whereaboutsv1alpha1.OverlappingRangeIPReservation
	for {
		overlappingIPs, err := i.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations("").List(ctxWithTimeout, listOptions)
		if err != nil {
			return nil, err
		}
		overlappingIPsList = append(overlappingIPsList, overlappingIPs.Items...)
		if overlappingIPs.Continue == "" {
			break
		}
		listOptions.Continue = overlappingIPs.Continue
	}
	return overlappingIPsList, nil
}

func (i *Client) DeleteOverlappingIP(ctx context.Context, clusterWideIP *whereaboutsv1alpha1.OverlappingRangeIPReservation) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, storage.RequestTimeout)
	defer cancel()

	return i.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(clusterWideIP.GetNamespace()).Delete(
		ctxWithTimeout, clusterWideIP.GetName(), metav1.DeleteOptions{})
}
