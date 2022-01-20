package kubernetes

import (
	"context"
	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
}

func NewClient() (*Client, error) {
	scheme := runtime.NewScheme()
	_ = whereaboutsv1alpha1.AddToScheme(scheme)

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return newClient(config, scheme)
}

func NewClientViaKubeconfig(kubeconfigPath string) (*Client, error) {
	scheme := runtime.NewScheme()
	_ = whereaboutsv1alpha1.AddToScheme(scheme)

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{}).ClientConfig()

	if err != nil {
		return nil, err
	}

	return newClient(config, scheme)
}

func newClient(config *rest.Config, schema *runtime.Scheme) (*Client, error) {
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

	return newKubernetesClient(c, clientSet), nil
}

func newKubernetesClient(k8sClient client.Client, k8sClientSet *kubernetes.Clientset) *Client {
	return &Client{
		client:    k8sClient,
		clientSet: k8sClientSet,
		retries:   storage.DatastoreRetries,
	}
}

func (i *Client) ListIPPools(ctx context.Context) ([]storage.IPPool, error) {
	logging.Debugf("listing IP pools")
	ipPoolList := &whereaboutsv1alpha1.IPPoolList{}

	if err := i.client.List(ctx, ipPoolList, &client.ListOptions{}); err != nil {
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

func (i *Client) ListPods() ([]v1.Pod, error) {
	podList, err := i.clientSet.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var podEntries []v1.Pod
	for _, pod := range podList.Items {
		podEntries = append(podEntries, pod)
	}
	return podEntries, nil
}

func (i *Client) ListOverlappingIPs(ctx context.Context) ([]whereaboutsv1alpha1.OverlappingRangeIPReservation, error) {
	overlappingIPsList := whereaboutsv1alpha1.OverlappingRangeIPReservationList{}
	if err := i.client.List(ctx, &overlappingIPsList, &client.ListOptions{}); err != nil {
		return nil, err
	}

	var clusterWiderReservations []whereaboutsv1alpha1.OverlappingRangeIPReservation
	for _, reservationInfo := range overlappingIPsList.Items {
		clusterWiderReservations = append(clusterWiderReservations, reservationInfo)
	}
	return clusterWiderReservations, nil
}

func (i *Client) DeleteOverlappingIP(ctx context.Context, clusterWideIP *whereaboutsv1alpha1.OverlappingRangeIPReservation) error {
	return i.client.Delete(ctx, clusterWideIP)
}
