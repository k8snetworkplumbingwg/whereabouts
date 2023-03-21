//go:build test
// +build test

package controlloop

import (
	"context"
	"net"

	kubeClient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

type dummyPodController struct {
	*PodController
	ipPoolCache  cache.Store
	networkCache cache.Store
	podCache     cache.Store
}

func newDummyPodController(
	k8sClient k8sclient.Interface,
	wbClient wbclient.Interface,
	nadClient nadclient.Interface,
	stopChannel chan struct{},
	mountPath string,
	recorder record.EventRecorder) (*dummyPodController, error) {

	const noResyncPeriod = 0
	netAttachDefInformerFactory := nadinformers.NewSharedInformerFactory(nadClient, noResyncPeriod)
	wbInformerFactory := wbinformers.NewSharedInformerFactory(wbClient, noResyncPeriod)
	podInformerFactory, err := PodInformerFactory(k8sClient)
	if err != nil {
		return nil, err
	}

	podController := newPodController(
		k8sClient,
		wbClient,
		podInformerFactory,
		wbInformerFactory,
		netAttachDefInformerFactory,
		nil,
		recorder,
		func(_ context.Context, _ int, ipamConfig types.IPAMConfig, client *kubeClient.KubernetesIPAM) ([]net.IPNet, error) {
			ipPools := castToIPPool(wbInformerFactory.Whereabouts().V1alpha1().IPPools().Informer().GetStore().List())
			for _, pool := range ipPools {
				for index, allocation := range pool.Spec.Allocations {
					if allocation.PodRef == ipamConfig.GetPodRef() {
						delete(pool.Spec.Allocations, index)
						_, err := wbClient.WhereaboutsV1alpha1().IPPools(ipPoolsNamespace()).Update(context.TODO(), &pool, metav1.UpdateOptions{})
						if err != nil {
							return []net.IPNet{}, err // no need to bother computing the allocated range
						}
					}
				}
			}

			return []net.IPNet{}, nil
		})

	alwaysReady := func() bool { return true }
	podController.arePodsSynched = alwaysReady
	podController.areIPPoolsSynched = alwaysReady
	podController.areNetAttachDefsSynched = alwaysReady

	podInformerFactory.Start(stopChannel)
	netAttachDefInformerFactory.Start(stopChannel)
	wbInformerFactory.Start(stopChannel)

	podController.mountPath = mountPath

	controller := &dummyPodController{
		PodController: podController,
		ipPoolCache:   podController.ipPoolInformer.GetStore(),
		networkCache:  podController.netAttachDefInformer.GetStore(),
		podCache:      podController.podsInformer.GetStore(),
	}

	if err := controller.initControllerCaches(k8sClient, wbClient, nadClient); err != nil {
		return nil, err
	}
	go podController.Start(stopChannel)

	return controller, nil
}

func (dpc *dummyPodController) initControllerCaches(k8sClient k8sclient.Interface, wbClient wbclient.Interface, nadClient nadclient.Interface) error {
	if err := dpc.synchIPPool(wbClient); err != nil {
		return err
	}
	if err := dpc.synchPods(k8sClient); err != nil {
		return err
	}
	if err := dpc.synchNetworkAttachments(nadClient); err != nil {
		return err
	}
	return nil
}

func (dpc *dummyPodController) synchIPPool(ipPoolClient wbclient.Interface) error {
	ipPools, err := ipPoolClient.WhereaboutsV1alpha1().IPPools(ipPoolsNamespace()).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, pool := range ipPools.Items {
		if err := dpc.ipPoolCache.Add(&pool); err != nil {
			return err
		}
	}
	return nil
}

func (dpc *dummyPodController) synchNetworkAttachments(netAttachDefClient nadclient.Interface) error {
	const allNamespaces = ""

	networkAttachments, err := netAttachDefClient.K8sCniCncfIoV1().NetworkAttachmentDefinitions(allNamespaces).List(
		context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, network := range networkAttachments.Items {
		if err := dpc.networkCache.Add(&network); err != nil {
			return err
		}
	}
	return nil
}

func (dpc *dummyPodController) synchPods(k8sClient k8sclient.Interface) error {
	const allNamespaces = ""

	pods, err := k8sClient.CoreV1().Pods(allNamespaces).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		if err := dpc.podCache.Add(&pod); err != nil {
			return err
		}
	}
	return nil
}

func castToIPPool(pools []interface{}) []v1alpha1.IPPool {
	var ipPools []v1alpha1.IPPool
	for _, pool := range pools {
		castPool, isPool := pool.(*v1alpha1.IPPool)
		if !isPool {
			continue
		}
		ipPools = append(ipPools, *castPool)
	}
	return ipPools
}
