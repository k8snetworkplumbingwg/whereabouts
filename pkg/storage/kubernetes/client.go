package kubernetes

import (
	"context"
	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Client has info on how to connect to the kubernetes cluster
type Client struct {
	client    client.Client
	clientSet *kubernetes.Clientset
	retries   int
	timeout   time.Duration
}

func NewClient(timeout time.Duration) (*Client, error) {
	scheme := runtime.NewScheme()
	_ = whereaboutsv1alpha1.AddToScheme(scheme)

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return newClient(config, scheme, timeout)
}

func NewClientViaKubeconfig(kubeconfigPath string, timeout time.Duration) (*Client, error) {
	scheme := runtime.NewScheme()
	_ = whereaboutsv1alpha1.AddToScheme(scheme)

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{}).ClientConfig()

	if err != nil {
		return nil, err
	}

	return newClient(config, scheme, timeout)
}

func newClient(config *rest.Config, schema *runtime.Scheme, timeout time.Duration) (*Client, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	mapper, err := apiutil.NewDiscoveryRESTMapper(config)
	if err != nil {
		return nil, err
	}
	c, err := client.New(config, client.Options{Scheme: schema, Mapper: mapper})
	if err != nil {
		return nil, err
	}

	return newKubernetesClient(c, clientSet, timeout), nil
}

func newKubernetesClient(k8sClient client.Client, k8sClientSet *kubernetes.Clientset, timeout time.Duration) *Client {
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
	ipPoolList := &whereaboutsv1alpha1.IPPoolList{}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	if err := i.client.List(ctxWithTimeout, ipPoolList, &client.ListOptions{}); err != nil {
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
	overlappingIPsList := whereaboutsv1alpha1.OverlappingRangeIPReservationList{}
	if err := i.client.List(ctx, &overlappingIPsList, &client.ListOptions{}); err != nil {
		return nil, err
	}

	return overlappingIPsList.Items, nil
}

func (i *Client) DeleteOverlappingIP(ctx context.Context, clusterWideIP *whereaboutsv1alpha1.OverlappingRangeIPReservation) error {
	return i.client.Delete(ctx, clusterWideIP)
}
