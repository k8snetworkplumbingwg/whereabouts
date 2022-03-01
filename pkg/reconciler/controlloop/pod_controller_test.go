package controlloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	v1coreinformerfactory "k8s.io/client-go/informers"
	k8sclient "k8s.io/client-go/kubernetes"
	fakek8sclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	nad "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	fakenadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/fake"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	fakewbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned/fake"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

func TestIPControlLoop(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconcile IP address allocation in the system")
}

type dummyPodController struct {
	*PodController
	ipPoolCache  cache.Store
	networkCache cache.Store
	podCache     cache.Store
}

func (dpc *dummyPodController) withSynchedIPPool(ipPoolClient wbclient.Interface) error {
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

func (dpc *dummyPodController) withSynchedNetworkAttachments(netAttachDefClient nadclient.Interface) error {
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

func (dpc *dummyPodController) withSynchedPods(k8sClient k8sclient.Interface) error {
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

var _ = Describe("IPControlLoop", func() {
	const (
		dummyNetIPRange = "192.168.2.0/24"
		namespace       = "default"
	)

	var (
		k8sClient          k8sclient.Interface
		cniConfigDir       string
		netAttachDefClient nadclient.Interface
		pod                *v1.Pod
		wbClient           wbclient.Interface
	)

	BeforeEach(func() {
		const configFilePermissions = 0755

		var err error
		cniConfigDir, err = ioutil.TempDir("", "multus-config")
		Expect(err).ToNot(HaveOccurred())
		Expect(os.MkdirAll(path.Join(cniConfigDir, path.Dir(whereaboutsConfigPath)), configFilePermissions)).To(Succeed())
		Expect(ioutil.WriteFile(
			path.Join(cniConfigDir, whereaboutsConfigPath),
			[]byte(dummyWhereaboutsConfig()), configFilePermissions)).To(Succeed())
	})

	BeforeEach(func() {
		const (
			networkName = "meganet"
			podName     = "tiny-winy-pod"
		)

		pod = podSpec(podName, namespace, networkName)
		k8sClient = fakek8sclient.NewSimpleClientset(pod)

		var err error
		netAttachDefClient, err = newFakeNetAttachDefClient(namespace, netAttachDef(networkName, namespace, dummyNetSpec(networkName, dummyNetIPRange)))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(cniConfigDir)).To(Succeed())
	})

	Context("IPPool featuring an allocation for the pod", func() {
		var (
			dummyNetworkPool *v1alpha1.IPPool
			stopChannel      chan struct{}
		)

		BeforeEach(func() {
			stopChannel = make(chan struct{})
			dummyNetworkPool = ipPool(dummyNetIPRange, podReference(pod))
			wbClient = fakewbclient.NewSimpleClientset(dummyNetworkPool)

			Expect(newDummyPodController(k8sClient, wbClient, netAttachDefClient, stopChannel, cniConfigDir)).NotTo(BeNil())

			// assure the pool features an allocated address
			ipPool, err := wbClient.WhereaboutsV1alpha1().IPPools(dummyNetworkPool.GetNamespace()).Get(context.TODO(), dummyNetworkPool.GetName(), metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ipPool.Spec.Allocations).NotTo(BeEmpty())
		})

		AfterEach(func() {
			stopChannel <- struct{}{}
		})

		When("the associated pod is deleted", func() {
			BeforeEach(func() {
				Expect(k8sClient.CoreV1().Pods(namespace).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{})).To(Succeed())
			})

			It("stale IP addresses are garbage collected", func() {
				Eventually(func() (map[string]v1alpha1.IPAllocation, error) {
					ipPool, err := wbClient.WhereaboutsV1alpha1().IPPools(dummyNetworkPool.GetNamespace()).Get(
						context.TODO(), dummyNetworkPool.GetName(), metav1.GetOptions{})
					return ipPool.Spec.Allocations, err
				}).Should(BeEmpty(), "the ip control loop should have removed this stale address")
			})
		})
	})
})

func newFakeNetAttachDefClient(namespace string, networkAttachments ...nad.NetworkAttachmentDefinition) (nadclient.Interface, error) {
	netAttachDefClient := fakenadclient.NewSimpleClientset()
	gvr := metav1.GroupVersionResource{
		Group:    "k8s.cni.cncf.io",
		Version:  "v1",
		Resource: "network-attachment-definitions",
	}

	for _, networkAttachment := range networkAttachments {
		if err := netAttachDefClient.Tracker().Create(schema.GroupVersionResource(gvr), &networkAttachment, namespace); err != nil {
			return nil, err
		}
	}
	return netAttachDefClient, nil
}

func dummyNetSpec(networkName string, ipRange string) string {
	return fmt.Sprintf(`{
      "cniVersion": "0.3.0",
      "name": "%s",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "%s"
      }
    }`, networkName, ipRange)
}

func podSpec(name string, namespace string, networks ...string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: podNetworkSelectionElements(networks...),
		},
	}
}

func netAttachDef(netName string, namespace string, config string) nad.NetworkAttachmentDefinition {
	return nad.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      netName,
			Namespace: namespace,
		},
		Spec: nad.NetworkAttachmentDefinitionSpec{
			Config: config,
		},
	}
}

func podNetworkSelectionElements(networkNames ...string) map[string]string {
	return map[string]string{
		nad.NetworkAttachmentAnnot: strings.Join(networkNames, ","),
		nad.NetworkStatusAnnot:     podNetworkStatusAnnotations("default", networkNames...),
	}
}

func podNetworkStatusAnnotations(namespace string, networkNames ...string) string {
	var netStatus []nad.NetworkStatus
	for i, networkName := range networkNames {
		netStatus = append(
			netStatus,
			nad.NetworkStatus{
				Name:      fmt.Sprintf("%s/%s", namespace, networkName),
				Interface: fmt.Sprintf("net%d", i),
			})
	}
	serelizedNetStatus, err := json.Marshal(netStatus)
	if err != nil {
		return ""
	}
	return string(serelizedNetStatus)
}

func ipPool(ipRange string, podReferences ...string) *v1alpha1.IPPool {
	return &v1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubernetes.NormalizeRange(ipRange),
			Namespace: ipPoolsNamespace(),
		},
		Spec: v1alpha1.IPPoolSpec{
			Range:       ipRange,
			Allocations: allocations(podReferences...),
		},
	}
}

func allocations(podReferences ...string) map[string]v1alpha1.IPAllocation {
	poolAllocations := map[string]v1alpha1.IPAllocation{}
	for i, podRef := range podReferences {
		poolAllocations[fmt.Sprintf("%d", i)] = v1alpha1.IPAllocation{
			ContainerID: "",
			PodRef:      podRef,
		}
	}
	return poolAllocations
}

func newDummyPodController(
	k8sClient k8sclient.Interface,
	wbClient wbclient.Interface,
	nadClient nadclient.Interface,
	stopChannel chan struct{},
	mountPath string) (*dummyPodController, error) {

	const noResyncPeriod = 0
	netAttachDefInformerFactory := nadinformers.NewSharedInformerFactory(nadClient, noResyncPeriod)
	wbInformerFactory := wbinformers.NewSharedInformerFactory(wbClient, noResyncPeriod)
	podInformerFactory := v1coreinformerfactory.NewSharedInformerFactory(k8sClient, noResyncPeriod)

	podController := newPodController(
		podInformerFactory,
		wbInformerFactory,
		netAttachDefInformerFactory,
		nil,
		nil,
		func(_ context.Context, _ int, _ types.IPAMConfig, _ string, podRef string) (net.IPNet, error) {
			ipPools := castToIPPool(wbInformerFactory.Whereabouts().V1alpha1().IPPools().Informer().GetStore().List())
			for _, pool := range ipPools {
				for index, allocation := range pool.Spec.Allocations {
					if allocation.PodRef == podRef {
						delete(pool.Spec.Allocations, index)
						_, err := wbClient.WhereaboutsV1alpha1().IPPools(ipPoolsNamespace()).Update(context.TODO(), &pool, metav1.UpdateOptions{})
						if err != nil {
							return net.IPNet{}, err // no need to bother computing the allocated range
						}
					}
				}
			}

			return net.IPNet{}, nil
		})

	alwaysReady := func() bool { return true }
	podController.arePodsSynched = alwaysReady
	podController.areIPPoolsSynched = alwaysReady
	podController.areNetAttachDefsSynched = alwaysReady

	podController.handler.mountPath = mountPath

	controller := &dummyPodController{
		PodController: podController,
		ipPoolCache:   podController.ipPoolInformer.GetStore(),
		networkCache:  podController.netAttachDefInformer.GetStore(),
		podCache:      podController.podsInformer.GetStore(),
	}

	netAttachDefInformerFactory.Start(stopChannel)
	wbInformerFactory.Start(stopChannel)
	podInformerFactory.Start(stopChannel)

	if err := controller.withSynchedIPPool(wbClient); err != nil {
		return nil, err
	}
	if err := controller.withSynchedPods(k8sClient); err != nil {
		return nil, err
	}
	if err := controller.withSynchedNetworkAttachments(nadClient); err != nil {
		return nil, err
	}

	go podController.Start(stopChannel)

	return controller, nil
}

func podReference(pod *v1.Pod) string {
	return fmt.Sprintf("%s/%s", pod.GetNamespace(), pod.GetName())
}

func dummyWhereaboutsConfig() string {
	return `{
      "datastore": "kubernetes",
      "kubernetes": {
        "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
      },
      "log_file": "/tmp/whereabouts.log",
      "log_level": "verbose"
    }
`
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
