package controlloop

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sclient "k8s.io/client-go/kubernetes"
	fakek8sclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	nad "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	fakenadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/fake"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	fakewbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned/fake"
)

func TestIPControlLoop(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconcile IP address allocation in the system")
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
			eventRecorder    *record.FakeRecorder
		)

		BeforeEach(func() {
			stopChannel = make(chan struct{})
			dummyNetworkPool = ipPool(dummyNetIPRange, ipPoolsNamespace(), podReference(pod))
			wbClient = fakewbclient.NewSimpleClientset(dummyNetworkPool)

			const maxEvents = 10
			eventRecorder = record.NewFakeRecorder(maxEvents)
			Expect(newDummyPodController(k8sClient, wbClient, netAttachDefClient, stopChannel, cniConfigDir, eventRecorder)).NotTo(BeNil())

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

			It("registers an event over the event recorder", func() {
				Eventually(<-eventRecorder.Events).Should(Equal("Normal IPAddressGarbageCollected successful cleanup of IP address [192.168.2.0] from network meganet"))
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
