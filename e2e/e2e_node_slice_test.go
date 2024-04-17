package whereabouts_e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	wbtestclient "github.com/k8snetworkplumbingwg/whereabouts/e2e/client"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/entities"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/poolconsistency"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/retrievers"
	testenv "github.com/k8snetworkplumbingwg/whereabouts/e2e/testenvironment"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/util"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	wbstorage "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

func TestWhereaboutsE2ENodeSlice(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "whereabouts-e2e-node-slice")
}

var _ = Describe("Whereabouts node slice functionality", func() {
	Context("Test setup", func() {
		const (
			testNamespace   = "default"
			ipv4TestRange   = "10.0.0.0/8"
			sliceSize       = "/20" // tests will depend on subnets being > node count of test environment
			testNetworkName = "wa-nad"
			subnets         = 4096
			rsName          = "whereabouts-scale-test"
		)

		var (
			clientInfo   *wbtestclient.ClientInfo
			testConfig   *testenv.Configuration
			netAttachDef *nettypes.NetworkAttachmentDefinition
			replicaSet   *v1.ReplicaSet
			pod          *core.Pod
		)

		BeforeEach(func() {
			var (
				config *rest.Config
				err    error
			)

			testConfig, err = testenv.NewConfig()
			Expect(err).NotTo(HaveOccurred())

			config, err = util.ClusterConfig()
			Expect(err).NotTo(HaveOccurred())

			clientInfo, err = wbtestclient.NewClientInfo(config)
			Expect(err).NotTo(HaveOccurred())

			netAttachDef = util.MacvlanNetworkWithNodeSlice(testNetworkName, testNamespace, ipv4TestRange, testNetworkName, sliceSize)

			By("creating a NetworkAttachmentDefinition for whereabouts")
			_, err = clientInfo.AddNetAttachDef(netAttachDef)
			Expect(err).NotTo(HaveOccurred())

			By("checking node slices have been allocated and nodes are assigned")
			Expect(util.ValidateNodeSlicePoolSlicesCreatedAndNodesAssigned(testNetworkName, testNamespace, subnets, clientInfo)).To(Succeed())
		})

		AfterEach(func() {
			Expect(clientInfo.DelNetAttachDef(netAttachDef)).To(Succeed())
			time.Sleep(1 * time.Second)
			Expect(clientInfo.NodeSliceDeleted(testNetworkName, testNamespace)).To(Succeed())
		})

		Context("Single pod tests node slice", func() {
			BeforeEach(func() {
				const singlePodName = "whereabouts-basic-test"
				var err error

				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("deleting pod with whereabouts net-attach-def")
				Expect(clientInfo.DeletePod(pod)).To(Succeed())
			})

			It("allocates a single pod within the correct IP range", func() {
				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs).NotTo(BeEmpty())
				Expect(util.InNodeRange(clientInfo, pod.Spec.NodeName, testNetworkName, testNamespace, secondaryIfaceIPs[0])).To(Succeed())
			})
		})

		Context("Replicaset tests node slice", func() {
			const (
				emptyReplicaSet = 0
				rsSteadyTimeout = 1200 * time.Second
			)

			var k8sIPAM *wbstorage.KubernetesIPAM

			BeforeEach(func() {
				By("creating a replicaset with whereabouts net-attach-def")
				var err error

				const ipPoolNamespace = "kube-system"
				k8sIPAM, err = wbstorage.NewKubernetesIPAMWithNamespace("", "", types.IPAMConfig{
					Kubernetes: types.KubernetesConfig{
						KubeConfigPath: testConfig.KubeconfigPath,
					},
					NodeSliceSize: sliceSize,
					NetworkName:   testNetworkName,
					Namespace:     testNamespace,
				}, ipPoolNamespace)
				Expect(err).NotTo(HaveOccurred())

				replicaSet, err = clientInfo.ProvisionReplicaSet(
					rsName,
					testNamespace,
					emptyReplicaSet,
					util.PodTierLabel(rsName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("removing replicas and expecting 0 IP pool allocations")
				Expect(
					util.CheckZeroIPPoolAllocationsAndReplicas(
						context.TODO(), clientInfo, k8sIPAM, rsName, testNamespace, ipv4TestRange, testNetworkName)).To(Succeed())

				By("deleting replicaset with whereabouts net-attach-def")
				Expect(clientInfo.DeleteReplicaSet(replicaSet)).To(Succeed())
			})

			It("allocates each IP pool entry with a unique pod IP", func() {
				By("creating max number of pods and checking IP Pool validity")
				for i := 0; i < testConfig.NumberOfIterations; i++ {
					Expect(
						util.CheckZeroIPPoolAllocationsAndReplicas(
							context.TODO(), clientInfo, k8sIPAM, rsName, testNamespace, ipv4TestRange, testNetworkName)).To(Succeed())

					allPods, err := clientInfo.Client.CoreV1().Pods(core.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())

					replicaSet, err = clientInfo.UpdateReplicaSet(
						entities.ReplicaSetObject(
							testConfig.MaxReplicas(allPods.Items),
							rsName,
							testNamespace,
							util.PodTierLabel(rsName),
							entities.PodNetworkSelectionElements(testNetworkName),
						))
					Expect(err).NotTo(HaveOccurred())
					Expect(
						wbtestclient.WaitForReplicaSetSteadyState(
							context.TODO(),
							clientInfo.Client,
							testNamespace,
							entities.ReplicaSetQuery(rsName),
							replicaSet,
							rsSteadyTimeout)).To(Succeed())

					podList, err := wbtestclient.ListPods(context.TODO(), clientInfo.Client, testNamespace, entities.ReplicaSetQuery(rsName))
					Expect(err).NotTo(HaveOccurred())
					Expect(podList.Items).NotTo(BeEmpty())
					nodes, err := clientInfo.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(nodes.Items).NotTo(BeEmpty())
					ipPools := []storage.IPPool{}
					for _, node := range nodes.Items {
						nodeSliceRange, err := wbstorage.GetNodeSlicePoolRange(context.TODO(), k8sIPAM, node.Name)
						Expect(err).NotTo(HaveOccurred())
						ipPool, err := k8sIPAM.GetIPPool(context.Background(), wbstorage.PoolIdentifier{IpRange: nodeSliceRange, NetworkName: testNetworkName, NodeName: node.Name})
						if err == nil {
							ipPools = append(ipPools, ipPool)
						}
					}
					Expect(poolconsistency.NewNodeSliceConsistencyCheck(ipPools, podList.Items).MissingIPs()).To(BeEmpty())
					Expect(poolconsistency.NewNodeSliceConsistencyCheck(ipPools, podList.Items).StaleIPs()).To(BeEmpty())
				}
			})
		})

		Context("stateful set tests", func() {
			const (
				initialReplicaNumber = 20
				ipPoolNamespace      = "kube-system"
				namespace            = "default"
				serviceName          = "web"
				selector             = "app=" + serviceName
				statefulSetName      = "statefulthingy"
			)

			podList := func(podList *core.PodList) []core.Pod { return podList.Items }
			var k8sIPAM *wbstorage.KubernetesIPAM

			Context("regular sized network", func() {
				BeforeEach(func() {
					var err error
					_, err = clientInfo.ProvisionStatefulSet(statefulSetName, namespace, serviceName, initialReplicaNumber, testNetworkName)
					Expect(err).NotTo(HaveOccurred())
					Expect(
						clientInfo.Client.CoreV1().Pods(namespace).List(
							context.TODO(), metav1.ListOptions{LabelSelector: selector})).To(
						WithTransform(podList, HaveLen(initialReplicaNumber)))

					const ipPoolNamespace = "kube-system"
					k8sIPAM, err = wbstorage.NewKubernetesIPAMWithNamespace("", "", types.IPAMConfig{
						Kubernetes: types.KubernetesConfig{
							KubeConfigPath: testConfig.KubeconfigPath,
						},
						NodeSliceSize: sliceSize,
						NetworkName:   testNetworkName,
						Namespace:     testNamespace,
					}, ipPoolNamespace)
					Expect(err).NotTo(HaveOccurred())
				})

				AfterEach(func() {
					Expect(clientInfo.DeleteStatefulSet(namespace, serviceName, selector)).To(Succeed())
					Expect(
						clientInfo.Client.CoreV1().Pods(namespace).List(
							context.TODO(), metav1.ListOptions{LabelSelector: selector})).To(
						WithTransform(podList, BeEmpty()),
						"cannot have leaked pods in the system")

					poolAllocations := func(ipPool *v1alpha1.IPPool) map[string]v1alpha1.IPAllocation {
						return ipPool.Spec.Allocations
					}
					nodes, err := clientInfo.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(nodes.Items).NotTo(BeEmpty())
					for _, node := range nodes.Items {
						Expect(
							clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
								context.TODO(),
								wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: ipv4TestRange, NetworkName: testNetworkName, NodeName: node.Name}),
								metav1.GetOptions{})).To(
							WithTransform(poolAllocations, BeEmpty()),
							"cannot have leaked IPAllocations in the system")
					}
				})

				It("IPPools feature allocations", func() {
					nodes, err := clientInfo.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(nodes.Items).NotTo(BeEmpty())
					ipPools := []storage.IPPool{}
					podList, err := clientInfo.Client.CoreV1().Pods(testNamespace).List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(podList.Items).NotTo(BeEmpty())
					for _, node := range nodes.Items {
						nodeSliceRange, err := wbstorage.GetNodeSlicePoolRange(context.TODO(), k8sIPAM, node.Name)
						Expect(err).NotTo(HaveOccurred())
						ipPool, err := k8sIPAM.GetIPPool(context.Background(), wbstorage.PoolIdentifier{IpRange: nodeSliceRange, NetworkName: testNetworkName, NodeName: node.Name})
						if err == nil {
							ipPools = append(ipPools, ipPool)
						}
					}
					Expect(poolconsistency.NewNodeSliceConsistencyCheck(ipPools, podList.Items).MissingIPs()).To(BeEmpty())
					totalAllocations := 0
					for _, node := range nodes.Items {
						nodeSliceRange, err := wbstorage.GetNodeSlicePoolRange(context.TODO(), k8sIPAM, node.Name)
						Expect(err).NotTo(HaveOccurred())
						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.TODO(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: nodeSliceRange, NetworkName: testNetworkName, NodeName: node.Name}),
							metav1.GetOptions{})
						// error is okay because pod may not land on every node
						if err == nil {
							totalAllocations = totalAllocations + len(ipPool.Spec.Allocations)
						}
					}

					Expect(totalAllocations).To(Equal(initialReplicaNumber))
				})

				table.DescribeTable("stateful sets scale up / down", func(testSetup func(int), instanceDelta int) {
					const scaleTimeout = util.CreatePodTimeout * 6

					testSetup(instanceDelta)

					Eventually(func() (int, error) {
						totalAllocations := 0
						nodes, err := clientInfo.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
						Expect(err).NotTo(HaveOccurred())
						Expect(nodes.Items).NotTo(BeEmpty())
						for _, node := range nodes.Items {
							nodeSliceRange, err := wbstorage.GetNodeSlicePoolRange(context.TODO(), k8sIPAM, node.Name)
							Expect(err).NotTo(HaveOccurred())
							ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.TODO(),
								wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: nodeSliceRange, NetworkName: testNetworkName, NodeName: node.Name}),
								metav1.GetOptions{})
							// error is okay because pod may not land on every node
							if err == nil {
								totalAllocations = totalAllocations + len(ipPool.Spec.Allocations)
							}
						}

						return totalAllocations, nil
					}, scaleTimeout).Should(
						Equal(initialReplicaNumber), "we should have one allocation for each live pod")
				},
					table.Entry("scale up then down 5 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
					}, 5),
					table.Entry("scale up then down 10 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
					}, 10),
					table.Entry("scale up then down 20 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
					}, 20),
					table.Entry("scale down then up 5 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
					}, 5),
					table.Entry("scale down then up 10 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
					}, 10),
					table.Entry("scale down then up 20 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
					}, 20),
				)
			})
		})
	})
})
