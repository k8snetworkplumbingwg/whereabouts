package controlloop

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	var cniConfigDir string

	BeforeEach(func() {
		const configFilePermissions = 0755

		var err error
		cniConfigDir, err = os.MkdirTemp("", "multus-config")
		Expect(err).ToNot(HaveOccurred())
		Expect(os.MkdirAll(path.Join(cniConfigDir, path.Dir(whereaboutsConfigPath)), configFilePermissions)).To(Succeed())
		Expect(os.WriteFile(
			path.Join(cniConfigDir, whereaboutsConfigPath),
			[]byte(dummyWhereaboutsConfig()), configFilePermissions)).To(Succeed())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(cniConfigDir)).To(Succeed())
	})

	Context("a running pod on a node", func() {
		const (
			networkName = "meganet"
			podName     = "tiny-winy-pod"
			nodeName    = "hypernode"
		)

		var (
			k8sClient          k8sclient.Interface
			pod                *v1.Pod
			node               *v1.Node
			dummyPodController *dummyPodController
			podControllerError error
		)

		BeforeEach(func() {
			pod = podSpec(podName, namespace, nodeName, networkName)
			node = nodeSpec(nodeName)
			k8sClient = fakek8sclient.NewSimpleClientset(pod, node)
			os.Setenv("NODENAME", nodeName)
		})

		When("NODENAME is set to an invalid value", func() {
			var (
				wbClient           wbclient.Interface
				eventRecorder      *record.FakeRecorder
				netAttachDefClient nadclient.Interface
				stopChannel        chan struct{}
			)

			BeforeEach(func() {
				os.Setenv("NODENAME", "invalid-node-name")

				stopChannel = make(chan struct{})
				wbClient = fakewbclient.NewSimpleClientset()
				netAttachDefClient, podControllerError = newFakeNetAttachDefClient(namespace, netAttachDef(networkName, namespace, dummyNonWhereaboutsIPAMNetSpec(networkName)))
				Expect(podControllerError).NotTo(HaveOccurred())

				const maxEvents = 1
				eventRecorder = record.NewFakeRecorder(maxEvents)
			})

			It("should fail", func() {
				_, podControllerError = newDummyPodController(k8sClient, wbClient, netAttachDefClient, stopChannel, cniConfigDir, eventRecorder)
				Expect(errors.IsNotFound(podControllerError)).Should(BeTrue())
			})

			AfterEach(func() {
				if podControllerError != nil {
					return
				}
				stopChannel <- struct{}{}
			})
		})

		Context("IPPool featuring an allocation for the pod", func() {
			var (
				dummyNetworkPool *v1alpha1.IPPool
				wbClient         wbclient.Interface
			)

			BeforeEach(func() {
				dummyNetworkPool = ipPool(dummyNetIPRange, ipPoolsNamespace(), podReference(pod))
				wbClient = fakewbclient.NewSimpleClientset(dummyNetworkPool)
			})

			Context("the network attachment is available", func() {
				var (
					eventRecorder      *record.FakeRecorder
					netAttachDefClient nadclient.Interface
					stopChannel        chan struct{}
				)

				BeforeEach(func() {
					var err error
					netAttachDefClient, err = newFakeNetAttachDefClient(namespace, netAttachDef(networkName, namespace, dummyNetSpec(networkName, dummyNetIPRange)))
					Expect(err).NotTo(HaveOccurred())

					const maxEvents = 10
					stopChannel = make(chan struct{})
					eventRecorder = record.NewFakeRecorder(maxEvents)

					dummyPodController, podControllerError = newDummyPodController(k8sClient, wbClient, netAttachDefClient, stopChannel, cniConfigDir, eventRecorder)
					Expect(podControllerError).NotTo(HaveOccurred())
					Expect(dummyPodController).NotTo(BeNil())

					// assure the pool features an allocated address
					ipPool, err := wbClient.WhereaboutsV1alpha1().IPPools(dummyNetworkPool.GetNamespace()).Get(context.TODO(), dummyNetworkPool.GetName(), metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(ipPool.Spec.Allocations).NotTo(BeEmpty())
				})

				AfterEach(func() {
					if podControllerError != nil {
						return
					}
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

					It("registers a SUCCESSFUL IP CLEANUP event over the event recorder", func() {
						Eventually(<-eventRecorder.Events).Should(Equal("Normal IPAddressGarbageCollected successful cleanup of IP address [192.168.2.0] from network meganet"))
					})
				})
			})

			Context("the network attachment was deleted", func() {
				var (
					eventRecorder      *record.FakeRecorder
					netAttachDefClient nadclient.Interface
					stopChannel        chan struct{}
				)

				BeforeEach(func() {
					stopChannel = make(chan struct{})

					var err error
					netAttachDefClient, err = newFakeNetAttachDefClient(namespace)
					Expect(err).NotTo(HaveOccurred())

					const maxEvents = 10
					eventRecorder = record.NewFakeRecorder(maxEvents)
					dummyPodController, podControllerError = newDummyPodController(k8sClient, wbClient, netAttachDefClient, stopChannel, cniConfigDir, eventRecorder)
					Expect(podControllerError).NotTo(HaveOccurred())
					Expect(dummyPodController).NotTo(BeNil())

					// assure the pool features an allocated address
					ipPool, err := wbClient.WhereaboutsV1alpha1().IPPools(dummyNetworkPool.GetNamespace()).Get(context.TODO(), dummyNetworkPool.GetName(), metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(ipPool.Spec.Allocations).NotTo(BeEmpty())
				})

				When("the associated pod is deleted", func() {
					BeforeEach(func() {
						Expect(k8sClient.CoreV1().Pods(namespace).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{})).To(Succeed())
					})

					It("stale IP addresses are left in the IPPool", func() {
						Eventually(func() (map[string]v1alpha1.IPAllocation, error) {
							ipPool, err := wbClient.WhereaboutsV1alpha1().IPPools(dummyNetworkPool.GetNamespace()).Get(
								context.TODO(), dummyNetworkPool.GetName(), metav1.GetOptions{})
							return ipPool.Spec.Allocations, err
						}).Should(
							ContainElements(v1alpha1.IPAllocation{PodRef: podID(pod.GetNamespace(), pod.GetName())}),
							"the ip control loop cannot garbage collect the stale address without accessing the attachment configuration")
					})

					It("registers a DROP FROM QUEUE event over the event recorder", func() {
						expectedEventString := fmt.Sprintf(
							"Warning IPAddressGarbageCollectionFailed failed to garbage collect addresses for pod %s",
							podID(pod.GetNamespace(), pod.GetName()))
						Eventually(<-eventRecorder.Events).Should(Equal(expectedEventString))
					})
				})
			})
		})

		Context("with secondary networks whose type is *not* whereabouts", func() {
			var (
				wbClient           wbclient.Interface
				eventRecorder      *record.FakeRecorder
				netAttachDefClient nadclient.Interface
				stopChannel        chan struct{}
			)

			BeforeEach(func() {
				stopChannel = make(chan struct{})

				wbClient = fakewbclient.NewSimpleClientset()
				var err error
				netAttachDefClient, err = newFakeNetAttachDefClient(namespace, netAttachDef(networkName, namespace, dummyNonWhereaboutsIPAMNetSpec(networkName)))
				Expect(err).NotTo(HaveOccurred())

				const maxEvents = 1
				eventRecorder = record.NewFakeRecorder(maxEvents)

				dummyPodController, podControllerError = newDummyPodController(k8sClient, wbClient, netAttachDefClient, stopChannel, cniConfigDir, eventRecorder)
				Expect(podControllerError).NotTo(HaveOccurred())
				Expect(dummyPodController).NotTo(BeNil())
			})

			AfterEach(func() {
				if podControllerError != nil {
					return
				}
				stopChannel <- struct{}{}
			})

			When("the pod is deleted", func() {
				BeforeEach(func() {
					Expect(k8sClient.CoreV1().Pods(namespace).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{})).To(Succeed())
				})

				It("should not report any event", func() {
					const eventTimeout = time.Second
					Consistently(eventRecorder.Events, eventTimeout).ShouldNot(Receive())
				})
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
