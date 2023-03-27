package whereabouts_e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	wbtestclient "github.com/k8snetworkplumbingwg/whereabouts/e2e/client"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/entities"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/poolconsistency"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/retrievers"
	testenv "github.com/k8snetworkplumbingwg/whereabouts/e2e/testenvironment"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbstorage "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const (
	createPodTimeout = 10 * time.Second
)

func TestWhereaboutsE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "whereabouts-e2e")
}

var _ = Describe("Whereabouts functionality", func() {
	Context("Test setup", func() {
		const (
			testNamespace   = "default"
			ipv4TestRange   = "10.10.0.0/16"
			testNetworkName = "wa-nad"
			rsName          = "whereabouts-scale-test"
			ipPoolCIDR      = "10.10.0.0/16"
		)

		var (
			clientInfo   *wbtestclient.ClientInfo
			testConfig   *testenv.Configuration
			netAttachDef *nettypes.NetworkAttachmentDefinition
			pod          *core.Pod
			replicaSet   *v1.ReplicaSet
		)

		BeforeEach(func() {
			var (
				config *rest.Config
				err    error
			)

			testConfig, err = testenv.NewConfig()
			Expect(err).NotTo(HaveOccurred())

			config, err = clusterConfig()
			Expect(err).NotTo(HaveOccurred())

			clientInfo, err = wbtestclient.NewClientInfo(config)
			Expect(err).NotTo(HaveOccurred())

			netAttachDef = macvlanNetworkWithWhereaboutsIPAMNetwork(testNetworkName, testNamespace, ipv4TestRange, []string{}, wbstorage.UnnamedNetwork, true)

			By("creating a NetworkAttachmentDefinition for whereabouts")
			_, err = clientInfo.AddNetAttachDef(netAttachDef)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(clientInfo.DelNetAttachDef(netAttachDef)).To(Succeed())
		})

		Context("Single pod tests", func() {
			BeforeEach(func() {
				const singlePodName = "whereabouts-basic-test"
				var err error

				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					podTierLabel(singlePodName),
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
				secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod)
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs).NotTo(BeEmpty())
				Expect(inRange(ipv4TestRange, secondaryIfaceIPs[0])).To(Succeed())
			})
		})

		Context("DualStack", func() {
			const (
				testDualStackNetworkName = "wa-dualstack-nad"
				dualStackIPv4Range       = "11.11.0.0/16"
				dualStackIPv6Range       = "abcd::0/64"
			)

			var (
				netAttachDefDualStack *nettypes.NetworkAttachmentDefinition
				testIPRangesDualStack = []string{dualStackIPv4Range, dualStackIPv6Range}
			)

			Context("IPRanges configuration only", func() {
				BeforeEach(func() {
					const dualstackPodName = "whereabouts-dualstack-test"
					var err error

					netAttachDefDualStack = macvlanNetworkWithWhereaboutsIPAMNetwork(
						testDualStackNetworkName,
						testNamespace,
						"",
						testIPRangesDualStack, wbstorage.UnnamedNetwork, true)

					By("creating DualStack NetworkAttachmentDefinition for whereabouts")
					_, err = clientInfo.AddNetAttachDef(netAttachDefDualStack)
					Expect(err).NotTo(HaveOccurred())

					By("creating a pod with whereabouts net-attach-def")
					pod, err = clientInfo.ProvisionPod(
						dualstackPodName,
						testNamespace,
						podTierLabel(dualstackPodName),
						entities.PodNetworkSelectionElements(testDualStackNetworkName),
					)
					Expect(err).NotTo(HaveOccurred())
				})

				AfterEach(func() {
					By("deleting pod with whereabouts net-attach-def")
					Expect(clientInfo.DeletePod(pod)).To(Succeed())
					By("deleting DualStack NetworkAttachmentDefinition for whereabouts")
					Expect(clientInfo.DelNetAttachDef(netAttachDefDualStack)).To(Succeed())
				})

				It("allocates a single pod within the correct IP ranges", func() {
					By("checking pod IP is within whereabouts IPAM ranges")
					secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod)
					Expect(err).NotTo(HaveOccurred())
					Expect(secondaryIfaceIPs).To(HaveLen(2))
					Expect(inRange(dualStackIPv4Range, secondaryIfaceIPs[0])).To(Succeed())
					Expect(inRange(dualStackIPv6Range, secondaryIfaceIPs[1])).To(Succeed())
				})
			})

			Context("IPRanges along with old range", func() {
				BeforeEach(func() {
					const dualstackPodName = "whereabouts-dualstack-test"
					var err error

					netAttachDefDualStack = macvlanNetworkWithWhereaboutsIPAMNetwork(
						testDualStackNetworkName,
						testNamespace,
						ipv4TestRange,
						testIPRangesDualStack, wbstorage.UnnamedNetwork, true)

					By("creating DualStack NetworkAttachmentDefinition for whereabouts")
					_, err = clientInfo.AddNetAttachDef(netAttachDefDualStack)
					Expect(err).NotTo(HaveOccurred())

					By("creating a pod with whereabouts net-attach-def")
					pod, err = clientInfo.ProvisionPod(
						dualstackPodName,
						testNamespace,
						podTierLabel(dualstackPodName),
						entities.PodNetworkSelectionElements(testDualStackNetworkName),
					)
					Expect(err).NotTo(HaveOccurred())
				})

				AfterEach(func() {
					By("deleting pod with whereabouts net-attach-def")
					Expect(clientInfo.DeletePod(pod)).To(Succeed())
					By("deleting DualStack NetworkAttachmentDefinition for whereabouts")
					Expect(clientInfo.DelNetAttachDef(netAttachDefDualStack)).To(Succeed())
				})

				It("allocates a single pod within the correct IP ranges", func() {
					By("checking pod IP is within whereabouts IPAM ranges")
					secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod)
					Expect(err).NotTo(HaveOccurred())
					Expect(secondaryIfaceIPs).To(HaveLen(3))
					Expect(inRange(ipv4TestRange, secondaryIfaceIPs[0])).To(Succeed())
					Expect(inRange(dualStackIPv4Range, secondaryIfaceIPs[1])).To(Succeed())
					Expect(inRange(dualStackIPv6Range, secondaryIfaceIPs[2])).To(Succeed())
				})
			})
		})

		Context("Replicaset tests", func() {
			const (
				emptyReplicaSet = 0
				rsSteadyTimeout = 1200 * time.Second
			)

			var k8sIPAM *wbstorage.KubernetesIPAM

			BeforeEach(func() {
				By("creating a replicaset with whereabouts net-attach-def")
				var err error

				const ipPoolNamespace = "kube-system"
				k8sIPAM, err = wbstorage.NewKubernetesIPAMWithNamespace("", types.IPAMConfig{
					Kubernetes: types.KubernetesConfig{
						KubeConfigPath: testConfig.KubeconfigPath,
					},
				}, ipPoolNamespace)
				Expect(err).NotTo(HaveOccurred())

				replicaSet, err = clientInfo.ProvisionReplicaSet(
					rsName,
					testNamespace,
					emptyReplicaSet,
					podTierLabel(rsName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("removing replicas and expecting 0 IP pool allocations")
				Expect(
					checkZeroIPPoolAllocationsAndReplicas(
						clientInfo, k8sIPAM, rsName, testNamespace, ipPoolCIDR, testNetworkName)).To(Succeed())

				By("deleting replicaset with whereabouts net-attach-def")
				Expect(clientInfo.DeleteReplicaSet(replicaSet)).To(Succeed())
			})

			It("allocates each IP pool entry with a unique pod IP", func() {
				By("creating max number of pods and checking IP Pool validity")
				for i := 0; i < testConfig.NumberOfIterations; i++ {
					Expect(
						checkZeroIPPoolAllocationsAndReplicas(
							clientInfo, k8sIPAM, rsName, testNamespace, ipPoolCIDR, testNetworkName)).To(Succeed())

					allPods, err := clientInfo.Client.CoreV1().Pods(core.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())

					replicaSet, err = clientInfo.UpdateReplicaSet(
						entities.ReplicaSetObject(
							testConfig.MaxReplicas(allPods.Items),
							rsName,
							testNamespace,
							podTierLabel(rsName),
							entities.PodNetworkSelectionElements(testNetworkName),
						))
					Expect(err).NotTo(HaveOccurred())
					Expect(
						wbtestclient.WaitForReplicaSetSteadyState(
							clientInfo.Client,
							testNamespace,
							entities.ReplicaSetQuery(rsName),
							replicaSet,
							rsSteadyTimeout)).To(Succeed())

					podList, err := wbtestclient.ListPods(clientInfo.Client, testNamespace, entities.ReplicaSetQuery(rsName))
					Expect(err).NotTo(HaveOccurred())
					Expect(podList.Items).NotTo(BeEmpty())

					ipPool, err := k8sIPAM.GetIPPool(context.Background(), wbstorage.PoolIdentifier{IpRange: ipPoolCIDR, NetworkName: wbstorage.UnnamedNetwork})
					Expect(err).NotTo(HaveOccurred())
					Expect(poolconsistency.NewPoolConsistencyCheck(ipPool, podList.Items).MissingIPs()).To(BeEmpty())
					Expect(poolconsistency.NewPoolConsistencyCheck(ipPool, podList.Items).StaleIPs()).To(BeEmpty())
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

			Context("regular sized network", func() {
				BeforeEach(func() {
					var err error
					_, err = clientInfo.ProvisionStatefulSet(statefulSetName, namespace, serviceName, initialReplicaNumber, testNetworkName)
					Expect(err).NotTo(HaveOccurred())
					Expect(
						clientInfo.Client.CoreV1().Pods(namespace).List(
							context.TODO(), metav1.ListOptions{LabelSelector: selector})).To(
						WithTransform(podList, HaveLen(initialReplicaNumber)))
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
					Expect(
						clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.TODO(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})).To(
						WithTransform(poolAllocations, BeEmpty()),
						"cannot have leaked IPAllocations in the system")
				})

				It("IPPools feature allocations", func() {
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.TODO(),
						wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
						metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(ipPool.Spec.Allocations).To(HaveLen(initialReplicaNumber))
				})

				table.DescribeTable("stateful sets scale up / down", func(testSetup func(int), instanceDelta int) {
					const scaleTimeout = createPodTimeout * 6

					testSetup(instanceDelta)

					Eventually(func() (map[string]v1alpha1.IPAllocation, error) {
						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.TODO(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})
						if err != nil {
							return map[string]v1alpha1.IPAllocation{}, err
						}

						return ipPool.Spec.Allocations, nil
					}, scaleTimeout).Should(
						HaveLen(initialReplicaNumber), "we should have one allocation for each live pod")
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

			Context("network with very few IPs", func() {
				const (
					namespace                = "default"
					networkName              = "meganet2000"
					rangeWithTwoIPs          = "10.10.0.0/30"
					replicaNumber            = 2
					statefulSetCreateTimeout = 20 * time.Second
				)

				var tinyNetwork *nettypes.NetworkAttachmentDefinition

				BeforeEach(func() {
					var err error
					tinyNetwork, err = clientInfo.AddNetAttachDef(
						macvlanNetworkWithWhereaboutsIPAMNetwork(networkName, namespace, rangeWithTwoIPs, []string{}, wbstorage.UnnamedNetwork, true))
					Expect(err).NotTo(HaveOccurred())

					_, err = clientInfo.ProvisionStatefulSet(statefulSetName, namespace, serviceName, replicaNumber, networkName)
					Expect(err).NotTo(HaveOccurred())
				})

				AfterEach(func() {
					Expect(clientInfo.DelNetAttachDef(tinyNetwork)).To(Succeed())
					Expect(clientInfo.DeleteStatefulSet(namespace, serviceName, selector)).To(Succeed())
				})

				It("IPPool is exhausted", func() {
					const scaleUpReplicas = 1
					Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, scaleUpReplicas)).To(Succeed())
					Expect(
						wbtestclient.WaitForStatefulSetCondition(
							clientInfo.Client,
							namespace,
							serviceName,
							replicaNumber+scaleUpReplicas,
							statefulSetCreateTimeout,
							wbtestclient.IsStatefulSetReadyPredicate)).To(HaveOccurred(), "the IPPool is already at its limits")
				})

				Context("deleting a pod from the statefulset", func() {
					var (
						containerID string
						podRef      string
					)

					BeforeEach(func() {
						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.TODO(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						Expect(ipPool.Spec.Allocations).NotTo(BeEmpty())

						containerID = ipPool.Spec.Allocations["1"].ContainerID
						podRef = ipPool.Spec.Allocations["1"].PodRef

						decomposedPodRef := strings.Split(podRef, "/")
						Expect(decomposedPodRef).To(HaveLen(2))
						podName := decomposedPodRef[1]

						rightNow := int64(0)
						Expect(clientInfo.Client.CoreV1().Pods(namespace).Delete(
							context.TODO(), podName, metav1.DeleteOptions{GracePeriodSeconds: &rightNow})).To(Succeed())

						Expect(wbtestclient.WaitForStatefulSetCondition(
							clientInfo.Client,
							namespace,
							serviceName,
							replicaNumber,
							time.Second,
							wbtestclient.IsStatefulSetDegradedPredicate)).Should(Succeed())

						scaleUpTimeout := 2 * createPodTimeout
						Expect(wbtestclient.WaitForStatefulSetCondition(
							clientInfo.Client,
							namespace,
							serviceName,
							replicaNumber,
							scaleUpTimeout,
							wbtestclient.IsStatefulSetReadyPredicate)).Should(Succeed())
					})

					It("can recover from an exhausted IP pool", func() {
						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.TODO(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IpRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						Expect(ipPool.Spec.Allocations).NotTo(BeEmpty())

						Expect(allocationForPodRef(podRef, *ipPool).ContainerID).NotTo(Equal(containerID))
					})
				})
			})
		})

		Context("Named ranges test", func() {
			const (
				testNetwork2Name = "wa-nad-2"
			)
			var (
				netAttachDef2 *nettypes.NetworkAttachmentDefinition
				pod2          *core.Pod
			)

			BeforeEach(func() {
				var (
					err error
				)

				netAttachDef2 = macvlanNetworkWithWhereaboutsIPAMNetwork(testNetwork2Name, testNamespace, ipv4TestRange, []string{}, testNetwork2Name, false)

				By("creating a second NetworkAttachmentDefinition for whereabouts")
				_, err = clientInfo.AddNetAttachDef(netAttachDef2)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				Expect(clientInfo.DelNetAttachDef(netAttachDef2)).To(Succeed())
			})

			BeforeEach(func() {
				const (
					singlePodName  = "whereabouts-basic-test"
					singlePod2Name = "whereabouts-basic-test-2"
				)
				var err error

				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					podTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating a second pod with the second whereabouts net-attach-def")
				pod2, err = clientInfo.ProvisionPod(
					singlePod2Name,
					testNamespace,
					podTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetwork2Name),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("deleting pod with whereabouts net-attach-def")
				Expect(clientInfo.DeletePod(pod)).To(Succeed())
				By("deleting the second pod with whereabouts net-attach-def")
				Expect(clientInfo.DeletePod(pod2)).To(Succeed())
			})

			It("allocates the same IP to the Pods as they are in differenct address collision domains", func() {
				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod)
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs).NotTo(BeEmpty())

				By("checking pod 2 IP is within whereabouts IPAM range")
				secondaryIfaceIPs2, err := retrievers.SecondaryIfaceIPValue(pod2)
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs2).NotTo(BeEmpty())

				Expect(secondaryIfaceIPs[0]).To(Equal(secondaryIfaceIPs2[0]))
			})
		})
	})
})

func allocationForPodRef(podRef string, ipPool v1alpha1.IPPool) *v1alpha1.IPAllocation {
	for _, allocation := range ipPool.Spec.Allocations {
		if allocation.PodRef == podRef {
			return &allocation
		}
	}
	return nil
}

func clusterConfig() (*rest.Config, error) {
	const kubeconfig = "KUBECONFIG"

	kubeconfigPath, found := os.LookupEnv(kubeconfig)
	if !found {
		return nil, fmt.Errorf("must provide the path to the kubeconfig via the `KUBECONFIG` env variable")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func podTierLabel(podTier string) map[string]string {
	const tier = "tier"
	return map[string]string{tier: podTier}
}

// Waits for all replicas to be fully removed from replicaset, and checks that there are 0 ip pool allocations
func checkZeroIPPoolAllocationsAndReplicas(clientInfo *wbtestclient.ClientInfo, k8sIPAM *wbstorage.KubernetesIPAM, rsName, namespace string, ipPoolCIDR string, networkNames ...string) error {
	const (
		emptyReplicaSet   = 0
		rsSteadyTimeout   = 1200 * time.Second
		zeroIPPoolTimeout = 2 * time.Minute
	)
	var err error

	replicaSet, err := clientInfo.UpdateReplicaSet(
		entities.ReplicaSetObject(
			emptyReplicaSet,
			rsName,
			namespace,
			podTierLabel(rsName),
			entities.PodNetworkSelectionElements(networkNames...),
		))
	if err != nil {
		return err
	}

	matchingLabel := entities.ReplicaSetQuery(rsName)
	if err = wbtestclient.WaitForReplicaSetSteadyState(clientInfo.Client, namespace, matchingLabel, replicaSet, rsSteadyTimeout); err != nil {
		return err
	}

	if err = wbtestclient.WaitForZeroIPPoolAllocations(k8sIPAM, ipPoolCIDR, zeroIPPoolTimeout); err != nil {
		return err
	}

	return nil
}

// Returns a network attachment definition object configured by provided parameters
func generateNetAttachDefSpec(name, namespace, config string) *nettypes.NetworkAttachmentDefinition {
	return &nettypes.NetworkAttachmentDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "NetworkAttachmentDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: nettypes.NetworkAttachmentDefinitionSpec{
			Config: config,
		},
	}
}

func macvlanNetworkWithWhereaboutsIPAMNetwork(networkName string, namespaceName string, ipRange string, ipRanges []string, poolName string, enableOverlappingRanges bool) *nettypes.NetworkAttachmentDefinition {
	macvlanConfig := fmt.Sprintf(`{
        "cniVersion": "0.3.0",
        "disableCheck": true,
        "plugins": [
            {
                "type": "macvlan",
                "master": "eth0",
                "mode": "bridge",
                "ipam": {
                    "type": "whereabouts",
                    "leader_lease_duration": 1500,
                    "leader_renew_deadline": 1000,
                    "leader_retry_period": 500,
                    "range": "%s",
                    "ipRanges": %s,
                    "log_level": "debug",
                    "log_file": "/tmp/wb",
                    "network_name": "%s",
                    "enable_overlapping_ranges": %v
                }
            }
        ]
    }`, ipRange, createIPRanges(ipRanges), poolName, enableOverlappingRanges)
	return generateNetAttachDefSpec(networkName, namespaceName, macvlanConfig)
}

func inRange(cidr string, ip string) error {
	_, cidrRange, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}

	if cidrRange.Contains(net.ParseIP(ip)) {
		return nil
	}

	return fmt.Errorf("ip [%s] is NOT in range %s", ip, cidr)
}

func createIPRanges(ranges []string) string {
	formattedRanges := []string{}
	for _, ipRange := range ranges {
		singleRange := fmt.Sprintf(`{"range": "%s"}`, ipRange)
		formattedRanges = append(formattedRanges, singleRange)
	}
	ipRanges := "[" + strings.Join(formattedRanges[:], ",") + "]"
	return ipRanges
}
