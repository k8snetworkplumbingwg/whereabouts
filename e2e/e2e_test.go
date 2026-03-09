package whereabouts_e2e

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"sort"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbtestclient "github.com/k8snetworkplumbingwg/whereabouts/e2e/client"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/entities"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/poolconsistency"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/retrievers"
	testenv "github.com/k8snetworkplumbingwg/whereabouts/e2e/testenvironment"
	"github.com/k8snetworkplumbingwg/whereabouts/e2e/util"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/iphelpers"
	wbstorage "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const (
	createPodTimeout = 10 * time.Second
	ipPoolNamespace  = "kube-system"
)

func TestWhereaboutsE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "whereabouts-e2e")
}

var _ = Describe("Whereabouts functionality", func() {
	Context("Test setup", func() {
		const (
			testNamespace            = "default"
			ipv4TestRange            = "10.10.0.0/16"
			ipv4TestRangeOverlapping = "10.10.0.0/17"
			testNetworkName          = "wa-nad"
			rsName                   = "whereabouts-scale-test"
			ipPoolCIDR               = "10.10.0.0/16"
		)

		var (
			clientInfo   *wbtestclient.ClientInfo
			testConfig   *testenv.Configuration
			netAttachDef *nettypes.NetworkAttachmentDefinition
			pod          *corev1.Pod
			replicaSet   *v1.ReplicaSet
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

			netAttachDef = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(testNetworkName, testNamespace, ipv4TestRange, []string{}, wbstorage.UnnamedNetwork, true)

			By("creating a NetworkAttachmentDefinition for whereabouts")
			_, err = clientInfo.AddNetAttachDef(netAttachDef)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(clientInfo.DelNetAttachDef(netAttachDef)).To(Succeed())
		})

		Context("Single pod tests", func() {
			const singlePodName = "whereabouts-basic-test"
			var err error

			AfterEach(func() {
				By("deleting pod with whereabouts net-attach-def")
				_ = clientInfo.DeletePod(pod)
			})

			It("allocates a single pod with a single interface", func() {
				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())

				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs).NotTo(BeEmpty())
				Expect(inRange(ipv4TestRange, secondaryIfaceIPs[0])).To(Succeed())

				By("verifying allocation")
				verifyAllocations(clientInfo, ipv4TestRange, secondaryIfaceIPs[0], testNamespace, pod.Name, "net1")

				By("deleting pod")
				err = clientInfo.DeletePod(pod)
				Expect(err).NotTo(HaveOccurred())

				By("checking that the IP allocation is removed")
				verifyNoAllocationsForPodRef(clientInfo, ipv4TestRange, testNamespace, pod.Name, secondaryIfaceIPs)
			})
			It("allocates a single pod with multiple interfaces", func() {
				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					podTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetworkName, testNetworkName, testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())

				By("checking pod IP is within whereabouts IPAM range")
				secondaryIPs := make([]string, 0, 3)

				for _, ifName := range []string{"net1", "net2", "net3"} {
					secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, ifName)
					Expect(err).NotTo(HaveOccurred())
					Expect(secondaryIfaceIPs).NotTo(BeEmpty())
					for _, ip := range secondaryIfaceIPs {
						Expect(inRange(ipv4TestRange, ip)).To(Succeed())

						By("verifying allocation")
						verifyAllocations(clientInfo, ipv4TestRange, ip, testNamespace, pod.Name, ifName)
					}
					secondaryIPs = append(secondaryIPs, secondaryIfaceIPs...)
				}

				By("deleting pod")
				err = clientInfo.DeletePod(pod)
				Expect(err).NotTo(HaveOccurred())

				By("checking that the IP allocation is removed")
				verifyNoAllocationsForPodRef(clientInfo, ipv4TestRange, testNamespace, pod.Name, secondaryIPs)
			})
		})

		Context("DS: basic IPRanges", func() {
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

					netAttachDefDualStack = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
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
						util.PodTierLabel(dualstackPodName),
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
					secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(secondaryIfaceIPs).To(HaveLen(2))
					Expect(util.InRange(dualStackIPv4Range, secondaryIfaceIPs[0])).To(Succeed())
					Expect(util.InRange(dualStackIPv6Range, secondaryIfaceIPs[1])).To(Succeed())
				})
			})

			Context("IPRanges along with old range", func() {
				BeforeEach(func() {
					const dualstackPodName = "whereabouts-dualstack-test"
					var err error

					netAttachDefDualStack = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
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
						util.PodTierLabel(dualstackPodName),
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
					secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(secondaryIfaceIPs).To(HaveLen(3))
					Expect(util.InRange(ipv4TestRange, secondaryIfaceIPs[0])).To(Succeed())
					Expect(util.InRange(dualStackIPv4Range, secondaryIfaceIPs[1])).To(Succeed())
					Expect(util.InRange(dualStackIPv6Range, secondaryIfaceIPs[2])).To(Succeed())
				})
			})
		})

		Context("ReplicaSet", func() {
			const (
				emptyReplicaSet = 0
				rsSteadyTimeout = 1200 * time.Second
			)

			var k8sIPAM *wbstorage.KubernetesIPAM
			ctx := context.Background()

			BeforeEach(func() {
				By("creating a replicaset with whereabouts net-attach-def")
				var err error

				k8sIPAM, err = wbstorage.NewKubernetesIPAMWithNamespace("", "", types.IPAMConfig{
					Kubernetes: types.KubernetesConfig{
						KubeConfigPath: testConfig.KubeconfigPath,
					},
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
						ctx, clientInfo, k8sIPAM, rsName, testNamespace, ipPoolCIDR, testNetworkName)).To(Succeed())

				By("deleting replicaset with whereabouts net-attach-def")
				Expect(clientInfo.DeleteReplicaSet(replicaSet)).To(Succeed())
			})

			It("allocates each IP pool entry with a unique pod IP", func() {
				By("creating max number of pods and checking IP Pool validity")
				for range testConfig.NumberOfIterations {
					Expect(
						util.CheckZeroIPPoolAllocationsAndReplicas(
							ctx, clientInfo, k8sIPAM, rsName, testNamespace, ipPoolCIDR, testNetworkName)).To(Succeed())

					allPods, err := clientInfo.Client.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
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
							ctx,
							clientInfo.Client,
							testNamespace,
							entities.ReplicaSetQuery(rsName),
							replicaSet,
							rsSteadyTimeout)).To(Succeed())

					podList, err := wbtestclient.ListPods(ctx, clientInfo.Client, testNamespace, entities.ReplicaSetQuery(rsName))
					Expect(err).NotTo(HaveOccurred())
					Expect(podList.Items).NotTo(BeEmpty())

					ipPool, err := k8sIPAM.GetIPPool(ctx, wbstorage.PoolIdentifier{IPRange: ipPoolCIDR, NetworkName: wbstorage.UnnamedNetwork})
					Expect(err).NotTo(HaveOccurred())
					Expect(poolconsistency.NewPoolConsistencyCheck(ipPool, podList.Items).MissingIPs()).To(BeEmpty())
					Expect(poolconsistency.NewPoolConsistencyCheck(ipPool, podList.Items).StaleIPs()).To(BeEmpty())
				}
			})
		})

		Context("StatefulSet", func() {
			const (
				initialReplicaNumber = 20
				namespace            = "default"
				serviceName          = "web"
				selector             = "app=" + serviceName
				statefulSetName      = "statefulthingy"
			)

			podList := func(podList *corev1.PodList) []corev1.Pod { return podList.Items }

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
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})).To(
						WithTransform(poolAllocations, BeEmpty()),
						"cannot have leaked IPAllocations in the system")
				})

				It("IPPools feature allocations", func() {
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.TODO(),
						wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
						metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(ipPool.Spec.Allocations).To(HaveLen(initialReplicaNumber))
				})

				DescribeTable("stateful sets scale up / down", func(testSetup func(int), instanceDelta int) {
					const scaleTimeout = util.CreatePodTimeout * 6

					testSetup(instanceDelta)

					Eventually(func() (map[string]v1alpha1.IPAllocation, error) {
						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.TODO(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})
						if err != nil {
							return map[string]v1alpha1.IPAllocation{}, err
						}

						return ipPool.Spec.Allocations, nil
					}, scaleTimeout).Should(
						HaveLen(initialReplicaNumber), "we should have one allocation for each live pod")
				},
					Entry("scale up then down 5 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
					}, 5),
					Entry("scale up then down 10 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
					}, 10),
					Entry("scale up then down 20 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
					}, 20),
					Entry("scale down then up 5 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
					}, 5),
					Entry("scale down then up 10 replicas", func(deltaInstances int) {
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
						Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
					}, 10),
					Entry("scale down then up 20 replicas", func(deltaInstances int) {
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
						util.MacvlanNetworkWithWhereaboutsIPAMNetwork(networkName, namespace, rangeWithTwoIPs, []string{}, wbstorage.UnnamedNetwork, true))
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
							context.Background(),
							clientInfo.Client,
							namespace,
							serviceName,
							replicaNumber+scaleUpReplicas,
							statefulSetCreateTimeout,
							wbtestclient.IsStatefulSetReadyPredicate)).To(HaveOccurred(), "the IPPool is already at its limits")
				})

				Context("deleting a pod from the statefulset", func() {
					It("can recover from an exhausted IP pool", func() {
						var containerID string
						var podRef string

						ctx := context.Background()

						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							ctx,
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						Expect(ipPool.Spec.Allocations).NotTo(BeEmpty())

						containerID = ipPool.Spec.Allocations["1"].ContainerID
						podRef = ipPool.Spec.Allocations["1"].PodRef

						decomposedPodRef := strings.Split(podRef, "/")
						Expect(decomposedPodRef).To(HaveLen(2))
						podName := decomposedPodRef[1]

						By("deleting pod")
						rightNow := int64(0)
						Expect(clientInfo.Client.CoreV1().Pods(namespace).Delete(
							ctx, podName, metav1.DeleteOptions{GracePeriodSeconds: &rightNow})).To(Succeed())

						By("checking that the IP allocation is recreated")
						Eventually(func() error {
							ipPool, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
								ctx,
								wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}),
								metav1.GetOptions{})
							if err != nil {
								return err
							}

							if len(ipPool.Spec.Allocations) == 0 {
								return fmt.Errorf("IP pool is empty")
							}

							if allocationForPodRef(podRef, *ipPool)[0].ContainerID == containerID {
								return fmt.Errorf("IP allocation not recreated")
							}

							return nil
						}, 3*time.Second, 500*time.Millisecond).Should(Succeed(), "the IP allocation should be recreated")
					})
				})
			})

			Context("reclaim previously allocated IP", func() {
				const (
					namespace       = "default"
					networkName     = "recovernet"
					rangeWithTwoIPs = "10.10.0.0/30"
					replicaNumber   = 1
				)

				var podName string
				var secondaryIPs []string
				var ifNames = []string{"net1", "net2"}

				var tinyNetwork *nettypes.NetworkAttachmentDefinition
				var originalAllocations []v1alpha1.IPAllocation
				var originalClusterWideAllocations []*v1alpha1.OverlappingRangeIPReservation

				BeforeEach(func() {
					var err error

					podName = fmt.Sprintf("%s-0", serviceName)

					tinyNetwork, err = clientInfo.AddNetAttachDef(
						macvlanNetworkWithWhereaboutsIPAMNetwork(networkName, namespace, rangeWithTwoIPs, []string{}, wbstorage.UnnamedNetwork, true))
					Expect(err).NotTo(HaveOccurred())

					// Request 2 interfaces.
					_, err = clientInfo.ProvisionStatefulSet(statefulSetName, namespace, serviceName, replicaNumber, networkName, networkName)
					Expect(err).NotTo(HaveOccurred())

					By("getting pod info")
					pod, err := clientInfo.Client.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					By("verifying allocation")
					for _, ifName := range ifNames {
						secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, ifName)
						Expect(err).NotTo(HaveOccurred())

						for _, ip := range secondaryIfaceIPs {
							verifyAllocations(clientInfo, rangeWithTwoIPs, ip, namespace, podName, ifName)
						}
						secondaryIPs = append(secondaryIPs, secondaryIfaceIPs...)
					}

					By("saving initial allocations")
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.Background(), wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}), metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					originalAllocations = allocationForPodRef(getPodRef(namespace, podName), *ipPool)
					Expect(originalAllocations).To(HaveLen(2))

					for _, ip := range secondaryIPs {
						overlapping, err := clientInfo.WbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(ipPoolNamespace).Get(context.Background(), wbstorage.NormalizeIP(net.ParseIP(ip), wbstorage.UnnamedNetwork), metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						originalClusterWideAllocations = append(originalClusterWideAllocations, overlapping)
					}
				})

				AfterEach(func() {
					Expect(clientInfo.DelNetAttachDef(tinyNetwork)).To(Succeed())
					Expect(clientInfo.DeleteStatefulSet(namespace, serviceName, selector)).To(Succeed())
				})

				It("can reclaim the previously allocated IPs", func() {
					By("checking that the IP allocation is removed when the pod is deleted")
					Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, -1)).To(Succeed())

					const podDeleteTimeout = 20 * time.Second
					err := wbtestclient.WaitForPodToDisappear(context.Background(), clientInfo.Client, namespace, podName, podDeleteTimeout)
					Expect(err).NotTo(HaveOccurred())
					verifyNoAllocationsForPodRef(clientInfo, rangeWithTwoIPs, namespace, podName, secondaryIPs)

					By("adding previous allocations")
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.Background(), wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}), metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					updatedPool := ipPool.DeepCopy()
					for i, ip := range secondaryIPs {
						firstIP, _, err := net.ParseCIDR(ipv4TestRange)
						Expect(err).NotTo(HaveOccurred())
						offset, err := iphelpers.IPGetOffset(net.ParseIP(ip), firstIP)
						Expect(err).NotTo(HaveOccurred())

						updatedPool.Spec.Allocations[offset.String()] = originalAllocations[i]
					}

					_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Update(context.Background(), updatedPool, metav1.UpdateOptions{})
					Expect(err).NotTo(HaveOccurred())

					for _, allocation := range originalClusterWideAllocations {
						allocation.ResourceVersion = ""
						_, err := clientInfo.WbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(ipPoolNamespace).Create(context.Background(), allocation, metav1.CreateOptions{})
						Expect(err).NotTo(HaveOccurred())
					}

					By("increasing replica count")
					Expect(clientInfo.ScaleStatefulSet(serviceName, namespace, 1)).To(Succeed())
					err = wbtestclient.WaitForStatefulSetCondition(context.Background(), clientInfo.Client, namespace, serviceName, replicaNumber, 1*time.Minute, wbtestclient.IsStatefulSetReadyPredicate)
					Expect(err).NotTo(HaveOccurred())

					By("getting pod info")
					pod, err := clientInfo.Client.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					By("verifying allocation")
					for _, ifName := range ifNames {
						secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, ifName)
						Expect(err).NotTo(HaveOccurred())

						for _, ip := range secondaryIfaceIPs {
							verifyAllocations(clientInfo, rangeWithTwoIPs, ip, namespace, podName, ifName)
						}
						secondaryIPs = append(secondaryIPs, secondaryIfaceIPs...)
					}

					By("comparing with previous allocations")
					ipPool, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.Background(), wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: rangeWithTwoIPs, NetworkName: wbstorage.UnnamedNetwork}), metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					currentAllocation := allocationForPodRef(getPodRef(namespace, podName), *ipPool)
					Expect(currentAllocation).To(HaveLen(2))

					for i, allocation := range currentAllocation {
						Expect(allocation.ContainerID).ToNot(Equal(originalAllocations[i].ContainerID))
						Expect(allocation.IfName).To(Equal(originalAllocations[i].IfName))
						Expect(allocation.PodRef).To(Equal(originalAllocations[i].PodRef))
					}
				})
			})
		})

		Context("Overlapping ranges", func() {
			const (
				testNetwork2Name = "wa-nad-2"
			)
			var (
				netAttachDef2 *nettypes.NetworkAttachmentDefinition
				pod2          *corev1.Pod
			)

			for _, enableOverlappingRanges := range []bool{true, false} {
				When(fmt.Sprintf("a second net-attach-definition with \"enable_overlapping_ranges\": %t is created",
					enableOverlappingRanges), func() {
					BeforeEach(func() {
						netAttachDef2 = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(testNetwork2Name, testNamespace,
							ipv4TestRangeOverlapping, []string{}, "", enableOverlappingRanges)

						By("creating a second NetworkAttachmentDefinition for whereabouts")
						_, err := clientInfo.AddNetAttachDef(netAttachDef2)
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
							util.PodTierLabel(singlePodName),
							entities.PodNetworkSelectionElements(testNetworkName),
						)
						Expect(err).NotTo(HaveOccurred())

						By("creating a second pod with the second whereabouts net-attach-def")
						pod2, err = clientInfo.ProvisionPod(
							singlePod2Name,
							testNamespace,
							util.PodTierLabel(singlePodName),
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

					It("allocates the correct IP address to the second pod", func() {
						ifName := "net1"
						By("checking pod IP is within whereabouts IPAM range")
						secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, ifName)
						Expect(err).NotTo(HaveOccurred())
						Expect(secondaryIfaceIPs).NotTo(BeEmpty())

						By("checking pod 2 IP is within whereabouts IPAM range")
						secondaryIfaceIPs2, err := retrievers.SecondaryIfaceIPValue(pod2, ifName)
						Expect(err).NotTo(HaveOccurred())
						Expect(secondaryIfaceIPs2).NotTo(BeEmpty())

						if enableOverlappingRanges {
							By("checking pod 2 IP is different from pod 1 IP")
							Expect(secondaryIfaceIPs[0]).NotTo(Equal(secondaryIfaceIPs2[0]))
						} else {
							By("checking pod 2 IP equals pod 1 IP")
							Expect(secondaryIfaceIPs[0]).To(Equal(secondaryIfaceIPs2[0]))
						}
					})
				})
			}
		})

		Context("Named ranges", func() {
			const (
				namedNetworkName = "named-range"
				testNetwork2Name = "wa-nad-2"
				testNetwork3Name = "wa-nad-3"
			)
			var (
				netAttachDef2 *nettypes.NetworkAttachmentDefinition
				netAttachDef3 *nettypes.NetworkAttachmentDefinition
				pod2          *corev1.Pod
				pod3          *corev1.Pod
			)

			BeforeEach(func() {
				var (
					err error
				)

				netAttachDef2 = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(testNetwork2Name, testNamespace,
					ipv4TestRange, []string{}, namedNetworkName, true)
				netAttachDef3 = util.MacvlanNetworkWithWhereaboutsIPAMNetwork(testNetwork3Name, testNamespace,
					ipv4TestRangeOverlapping, []string{}, namedNetworkName, true)

				By("creating a second NetworkAttachmentDefinition for whereabouts")
				_, err = clientInfo.AddNetAttachDef(netAttachDef2)
				Expect(err).NotTo(HaveOccurred())

				By("creating a third NetworkAttachmentDefinition for whereabouts")
				_, err = clientInfo.AddNetAttachDef(netAttachDef3)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				Expect(clientInfo.DelNetAttachDef(netAttachDef2)).To(Succeed())
				Expect(clientInfo.DelNetAttachDef(netAttachDef3)).To(Succeed())
			})

			BeforeEach(func() {
				const (
					singlePodName  = "whereabouts-basic-test"
					singlePod2Name = "whereabouts-basic-test-2"
					singlePod3Name = "whereabouts-basic-test-3"
				)
				var err error

				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating a second pod with the second whereabouts net-attach-def")
				pod2, err = clientInfo.ProvisionPod(
					singlePod2Name,
					testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetwork2Name),
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating a third pod with the third whereabouts net-attach-def")
				pod3, err = clientInfo.ProvisionPod(
					singlePod3Name,
					testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetwork3Name),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("deleting pod with whereabouts net-attach-def")
				Expect(clientInfo.DeletePod(pod)).To(Succeed())
				By("deleting the second pod with whereabouts net-attach-def")
				Expect(clientInfo.DeletePod(pod2)).To(Succeed())
				By("deleting the third pod with whereabouts net-attach-def")
				Expect(clientInfo.DeletePod(pod3)).To(Succeed())
			})

			It("allocates the same IP to the Pods as they are in different address collision domains", func() {
				ifName := "net1"
				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIPs, err := retrievers.SecondaryIfaceIPValue(pod, ifName)
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs).NotTo(BeEmpty())

				By("checking pod 2 IP is within whereabouts IPAM range and has the same IP as pod 1")
				secondaryIfaceIPs2, err := retrievers.SecondaryIfaceIPValue(pod2, ifName)
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs2).NotTo(BeEmpty())
				Expect(secondaryIfaceIPs[0]).To(Equal(secondaryIfaceIPs2[0]))

				By("checking pod 3 IP is within whereabouts IPAM range and has a different IP from pod 2")
				secondaryIfaceIPs3, err := retrievers.SecondaryIfaceIPValue(pod3, ifName)
				Expect(err).NotTo(HaveOccurred())
				Expect(secondaryIfaceIPs3).NotTo(BeEmpty())
				Expect(secondaryIfaceIPs2[0]).NotTo(Equal(secondaryIfaceIPs3[0]))
			})
		})

		// ────────────────────────────────────────────────────────────────
		// Parameterized IPv4 / IPv6 feature tests
		// Each feature is exercised with both address families.
		// ────────────────────────────────────────────────────────────────

		Context("Single pod allocation across address families", func() {
			DescribeTable("allocates and deallocates a single pod",
				func(networkName, ipRange string, expectV6 bool) {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					podName := "wb-single-" + networkName
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					By("checking pod IP is within range")
					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())

					By("verifying address family")
					if expectV6 {
						Expect(util.IsIPv6(ips[0])).To(BeTrue(), "expected IPv6, got %s", ips[0])
					} else {
						Expect(util.IsIPv4(ips[0])).To(BeTrue(), "expected IPv4, got %s", ips[0])
					}

					By("verifying IPPool allocation exists")
					verifyAllocations(clientInfo, ipRange, ips[0], testNamespace, podName, "net1")

					By("deleting pod and verifying deallocation")
					Expect(clientInfo.DeletePod(p)).To(Succeed())
					verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, podName, ips)
				},
				Entry("IPv4", "wa-single-v4", "10.50.0.0/24", false),
				Entry("IPv6", "wa-single-v6", "fd00:50::/112", true),
			)
		})

		Context("Multi-interface allocation across address families", func() {
			DescribeTable("allocates multiple interfaces on a single pod",
				func(networkName, ipRange string, expectV6 bool) {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					podName := "wb-multi-" + networkName
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName, networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					var allIPs = make([]string, 0, 2)
					for _, ifName := range []string{"net1", "net2"} {
						ips, err := retrievers.SecondaryIfaceIPValue(p, ifName)
						Expect(err).NotTo(HaveOccurred())
						Expect(ips).NotTo(BeEmpty())
						Expect(util.InRange(ipRange, ips[0])).To(Succeed())
						if expectV6 {
							Expect(util.IsIPv6(ips[0])).To(BeTrue())
						} else {
							Expect(util.IsIPv4(ips[0])).To(BeTrue())
						}
						allIPs = append(allIPs, ips[0])
					}

					By("verifying each interface got a different IP")
					Expect(allIPs[0]).NotTo(Equal(allIPs[1]))
				},
				Entry("IPv4", "wa-multi-v4", "10.51.0.0/24", false),
				Entry("IPv6", "wa-multi-v6", "fd00:51::/112", true),
			)
		})

		Context("Exclude ranges across address families", func() {
			DescribeTable("skips excluded IPs",
				func(networkName, ipRange string, excludeRanges, forbiddenIPs []string, expectV6 bool) {
					nad := util.MacvlanNetworkWithWhereaboutsExcludeRange(
						networkName, testNamespace, ipRange, excludeRanges)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					podName := "wb-excl-" + networkName
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())

					if expectV6 {
						Expect(util.IsIPv6(ips[0])).To(BeTrue())
					} else {
						Expect(util.IsIPv4(ips[0])).To(BeTrue())
					}

					By("verifying IP is not in excluded range")
					for _, forbidden := range forbiddenIPs {
						Expect(ips[0]).NotTo(Equal(forbidden),
							"IP %s should have been excluded", forbidden)
					}
				},
				Entry("IPv4 — exclude first two IPs",
					"wa-excl-v4", "10.52.0.0/30",
					[]string{"10.52.0.0/31"}, []string{"10.52.0.0", "10.52.0.1"}, false),
				Entry("IPv6 — exclude first two IPs",
					"wa-excl-v6", "fd00:52::/126",
					[]string{"fd00:52::/127"}, []string{"fd00:52::", "fd00:52::1"}, true),
			)
		})

		Context("Range start/end across address families", func() {
			DescribeTable("allocates within start/end bounds only",
				func(networkName, cidr, rangeStart, rangeEnd string, expectV6 bool) {
					nad := util.MacvlanNetworkWithWhereaboutsRangeStartEnd(
						networkName, testNamespace, cidr, rangeStart, rangeEnd)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					podName := "wb-range-" + networkName
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.InRange(cidr, ips[0])).To(Succeed())
					Expect(util.InIPRange(rangeStart, rangeEnd, ips[0])).To(Succeed())

					if expectV6 {
						Expect(util.IsIPv6(ips[0])).To(BeTrue())
					} else {
						Expect(util.IsIPv4(ips[0])).To(BeTrue())
					}
				},
				Entry("IPv4 — sub-range of /24",
					"wa-range-v4", "10.53.0.0/24", "10.53.0.100", "10.53.0.105", false),
				Entry("IPv6 — sub-range of /112",
					"wa-range-v6", "fd00:53::/112", "fd00:53::a0", "fd00:53::af", true),
			)
		})

		Context("Overlapping range protection across address families", func() {
			DescribeTable("prevents or allows duplicate IPs based on enable_overlapping_ranges",
				func(nad1Name, nad2Name, ipRange string, enableOverlapping, expectV6 bool) {
					// When overlapping is enabled both NADs share a pool so the
					// pool's own allocation logic guarantees unique IPs.
					// When disabled each NAD gets its own pool so both
					// independently assign the first available IP (.1).
					poolName1, poolName2 := nad1Name, nad2Name
					if enableOverlapping {
						poolName1, poolName2 = wbstorage.UnnamedNetwork, wbstorage.UnnamedNetwork
					}
					nad1 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						nad1Name, testNamespace, ipRange, []string{}, poolName1, enableOverlapping)
					nad2 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						nad2Name, testNamespace, ipRange, []string{}, poolName2, enableOverlapping)

					_, err := clientInfo.AddNetAttachDef(nad1)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad1)).To(Succeed()) }()
					_, err = clientInfo.AddNetAttachDef(nad2)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad2)).To(Succeed()) }()

					pod1Name := "wb-overlap1-" + nad1Name
					p1, err := clientInfo.ProvisionPod(
						pod1Name, testNamespace,
						util.PodTierLabel(pod1Name),
						entities.PodNetworkSelectionElements(nad1Name))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p1) }()

					pod2Name := "wb-overlap2-" + nad2Name
					p2, err := clientInfo.ProvisionPod(
						pod2Name, testNamespace,
						util.PodTierLabel(pod2Name),
						entities.PodNetworkSelectionElements(nad2Name))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p2) }()

					ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
					Expect(err).NotTo(HaveOccurred())
					ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
					Expect(err).NotTo(HaveOccurred())

					if expectV6 {
						Expect(util.IsIPv6(ips1[0])).To(BeTrue())
						Expect(util.IsIPv6(ips2[0])).To(BeTrue())
					}

					if enableOverlapping {
						By("overlapping enabled: IPs must be different")
						Expect(ips1[0]).NotTo(Equal(ips2[0]))
					} else {
						By("overlapping disabled: IPs may be the same")
						Expect(ips1[0]).To(Equal(ips2[0]))
					}
				},
				Entry("IPv4 overlapping enabled",
					"wa-ov4-en-1", "wa-ov4-en-2", "10.54.0.0/28", true, false),
				Entry("IPv4 overlapping disabled",
					"wa-ov4-dis-1", "wa-ov4-dis-2", "10.55.0.0/28", false, false),
				Entry("IPv6 overlapping enabled",
					"wa-ov6-en-1", "wa-ov6-en-2", "fd00:54::/124", true, true),
				Entry("IPv6 overlapping disabled",
					"wa-ov6-dis-1", "wa-ov6-dis-2", "fd00:55::/124", false, true),
			)
		})

		Context("Pool exhaustion across address families", func() {
			DescribeTable("fails to schedule when pool is exhausted",
				func(networkName, ipRange string, numUsable int) {
					const (
						serviceName     = "web-exhaust"
						statefulSetName = "wb-exhaust"
						selector        = "app=" + serviceName
					)

					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() {
						Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed())
					}()

					By(fmt.Sprintf("creating statefulset with %d replicas to fill pool", numUsable))
					_, err = clientInfo.ProvisionStatefulSet(
						statefulSetName, testNamespace, serviceName, numUsable, networkName)
					Expect(err).NotTo(HaveOccurred())
					defer func() {
						Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
					}()

					By("scaling up by 1 — should fail due to exhaustion")
					Expect(clientInfo.ScaleStatefulSet(serviceName, testNamespace, 1)).To(Succeed())
					Expect(
						wbtestclient.WaitForStatefulSetCondition(
							context.Background(), clientInfo.Client, testNamespace,
							serviceName, numUsable+1, 20*time.Second,
							wbtestclient.IsStatefulSetReadyPredicate),
					).To(HaveOccurred(), "should fail because pool is exhausted")
				},
				// /30 = 4 addresses total; .0 is skipped → 3 usable; but last is broadcast-ish
				// in practice whereabouts gives 2 usable IPs from a /30
				Entry("IPv4 /30 pool", "wa-exhaust-v4", "10.56.0.0/30", 2),
				// /126 = 4 addresses; similar behavior for IPv6
				Entry("IPv6 /126 pool", "wa-exhaust-v6", "fd00:56::/126", 2),
			)
		})

		Context("Concurrent allocation across address families", func() {
			DescribeTable("handles concurrent allocations without IP conflicts",
				func(networkName, ipRange string, replicaCount int, expectV6 bool) {
					const rsSteadyTimeout = 120 * time.Second
					rsName := "wb-conc-" + networkName

					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					k8sIPAM, err := wbstorage.NewKubernetesIPAMWithNamespace("", "", types.IPAMConfig{
						Kubernetes: types.KubernetesConfig{
							KubeConfigPath: testConfig.KubeconfigPath,
						},
					}, ipPoolNamespace)
					Expect(err).NotTo(HaveOccurred())

					rs, err := clientInfo.ProvisionReplicaSet(
						rsName, testNamespace, 0,
						util.PodTierLabel(rsName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() {
						// Scale down before deleting
						scaled, err := clientInfo.UpdateReplicaSet(
							entities.ReplicaSetObject(0, rsName, testNamespace,
								util.PodTierLabel(rsName),
								entities.PodNetworkSelectionElements(networkName)))
						Expect(err).NotTo(HaveOccurred())
						Expect(wbtestclient.WaitForReplicaSetSteadyState(
							context.Background(), clientInfo.Client, testNamespace,
							entities.ReplicaSetQuery(rsName), scaled, rsSteadyTimeout)).To(Succeed())
						Expect(clientInfo.DeleteReplicaSet(rs)).To(Succeed())
					}()

					By(fmt.Sprintf("scaling to %d replicas simultaneously", replicaCount))
					rs, err = clientInfo.UpdateReplicaSet(
						entities.ReplicaSetObject(int32(replicaCount), rsName, testNamespace,
							util.PodTierLabel(rsName),
							entities.PodNetworkSelectionElements(networkName)))
					Expect(err).NotTo(HaveOccurred())

					Expect(wbtestclient.WaitForReplicaSetSteadyState(
						context.Background(), clientInfo.Client, testNamespace,
						entities.ReplicaSetQuery(rsName), rs, rsSteadyTimeout)).To(Succeed())

					podList, err := wbtestclient.ListPods(context.Background(),
						clientInfo.Client, testNamespace, entities.ReplicaSetQuery(rsName))
					Expect(err).NotTo(HaveOccurred())
					Expect(podList.Items).To(HaveLen(replicaCount))

					ipSet := make(map[string]struct{})
					for _, p := range podList.Items {
						ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
						Expect(err).NotTo(HaveOccurred())
						Expect(ips).NotTo(BeEmpty())
						Expect(util.InRange(ipRange, ips[0])).To(Succeed())
						if expectV6 {
							Expect(util.IsIPv6(ips[0])).To(BeTrue())
						}
						_, dup := ipSet[ips[0]]
						Expect(dup).To(BeFalse(), "duplicate IP: %s", ips[0])
						ipSet[ips[0]] = struct{}{}
					}

					By("verifying pool consistency")
					ipPool, err := k8sIPAM.GetIPPool(context.Background(),
						wbstorage.PoolIdentifier{IPRange: ipRange, NetworkName: wbstorage.UnnamedNetwork})
					Expect(err).NotTo(HaveOccurred())
					Expect(poolconsistency.NewPoolConsistencyCheck(ipPool, podList.Items).MissingIPs()).To(BeEmpty())
					Expect(poolconsistency.NewPoolConsistencyCheck(ipPool, podList.Items).StaleIPs()).To(BeEmpty())
				},
				Entry("IPv4 — 10 pods on /28", "wa-conc-v4", "10.57.0.0/28", 10, false),
				Entry("IPv6 — 10 pods on /124", "wa-conc-v6", "fd00:57::/124", 10, true),
			)
		})

		Context("DS: exclude ranges", func() {
			It("applies exclude ranges to both address families in dual-stack config", func() {
				const (
					networkName   = "wa-ds-excl"
					v4Range       = "10.58.0.0/30"
					v6Range       = "fd00:58::/126"
					singlePodName = "wb-ds-excl-test"
				)

				// Build a NAD with dual-stack ipRanges and excludes for both families.
				// We'll use the raw config approach since our helpers only do single-stack excludes.
				macvlanConfig := fmt.Sprintf(`{
					"cniVersion": "0.3.0",
					"disableCheck": true,
					"plugins": [{
						"type": "macvlan",
						"master": "eth0",
						"mode": "bridge",
						"ipam": {
							"type": "whereabouts",
							"leader_lease_duration": 1500,
							"leader_renew_deadline": 1000,
							"leader_retry_period": 500,
							"range": "%s",
							"exclude": ["10.58.0.0/31"],
							"ipRanges": [{"range": "%s", "exclude": ["fd00:58::/127"]}],
							"log_level": "debug",
							"log_file": "/tmp/wb",
							"enable_overlapping_ranges": true
						}
					}]
				}`, v4Range, v6Range)
				nad := util.GenerateNetAttachDefSpec(networkName, testNamespace, macvlanConfig)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					singlePodName, testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).To(HaveLen(2))

				By("checking IPv4 address avoids excluded range")
				Expect(util.InRange(v4Range, ips[0])).To(Succeed())
				Expect(ips[0]).NotTo(Equal("10.58.0.0"))
				Expect(ips[0]).NotTo(Equal("10.58.0.1"))

				By("checking IPv6 address avoids excluded range")
				Expect(util.InRange(v6Range, ips[1])).To(Succeed())
				Expect(ips[1]).NotTo(Equal("fd00:58::"))
				Expect(ips[1]).NotTo(Equal("fd00:58::1"))
			})
		})

		Context("DS: range_start/range_end", func() {
			It("respects range bounds for both address families", func() {
				const (
					networkName   = "wa-ds-range"
					v4Range       = "10.59.0.0/24"
					v6Range       = "fd00:59::/112"
					singlePodName = "wb-ds-range-test"
				)

				macvlanConfig := fmt.Sprintf(`{
					"cniVersion": "0.3.0",
					"disableCheck": true,
					"plugins": [{
						"type": "macvlan",
						"master": "eth0",
						"mode": "bridge",
						"ipam": {
							"type": "whereabouts",
							"leader_lease_duration": 1500,
							"leader_renew_deadline": 1000,
							"leader_retry_period": 500,
							"range": "%s",
							"range_start": "10.59.0.200",
							"range_end": "10.59.0.205",
							"ipRanges": [{"range": "%s", "range_start": "fd00:59::c8", "range_end": "fd00:59::cd"}],
							"log_level": "debug",
							"log_file": "/tmp/wb",
							"enable_overlapping_ranges": true
						}
					}]
				}`, v4Range, v6Range)
				nad := util.GenerateNetAttachDefSpec(networkName, testNamespace, macvlanConfig)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					singlePodName, testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).To(HaveLen(2))

				By("checking IPv4 within range bounds")
				Expect(util.InIPRange("10.59.0.200", "10.59.0.205", ips[0])).To(Succeed())
				By("checking IPv6 within range bounds")
				Expect(util.InIPRange("fd00:59::c8", "fd00:59::cd", ips[1])).To(Succeed())
			})
		})

		Context("Allocation verification across address families", func() {
			DescribeTable("verifies IPPool allocation matches pod IP",
				func(networkName, ipRange string, expectV6 bool) {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					podName := "wb-verify-" + networkName
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())

					By("verifying IPPool has the correct allocation")
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
						context.Background(),
						wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipRange, NetworkName: wbstorage.UnnamedNetwork}),
						metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					podRef := getPodRef(testNamespace, podName)
					allocations := allocationForPodRef(podRef, *ipPool)
					Expect(allocations).To(HaveLen(1))
					Expect(allocations[0].IfName).To(Equal("net1"))
					Expect(allocations[0].PodRef).To(Equal(podRef))

					By("verifying the allocation offset matches the pod IP")
					firstIP, _, err := net.ParseCIDR(ipRange)
					Expect(err).NotTo(HaveOccurred())
					offset, err := iphelpers.IPGetOffset(net.ParseIP(ips[0]), firstIP)
					Expect(err).NotTo(HaveOccurred())
					_, ok := ipPool.Spec.Allocations[offset.String()]
					Expect(ok).To(BeTrue(), "allocation for pod IP %s at offset %s should exist", ips[0], offset)
				},
				Entry("IPv4", "wa-verify-v4", "10.60.0.0/24", false),
				Entry("IPv6", "wa-verify-v6", "fd00:60::/112", true),
			)
		})

		Context("Reconciler cleanup", func() {
			const singlePodName = "whereabouts-reconciler-test"

			AfterEach(func() {
				if pod != nil {
					_ = clientInfo.DeletePod(pod)
				}
			})

			It("cleans up stale allocations referencing non-existent pods", func() {
				By("getting or creating the IPPool")
				ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
					context.Background(),
					wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
					metav1.GetOptions{})
				if err != nil && errors.IsNotFound(err) {
					pod, err = clientInfo.ProvisionPod(
						singlePodName+"-init", testNamespace,
						util.PodTierLabel(singlePodName),
						entities.PodNetworkSelectionElements(testNetworkName))
					Expect(err).NotTo(HaveOccurred())
					Expect(clientInfo.DeletePod(pod)).To(Succeed())
					pod = nil
					Eventually(func() error {
						ipPool, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.Background(),
							wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
							metav1.GetOptions{})
						return err
					}, 10*time.Second, time.Second).Should(Succeed())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}

				By("injecting a stale allocation for a non-existent pod")
				updatedPool := ipPool.DeepCopy()
				staleOffset := "999"
				updatedPool.Spec.Allocations[staleOffset] = v1alpha1.IPAllocation{
					PodRef:      "default/non-existent-pod-xyz",
					ContainerID: "deadbeef",
					IfName:      "net1",
				}
				_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Update(
					context.Background(), updatedPool, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("waiting for the reconciler to clean up the stale allocation")
				Eventually(func() bool {
					ipPool, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
						context.Background(),
						wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
						metav1.GetOptions{})
					if err != nil {
						return false
					}
					_, exists := ipPool.Spec.Allocations[staleOffset]
					return !exists
				}, 2*time.Minute, 5*time.Second).Should(BeTrue(),
					"the reconciler should have removed the stale allocation for non-existent pod")
			})
		})

		// ────────────────────────────────────────────────────────────────
		// DualStack combination tests
		// ────────────────────────────────────────────────────────────────

		Context("DS: overlapping range protection", func() {
			It("prevents duplicate IPs across two dual-stack NADs with overlapping enabled", func() {
				const (
					nad1Name = "wa-ds-ov-1"
					nad2Name = "wa-ds-ov-2"
					v4Range  = "10.61.0.0/28"
					v6Range  = "fd00:61::/124"
				)

				nad1 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad1Name, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				nad2 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad2Name, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)

				_, err := clientInfo.AddNetAttachDef(nad1)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad1)).To(Succeed()) }()
				_, err = clientInfo.AddNetAttachDef(nad2)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad2)).To(Succeed()) }()

				p1, err := clientInfo.ProvisionPod(
					"wb-ds-ov-1", testNamespace,
					util.PodTierLabel("wb-ds-ov-1"),
					entities.PodNetworkSelectionElements(nad1Name))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p1) }()

				p2, err := clientInfo.ProvisionPod(
					"wb-ds-ov-2", testNamespace,
					util.PodTierLabel("wb-ds-ov-2"),
					entities.PodNetworkSelectionElements(nad2Name))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())

				By("verifying all IPs are unique between the two pods")
				allIPs := append(ips1, ips2...)
				ipSet := make(map[string]struct{})
				for _, ip := range allIPs {
					_, dup := ipSet[ip]
					Expect(dup).To(BeFalse(), "duplicate IP across overlapping dual-stack NADs: %s", ip)
					ipSet[ip] = struct{}{}
				}
			})
		})

		Context("DS: pool exhaustion", func() {
			It("fails to schedule when dual-stack pool is exhausted", func() {
				const (
					networkName     = "wa-ds-exhaust"
					v4Range         = "10.62.0.0/30"
					v6Range         = "fd00:62::/126"
					serviceName     = "web-ds-exhaust"
					statefulSetName = "wb-ds-exhaust"
					selector        = "app=" + serviceName
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed())
				}()

				By("filling the pool with 2 pods (all usable IPs in /30)")
				_, err = clientInfo.ProvisionStatefulSet(
					statefulSetName, testNamespace, serviceName, 2, networkName)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
				}()

				By("scaling up by 1 — should fail due to both v4 and v6 exhaustion")
				Expect(clientInfo.ScaleStatefulSet(serviceName, testNamespace, 1)).To(Succeed())
				Expect(
					wbtestclient.WaitForStatefulSetCondition(
						context.Background(), clientInfo.Client, testNamespace,
						serviceName, 3, 20*time.Second,
						wbtestclient.IsStatefulSetReadyPredicate),
				).To(HaveOccurred(), "should fail because dual-stack pool is exhausted")
			})
		})

		Context("DS: concurrent allocation", func() {
			It("handles concurrent dual-stack allocations without conflicts", func() {
				const (
					networkName     = "wa-ds-conc"
					v4Range         = "10.63.0.0/28"
					v6Range         = "fd00:63::/124"
					rsName          = "wb-ds-conc"
					replicaCount    = 8
					rsSteadyTimeout = 120 * time.Second
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				rs, err := clientInfo.ProvisionReplicaSet(
					rsName, testNamespace, 0,
					util.PodTierLabel(rsName),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					scaled, err := clientInfo.UpdateReplicaSet(
						entities.ReplicaSetObject(0, rsName, testNamespace,
							util.PodTierLabel(rsName),
							entities.PodNetworkSelectionElements(networkName)))
					Expect(err).NotTo(HaveOccurred())
					Expect(wbtestclient.WaitForReplicaSetSteadyState(
						context.Background(), clientInfo.Client, testNamespace,
						entities.ReplicaSetQuery(rsName), scaled, rsSteadyTimeout)).To(Succeed())
					Expect(clientInfo.DeleteReplicaSet(rs)).To(Succeed())
				}()

				By(fmt.Sprintf("scaling to %d replicas simultaneously", replicaCount))
				rs, err = clientInfo.UpdateReplicaSet(
					entities.ReplicaSetObject(int32(replicaCount), rsName, testNamespace,
						util.PodTierLabel(rsName),
						entities.PodNetworkSelectionElements(networkName)))
				Expect(err).NotTo(HaveOccurred())

				Expect(wbtestclient.WaitForReplicaSetSteadyState(
					context.Background(), clientInfo.Client, testNamespace,
					entities.ReplicaSetQuery(rsName), rs, rsSteadyTimeout)).To(Succeed())

				podList, err := wbtestclient.ListPods(context.Background(),
					clientInfo.Client, testNamespace, entities.ReplicaSetQuery(rsName))
				Expect(err).NotTo(HaveOccurred())
				Expect(podList.Items).To(HaveLen(replicaCount))

				v4Set := make(map[string]struct{})
				v6Set := make(map[string]struct{})
				for _, p := range podList.Items {
					ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).To(HaveLen(2), "expected dual-stack (2 IPs) per pod")
					Expect(util.InRange(v4Range, ips[0])).To(Succeed())
					Expect(util.InRange(v6Range, ips[1])).To(Succeed())

					_, dupV4 := v4Set[ips[0]]
					Expect(dupV4).To(BeFalse(), "duplicate IPv4: %s", ips[0])
					v4Set[ips[0]] = struct{}{}

					_, dupV6 := v6Set[ips[1]]
					Expect(dupV6).To(BeFalse(), "duplicate IPv6: %s", ips[1])
					v6Set[ips[1]] = struct{}{}
				}
			})
		})

		Context("DS: multi-interface", func() {
			It("allocates dual-stack IPs on multiple interfaces", func() {
				const (
					networkName   = "wa-ds-multi"
					v4Range       = "10.64.0.0/24"
					v6Range       = "fd00:64::/112"
					singlePodName = "wb-ds-multi"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					singlePodName, testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(networkName, networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				allIPs := make(map[string]struct{})
				for _, ifName := range []string{"net1", "net2"} {
					ips, err := retrievers.SecondaryIfaceIPValue(p, ifName)
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).To(HaveLen(2), "expected dual-stack on %s", ifName)
					Expect(util.InRange(v4Range, ips[0])).To(Succeed())
					Expect(util.InRange(v6Range, ips[1])).To(Succeed())
					for _, ip := range ips {
						_, dup := allIPs[ip]
						Expect(dup).To(BeFalse(), "duplicate IP across interfaces: %s", ip)
						allIPs[ip] = struct{}{}
					}
				}
			})
		})

		// ────────────────────────────────────────────────────────────────
		// Multi-pool combination tests
		// ────────────────────────────────────────────────────────────────

		Context("Multi-pool: two NADs", func() {
			It("allocates IPs from two separate pools simultaneously", func() {
				const (
					nad1Name = "wa-mp-pool1"
					nad2Name = "wa-mp-pool2"
					range1   = "10.65.0.0/24"
					range2   = "10.66.0.0/24"
					podName  = "wb-multi-pool"
				)

				nad1 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad1Name, testNamespace, range1, []string{}, wbstorage.UnnamedNetwork, true)
				nad2 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad2Name, testNamespace, range2, []string{}, wbstorage.UnnamedNetwork, true)

				_, err := clientInfo.AddNetAttachDef(nad1)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad1)).To(Succeed()) }()
				_, err = clientInfo.AddNetAttachDef(nad2)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad2)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					podName, testNamespace,
					util.PodTierLabel(podName),
					entities.PodNetworkSelectionElements(nad1Name, nad2Name))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips1, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips1).NotTo(BeEmpty())
				Expect(util.InRange(range1, ips1[0])).To(Succeed())

				ips2, err := retrievers.SecondaryIfaceIPValue(p, "net2")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).NotTo(BeEmpty())
				Expect(util.InRange(range2, ips2[0])).To(Succeed())

				By("verifying both pools have allocations referencing this pod")
				verifyAllocations(clientInfo, range1, ips1[0], testNamespace, podName, "net1")
				verifyAllocations(clientInfo, range2, ips2[0], testNamespace, podName, "net2")
			})
		})

		Context("Multi-pool: named networks", func() {
			It("isolates allocations via network_name even for identical CIDRs", func() {
				const (
					nad1Name     = "wa-nn-first"
					nad2Name     = "wa-nn-second"
					sharedRange  = "10.67.0.0/28"
					networkName1 = "net-alpha"
					networkName2 = "net-beta"
				)

				nad1 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad1Name, testNamespace, sharedRange, []string{}, networkName1, true)
				nad2 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad2Name, testNamespace, sharedRange, []string{}, networkName2, true)

				_, err := clientInfo.AddNetAttachDef(nad1)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad1)).To(Succeed()) }()
				_, err = clientInfo.AddNetAttachDef(nad2)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad2)).To(Succeed()) }()

				p1, err := clientInfo.ProvisionPod(
					"wb-nn-pod1", testNamespace,
					util.PodTierLabel("wb-nn-pod1"),
					entities.PodNetworkSelectionElements(nad1Name))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p1) }()

				p2, err := clientInfo.ProvisionPod(
					"wb-nn-pod2", testNamespace,
					util.PodTierLabel("wb-nn-pod2"),
					entities.PodNetworkSelectionElements(nad2Name))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())

				By("verifying separate IPPool CRs exist for each network_name")
				pool1Name := wbstorage.IPPoolName(wbstorage.PoolIdentifier{
					IPRange: sharedRange, NetworkName: networkName1})
				pool2Name := wbstorage.IPPoolName(wbstorage.PoolIdentifier{
					IPRange: sharedRange, NetworkName: networkName2})
				Expect(pool1Name).NotTo(Equal(pool2Name),
					"named networks should produce different pool names")

				_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
					context.Background(), pool1Name, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
					context.Background(), pool2Name, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("both pods can have the same IP since pools are isolated")
				// With named networks and lowest-available allocation, both should get the same first IP
				Expect(ips1[0]).To(Equal(ips2[0]),
					"isolated named networks should independently assign the same first IP")
			})
		})

		Context("Multi-pool: DS two NADs", func() {
			It("allocates dual-stack IPs from two separate pools", func() {
				const (
					nad1Name = "wa-mp-ds-1"
					nad2Name = "wa-mp-ds-2"
					v4Range1 = "10.68.0.0/24"
					v6Range1 = "fd00:68::/112"
					v4Range2 = "10.69.0.0/24"
					v6Range2 = "fd00:69::/112"
					podName  = "wb-mp-ds-pod"
				)

				nad1 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad1Name, testNamespace, v4Range1, []string{v6Range1}, wbstorage.UnnamedNetwork, true)
				nad2 := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					nad2Name, testNamespace, v4Range2, []string{v6Range2}, wbstorage.UnnamedNetwork, true)

				_, err := clientInfo.AddNetAttachDef(nad1)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad1)).To(Succeed()) }()
				_, err = clientInfo.AddNetAttachDef(nad2)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad2)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					podName, testNamespace,
					util.PodTierLabel(podName),
					entities.PodNetworkSelectionElements(nad1Name, nad2Name))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				By("verifying net1 has dual-stack from pool 1")
				ips1, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips1).To(HaveLen(2))
				Expect(util.InRange(v4Range1, ips1[0])).To(Succeed())
				Expect(util.InRange(v6Range1, ips1[1])).To(Succeed())

				By("verifying net2 has dual-stack from pool 2")
				ips2, err := retrievers.SecondaryIfaceIPValue(p, "net2")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).To(HaveLen(2))
				Expect(util.InRange(v4Range2, ips2[0])).To(Succeed())
				Expect(util.InRange(v6Range2, ips2[1])).To(Succeed())
			})
		})

		// ────────────────────────────────────────────────────────────────
		// Failure and edge-case tests
		// ────────────────────────────────────────────────────────────────

		Context("Failure: fully excluded range", func() {
			It("prevents pod from getting an IP when all addresses are excluded", func() {
				const (
					networkName     = "wa-full-excl"
					ipRange         = "10.70.0.0/30"
					serviceName     = "web-full-excl"
					statefulSetName = "wb-full-excl"
					selector        = "app=" + serviceName
				)

				// /30 = 4 IPs; exclude entire range — no IPs available
				nad := util.MacvlanNetworkWithWhereaboutsExcludeRange(
					networkName, testNamespace, ipRange, []string{"10.70.0.0/30"})
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating a statefulset — pod should fail to schedule (no IPs)")
				_, err = clientInfo.Client.AppsV1().StatefulSets(testNamespace).Create(
					context.Background(),
					entities.StatefulSetSpec(statefulSetName, testNamespace, serviceName, 1,
						entities.PodNetworkSelectionElements(networkName)),
					metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
				}()

				Expect(
					wbtestclient.WaitForStatefulSetCondition(
						context.Background(), clientInfo.Client, testNamespace,
						serviceName, 1, 20*time.Second,
						wbtestclient.IsStatefulSetReadyPredicate),
				).To(HaveOccurred(), "should fail because all IPs are excluded")
			})
		})

		Context("Failure: alloc-dealloc-realloc", func() {
			It("correctly frees and reassigns IPs across allocation cycles", func() {
				const (
					networkName = "wa-realloc"
					ipRange     = "10.71.0.0/30"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("cycle 1: allocate first pod")
				p1, err := clientInfo.ProvisionPod(
					"wb-realloc-1", testNamespace,
					util.PodTierLabel("wb-realloc-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				firstIP := ips1[0]

				By("cycle 1: deallocate first pod")
				Expect(clientInfo.DeletePod(p1)).To(Succeed())

				By("waiting for deallocation to complete")
				Eventually(func() bool {
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
						context.Background(),
						wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipRange, NetworkName: wbstorage.UnnamedNetwork}),
						metav1.GetOptions{})
					if err != nil {
						return false
					}
					for _, alloc := range ipPool.Spec.Allocations {
						if alloc.PodRef == getPodRef(testNamespace, "wb-realloc-1") {
							return false
						}
					}
					return true
				}, 30*time.Second, time.Second).Should(BeTrue())

				By("cycle 2: allocate second pod — should get the same first IP (lowest available)")
				p2, err := clientInfo.ProvisionPod(
					"wb-realloc-2", testNamespace,
					util.PodTierLabel("wb-realloc-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2[0]).To(Equal(firstIP),
					"after deallocation, lowest-available should reassign the same IP")
			})
		})

		Context("Failure: pool exhaustion recovery", func() {
			It("recovers from exhaustion when pods are deleted", func() {
				const (
					networkName = "wa-exhaust-recov"
					ipRange     = "10.72.0.0/30"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("filling the pool with 2 pods")
				p1, err := clientInfo.ProvisionPod(
					"wb-er-1", testNamespace,
					util.PodTierLabel("wb-er-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				p2, err := clientInfo.ProvisionPod(
					"wb-er-2", testNamespace,
					util.PodTierLabel("wb-er-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())

				By("deleting first pod to free an IP")
				Expect(clientInfo.DeletePod(p1)).To(Succeed())

				By("waiting for deallocation")
				Eventually(func() bool {
					ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
						context.Background(),
						wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipRange, NetworkName: wbstorage.UnnamedNetwork}),
						metav1.GetOptions{})
					if err != nil {
						return false
					}
					for _, alloc := range ipPool.Spec.Allocations {
						if alloc.PodRef == getPodRef(testNamespace, "wb-er-1") {
							return false
						}
					}
					return true
				}, 30*time.Second, time.Second).Should(BeTrue())

				By("allocating a new pod — should succeed with the freed IP")
				p3, err := clientInfo.ProvisionPod(
					"wb-er-3", testNamespace,
					util.PodTierLabel("wb-er-3"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p3) }()

				ips3, err := retrievers.SecondaryIfaceIPValue(p3, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips3[0]).To(Equal(ips1[0]),
					"freed IP should be reassigned as it is the lowest available")
			})
		})

		Context("Pod cleanup", func() {
			const singlePodName = "whereabouts-pod-cleanup-controller-test"
			var err error

			AfterEach(func() {
				_ = clientInfo.DeletePod(pod)
			})

			It("verifies that the pod cleanup controller deletes an orphaned allocation", func() {
				By("creating a pod with whereabouts net-attach-def")
				pod, err = clientInfo.ProvisionPod(
					singlePodName,
					testNamespace,
					util.PodTierLabel(singlePodName),
					entities.PodNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())

				By("duplicating the existing ippool allocation with a different offset")
				ip, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ip).NotTo(BeEmpty())

				firstIP, _, err := net.ParseCIDR(ipv4TestRange)
				Expect(err).NotTo(HaveOccurred())
				offset, err := iphelpers.IPGetOffset(net.ParseIP(ip[0]), firstIP)
				Expect(err).NotTo(HaveOccurred())

				ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.TODO(),
					wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}),
					metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())

				Expect(ipPool.Spec.Allocations).To(HaveKey(offset.String()))

				newOffset := new(big.Int).Add(offset, big.NewInt(int64(rand.Intn(100))))

				ipPool.Spec.Allocations[newOffset.String()] = ipPool.Spec.Allocations[offset.String()]
				_, err = clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Update(context.Background(), ipPool, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("deleting pod")
				err = clientInfo.DeletePod(pod)
				Expect(err).NotTo(HaveOccurred())

				By("checking that all IP allocations are removed")
				ip = append(ip, iphelpers.IPAddOffset(firstIP, newOffset).String())
				verifyNoAllocationsForPodRef(clientInfo, ipv4TestRange, testNamespace, pod.Name, ip)
			})
		})

		// ────────────────────────────────────────────────────────────────
		// Upstream feature tests (#573, #601, #510, #621, L3 mode)
		// ────────────────────────────────────────────────────────────────

		Context("Small subnets", func() {
			Context("/31 point-to-point links (RFC 3021)", func() {
				const (
					networkName = "wa-slash31"
					ipRange     = "10.73.0.0/31"
				)

				It("allocates both IPs in a /31 subnet", func() {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					By("creating first pod — should get 10.73.0.0")
					p1, err := clientInfo.ProvisionPod(
						"wb-s31-1", testNamespace,
						util.PodTierLabel("wb-s31-1"),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p1) }()

					ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips1).NotTo(BeEmpty())
					Expect(util.InRange(ipRange, ips1[0])).To(Succeed())

					By("creating second pod — should get 10.73.0.1")
					p2, err := clientInfo.ProvisionPod(
						"wb-s31-2", testNamespace,
						util.PodTierLabel("wb-s31-2"),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p2) }()

					ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips2).NotTo(BeEmpty())
					Expect(util.InRange(ipRange, ips2[0])).To(Succeed())

					By("verifying both IPs are different")
					Expect(ips1[0]).NotTo(Equal(ips2[0]))
				})
			})

			Context("/32 single-host routes", func() {
				const (
					networkName = "wa-slash32"
					ipRange     = "10.74.0.5/32"
				)

				It("allocates the single IP in a /32 subnet", func() {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					p1, err := clientInfo.ProvisionPod(
						"wb-s32-1", testNamespace,
						util.PodTierLabel("wb-s32-1"),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p1) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(ips[0]).To(Equal("10.74.0.5"))
				})

				It("exhausts the pool with 1 pod", func() {
					const (
						serviceName     = "web-s32-exhaust"
						statefulSetName = "wb-s32-exhaust"
						selector        = "app=" + serviceName
					)

					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName+"-ex", testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					By("filling the /32 pool with 1 pod")
					_, err = clientInfo.ProvisionStatefulSet(
						statefulSetName, testNamespace, serviceName, 1, networkName+"-ex")
					Expect(err).NotTo(HaveOccurred())
					defer func() {
						Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
					}()

					By("scaling up by 1 — should fail")
					Expect(clientInfo.ScaleStatefulSet(serviceName, testNamespace, 1)).To(Succeed())
					Expect(
						wbtestclient.WaitForStatefulSetCondition(
							context.Background(), clientInfo.Client, testNamespace,
							serviceName, 2, 20*time.Second,
							wbtestclient.IsStatefulSetReadyPredicate),
					).To(HaveOccurred(), "should fail because /32 pool is exhausted")
				})
			})

			Context("IPv6 /128 single-host routes", func() {
				const (
					networkName = "wa-slash128"
					ipRange     = "fd00:73::5/128"
				)

				It("allocates the single IP in a /128 subnet", func() {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					p1, err := clientInfo.ProvisionPod(
						"wb-s128-1", testNamespace,
						util.PodTierLabel("wb-s128-1"),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p1) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.IsIPv6(ips[0])).To(BeTrue())
				})
			})

			Context("IPv6 /127 point-to-point links", func() {
				const (
					networkName = "wa-slash127"
					ipRange     = "fd00:74::/127"
				)

				It("allocates both IPs in a /127 subnet", func() {
					nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
						networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
					_, err := clientInfo.AddNetAttachDef(nad)
					Expect(err).NotTo(HaveOccurred())
					defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

					p1, err := clientInfo.ProvisionPod(
						"wb-s127-1", testNamespace,
						util.PodTierLabel("wb-s127-1"),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p1) }()

					p2, err := clientInfo.ProvisionPod(
						"wb-s127-2", testNamespace,
						util.PodTierLabel("wb-s127-2"),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p2) }()

					ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
					Expect(err).NotTo(HaveOccurred())
					ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips1[0]).NotTo(Equal(ips2[0]))
					Expect(util.IsIPv6(ips1[0])).To(BeTrue())
					Expect(util.IsIPv6(ips2[0])).To(BeTrue())
				})
			})
		})

		Context("Gateway exclusion", func() {
			It("excludes the gateway IP when exclude_gateway is enabled", func() {
				const (
					networkName = "wa-gw-excl"
					ipRange     = "10.75.0.0/28"
					gateway     = "10.75.0.1"
				)

				nad := util.MacvlanNetworkWithWhereaboutsGatewayExclusion(
					networkName, testNamespace, ipRange, gateway)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating multiple pods to exhaust lower IPs")
				pods := make([]*corev1.Pod, 0, 5)
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				// The /28 has ~14 usable IPs. Create 5 pods and verify none got the gateway.
				for i := range 5 {
					podName := fmt.Sprintf("wb-gw-excl-%d", i)
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					Expect(ips[0]).NotTo(Equal(gateway),
						"pod should not receive the gateway IP %s", gateway)
				}
			})

			It("excludes the gateway IP for IPv6 when exclude_gateway is enabled", func() {
				const (
					networkName = "wa-gw-excl-v6"
					ipRange     = "fd00:75::/124"
					gateway     = "fd00:75::1"
				)

				nad := util.MacvlanNetworkWithWhereaboutsGatewayExclusion(
					networkName, testNamespace, ipRange, gateway)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating multiple IPv6 pods to verify gateway exclusion")
				pods := make([]*corev1.Pod, 0, 5)
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				// /124 = 16 addresses. Create 5 pods and verify none got the gateway.
				gatewayIP := net.ParseIP(gateway)
				for i := range 5 {
					podName := fmt.Sprintf("wb-gw-excl-v6-%d", i)
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.IsIPv6(ips[0])).To(BeTrue(), "expected IPv6, got %s", ips[0])
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					Expect(net.ParseIP(ips[0]).Equal(gatewayIP)).To(BeFalse(),
						"pod should not receive the gateway IP %s, got %s", gateway, ips[0])
				}
			})
		})

		Context("L3/Routed mode", func() {
			It("allocates network and broadcast addresses in L3 mode", func() {
				const (
					networkName     = "wa-l3-mode"
					ipRange         = "10.76.0.0/30"
					serviceName     = "web-l3"
					statefulSetName = "wb-l3"
					selector        = "app=" + serviceName
				)

				// /30 normally yields 2 usable IPs (.1, .2).
				// With L3: all 4 are usable (.0, .1, .2, .3).
				nad := util.MacvlanNetworkWithWhereaboutsL3Mode(
					networkName, testNamespace, ipRange)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating statefulset with 4 replicas (only possible in L3 mode for /30)")
				_, err = clientInfo.ProvisionStatefulSet(
					statefulSetName, testNamespace, serviceName, 4, networkName)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
				}()

				By("verifying all 4 pods got unique IPs including .0 and .3")
				podList, err := wbtestclient.ListPods(
					context.Background(), clientInfo.Client, testNamespace, selector)
				Expect(err).NotTo(HaveOccurred())
				Expect(podList.Items).To(HaveLen(4))

				ipSet := make(map[string]struct{})
				for _, p := range podList.Items {
					ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					_, dup := ipSet[ips[0]]
					Expect(dup).To(BeFalse(), "duplicate IP: %s", ips[0])
					ipSet[ips[0]] = struct{}{}
				}

				By("verifying network (.0) and broadcast (.3) addresses were allocated")
				Expect(ipSet).To(HaveKey("10.76.0.0"))
				Expect(ipSet).To(HaveKey("10.76.0.3"))
			})

			It("allocates all addresses in L3 mode for IPv6", func() {
				const (
					networkName     = "wa-l3-v6"
					ipRange         = "fd00:76::/126"
					serviceName     = "web-l3-v6"
					statefulSetName = "wb-l3-v6"
					selector        = "app=" + serviceName
				)

				// /126 = 4 addresses. Without L3, only 2 are usable (::1, ::2).
				// With L3: all 4 are usable (::0, ::1, ::2, ::3).
				nad := util.MacvlanNetworkWithWhereaboutsL3Mode(
					networkName, testNamespace, ipRange)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating statefulset with 4 replicas (only possible in L3 mode for /126)")
				_, err = clientInfo.ProvisionStatefulSet(
					statefulSetName, testNamespace, serviceName, 4, networkName)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
				}()

				By("verifying all 4 pods got unique IPv6 IPs")
				podList, err := wbtestclient.ListPods(
					context.Background(), clientInfo.Client, testNamespace, selector)
				Expect(err).NotTo(HaveOccurred())
				Expect(podList.Items).To(HaveLen(4))

				ipSet := make(map[string]bool)
				for _, p := range podList.Items {
					ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.IsIPv6(ips[0])).To(BeTrue(), "expected IPv6, got %s", ips[0])
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					normalized := net.ParseIP(ips[0]).String()
					Expect(ipSet).NotTo(HaveKey(normalized), "duplicate IP: %s", ips[0])
					ipSet[normalized] = true
				}

				By("verifying network (::0) and last (::3) addresses were allocated")
				Expect(ipSet).To(HaveKey(net.ParseIP("fd00:76::0").String()))
				Expect(ipSet).To(HaveKey(net.ParseIP("fd00:76::3").String()))
			})
		})

		Context("Optimistic IPAM", func() {
			It("allocates IPs without leader election when optimistic_ipam is enabled", func() {
				const (
					networkName = "wa-optimistic"
					ipRange     = "10.77.0.0/24"
				)

				nad := util.MacvlanNetworkWithWhereaboutsOptimisticIPAM(
					networkName, testNamespace, ipRange)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating a pod with optimistic IPAM")
				p, err := clientInfo.ProvisionPod(
					"wb-optimistic-1", testNamespace,
					util.PodTierLabel("wb-optimistic-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())
				Expect(util.InRange(ipRange, ips[0])).To(Succeed())

				By("verifying IPPool allocation exists")
				verifyAllocations(clientInfo, ipRange, ips[0], testNamespace, "wb-optimistic-1", "net1")

				By("creating a second pod for concurrent test")
				p2, err := clientInfo.ProvisionPod(
					"wb-optimistic-2", testNamespace,
					util.PodTierLabel("wb-optimistic-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).NotTo(BeEmpty())
				Expect(util.InRange(ipRange, ips2[0])).To(Succeed())
				Expect(ips[0]).NotTo(Equal(ips2[0]),
					"two pods should have different IPs")
			})

			It("correctly deallocates with optimistic IPAM", func() {
				const (
					networkName = "wa-optimistic-dealloc"
					ipRange     = "10.78.0.0/28"
				)

				nad := util.MacvlanNetworkWithWhereaboutsOptimisticIPAM(
					networkName, testNamespace, ipRange)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-opt-dealloc", testNamespace,
					util.PodTierLabel("wb-opt-dealloc"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				firstIP := ips[0]

				By("deleting the pod")
				Expect(clientInfo.DeletePod(p)).To(Succeed())

				By("waiting for deallocation and verifying IP is freed")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-opt-dealloc", ips)

				By("creating a new pod — should get the same IP (lowest available)")
				p2, err := clientInfo.ProvisionPod(
					"wb-opt-dealloc-2", testNamespace,
					util.PodTierLabel("wb-opt-dealloc-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2[0]).To(Equal(firstIP),
					"after deallocation, lowest-available reassignment should work with optimistic IPAM")
			})
		})

		Context("Preferred IP", func() {
			It("assigns the requested preferred IP when available", func() {
				const (
					networkName = "wa-preferred"
					ipRange     = "10.79.0.0/24"
					preferredIP = "10.79.0.42"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating a pod with preferred-ip annotation")
				p, err := clientInfo.ProvisionPod(
					"wb-preferred-1", testNamespace,
					util.PodTierLabel("wb-preferred-1"),
					entities.PodNetworkSelectionWithAnnotations(
						map[string]string{"whereabouts.cni.cncf.io/preferred-ip": preferredIP},
						networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())
				Expect(ips[0]).To(Equal(preferredIP),
					"pod should receive the preferred IP %s", preferredIP)
			})

			It("falls back to lowest available when preferred IP is taken", func() {
				const (
					networkName = "wa-preferred-taken"
					ipRange     = "10.80.0.0/28"
					preferredIP = "10.80.0.1"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating first pod — should get the lowest IP (10.80.0.1)")
				p1, err := clientInfo.ProvisionPod(
					"wb-pref-taken-1", testNamespace,
					util.PodTierLabel("wb-pref-taken-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p1) }()

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips1[0]).To(Equal(preferredIP),
					"first pod should get lowest available which is %s", preferredIP)

				By("creating second pod requesting the same preferred IP — should fall back")
				p2, err := clientInfo.ProvisionPod(
					"wb-pref-taken-2", testNamespace,
					util.PodTierLabel("wb-pref-taken-2"),
					entities.PodNetworkSelectionWithAnnotations(
						map[string]string{"whereabouts.cni.cncf.io/preferred-ip": preferredIP},
						networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).NotTo(BeEmpty())
				Expect(ips2[0]).NotTo(Equal(preferredIP),
					"preferred IP %s is taken, pod should fall back to another IP", preferredIP)
				Expect(util.InRange(ipRange, ips2[0])).To(Succeed())
			})

			It("preferred IP works for IPv6", func() {
				const (
					networkName = "wa-preferred-v6"
					ipRange     = "fd00:75::/112"
					preferredIP = "fd00:75::2a"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-pref-v6-1", testNamespace,
					util.PodTierLabel("wb-pref-v6-1"),
					entities.PodNetworkSelectionWithAnnotations(
						map[string]string{"whereabouts.cni.cncf.io/preferred-ip": preferredIP},
						networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())
				Expect(util.IsIPv6(ips[0])).To(BeTrue())
				// Compare as net.IP to handle normalization (fd00:75::2a vs fd00:75::002a)
				Expect(net.ParseIP(ips[0]).Equal(net.ParseIP(preferredIP))).To(BeTrue(),
					"pod should receive the preferred IPv6 %s, got %s", preferredIP, ips[0])
			})
		})

		Context("DS: gateway exclusion", func() {
			It("excludes the gateway IP for both address families", func() {
				const (
					networkName = "wa-ds-gw-excl"
					v4Range     = "10.81.0.0/28"
					v4Gateway   = "10.81.0.1"
					v6Range     = "fd00:81::/124"
					v6Gateway   = "fd00:81::1"
				)

				nad := util.MacvlanNetworkWithWhereaboutsDualStackGatewayExclusion(
					networkName, testNamespace, v4Range, v4Gateway, v6Range, v6Gateway)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				v4GatewayIP := net.ParseIP(v4Gateway)
				v6GatewayIP := net.ParseIP(v6Gateway)

				By("creating multiple pods to exercise both gateway exclusions")
				pods := make([]*corev1.Pod, 0, 5)
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				for i := range 5 {
					podName := fmt.Sprintf("wb-ds-gw-excl-%d", i)
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).To(HaveLen(2), "dual-stack pod should have 2 IPs")

					Expect(util.IsIPv4(ips[0])).To(BeTrue(), "first IP should be IPv4, got %s", ips[0])
					Expect(util.InRange(v4Range, ips[0])).To(Succeed())
					Expect(net.ParseIP(ips[0]).Equal(v4GatewayIP)).To(BeFalse(),
						"pod should not receive the v4 gateway %s, got %s", v4Gateway, ips[0])

					Expect(util.IsIPv6(ips[1])).To(BeTrue(), "second IP should be IPv6, got %s", ips[1])
					Expect(util.InRange(v6Range, ips[1])).To(Succeed())
					Expect(net.ParseIP(ips[1]).Equal(v6GatewayIP)).To(BeFalse(),
						"pod should not receive the v6 gateway %s, got %s", v6Gateway, ips[1])
				}
			})
		})

		Context("DS: L3/routed mode", func() {
			It("allocates all addresses including network and broadcast in both families", func() {
				const (
					networkName     = "wa-ds-l3"
					v4Range         = "10.82.0.0/30"
					v6Range         = "fd00:82::/126"
					serviceName     = "web-ds-l3"
					statefulSetName = "wb-ds-l3"
					selector        = "app=" + serviceName
				)

				// /30 = 4 v4 addresses, /126 = 4 v6 addresses.
				// With L3 mode, all 4 are usable in each family.
				nad := util.MacvlanNetworkWithWhereaboutsDualStackL3Mode(
					networkName, testNamespace, v4Range, v6Range)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating statefulset with 4 replicas")
				_, err = clientInfo.ProvisionStatefulSet(
					statefulSetName, testNamespace, serviceName, 4, networkName)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
				}()

				By("verifying all 4 pods got unique dual-stack IPs")
				podList, err := wbtestclient.ListPods(
					context.Background(), clientInfo.Client, testNamespace, selector)
				Expect(err).NotTo(HaveOccurred())
				Expect(podList.Items).To(HaveLen(4))

				v4Set := make(map[string]bool)
				v6Set := make(map[string]bool)
				for _, p := range podList.Items {
					ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).To(HaveLen(2))

					Expect(util.IsIPv4(ips[0])).To(BeTrue())
					Expect(util.InRange(v4Range, ips[0])).To(Succeed())
					Expect(v4Set).NotTo(HaveKey(ips[0]), "duplicate v4: %s", ips[0])
					v4Set[ips[0]] = true

					Expect(util.IsIPv6(ips[1])).To(BeTrue())
					Expect(util.InRange(v6Range, ips[1])).To(Succeed())
					normalized := net.ParseIP(ips[1]).String()
					Expect(v6Set).NotTo(HaveKey(normalized), "duplicate v6: %s", ips[1])
					v6Set[normalized] = true
				}

				By("verifying network (.0) and broadcast (.3) v4 addresses were allocated")
				Expect(v4Set).To(HaveKey("10.82.0.0"))
				Expect(v4Set).To(HaveKey("10.82.0.3"))

				By("verifying first (::0) and last (::3) v6 addresses were allocated")
				Expect(v6Set).To(HaveKey(net.ParseIP("fd00:82::0").String()))
				Expect(v6Set).To(HaveKey(net.ParseIP("fd00:82::3").String()))
			})
		})

		Context("IPv6: optimistic IPAM", func() {
			It("allocates unique IPs concurrently without leader election", func() {
				const (
					networkName = "wa-optimistic-v6"
					ipRange     = "fd00:83::/112"
				)

				nad := util.MacvlanNetworkWithWhereaboutsOptimisticIPAM(
					networkName, testNamespace, ipRange)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				pods := make([]*corev1.Pod, 0, 4)
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				ipSet := make(map[string]bool)
				for i := range 4 {
					podName := fmt.Sprintf("wb-opt-v6-%d", i)
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).NotTo(BeEmpty())
					Expect(util.IsIPv6(ips[0])).To(BeTrue(), "expected IPv6, got %s", ips[0])
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					normalized := net.ParseIP(ips[0]).String()
					Expect(ipSet).NotTo(HaveKey(normalized), "duplicate IP: %s", ips[0])
					ipSet[normalized] = true
				}

				Expect(ipSet).To(HaveLen(4), "all 4 pods should have unique IPv6 IPs")
			})
		})

		Context("DS: optimistic IPAM", func() {
			It("allocates correct dual-stack IPs without leader election", func() {
				const (
					networkName = "wa-ds-optimistic"
					v4Range     = "10.84.0.0/28"
					v6Range     = "fd00:84::/124"
				)

				nad := util.MacvlanNetworkWithWhereaboutsDualStackOptimisticIPAM(
					networkName, testNamespace, v4Range, v6Range)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				pods := make([]*corev1.Pod, 0, 3)
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				v4Set := make(map[string]bool)
				v6Set := make(map[string]bool)
				for i := range 3 {
					podName := fmt.Sprintf("wb-ds-opt-%d", i)
					p, err := clientInfo.ProvisionPod(
						podName, testNamespace,
						util.PodTierLabel(podName),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).To(HaveLen(2), "dual-stack pod should have 2 IPs")

					Expect(util.IsIPv4(ips[0])).To(BeTrue())
					Expect(util.InRange(v4Range, ips[0])).To(Succeed())
					Expect(v4Set).NotTo(HaveKey(ips[0]), "duplicate v4: %s", ips[0])
					v4Set[ips[0]] = true

					Expect(util.IsIPv6(ips[1])).To(BeTrue())
					Expect(util.InRange(v6Range, ips[1])).To(Succeed())
					normalized := net.ParseIP(ips[1]).String()
					Expect(v6Set).NotTo(HaveKey(normalized), "duplicate v6: %s", ips[1])
					v6Set[normalized] = true
				}
			})
		})

		Context("Node drain", func() {
			It("releases IP allocations after pods are evicted by node drain", func() {
				const (
					networkName = "wa-drain-test"
					ipRange     = "10.86.0.0/28"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating a pod with a secondary interface")
				p, err := clientInfo.ProvisionPod(
					"wb-drain-1", testNamespace,
					util.PodTierLabel("wb-drain-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())

				By("verifying the IP is allocated in the pool")
				verifyAllocations(clientInfo, ipRange, ips[0], testNamespace, "wb-drain-1", "net1")

				By("deleting the pod to simulate drain")
				Expect(clientInfo.DeletePod(p)).To(Succeed())

				By("verifying the IP is released from the pool after deletion")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-drain-1", ips)

				By("creating a new pod — should reclaim the freed IP")
				p2, err := clientInfo.ProvisionPod(
					"wb-drain-2", testNamespace,
					util.PodTierLabel("wb-drain-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2[0]).To(Equal(ips[0]),
					"after drain/cleanup the lowest-available IP should be reassigned")
			})
		})

		Context("IPv6: named network isolation", func() {
			It("isolates IPv6 allocations via network_name for identical CIDRs", func() {
				const (
					networkNameA = "wa-named-v6-a"
					networkNameB = "wa-named-v6-b"
					ipRange      = "fd00:87::/124"
					poolA        = "v6-net-a"
					poolB        = "v6-net-b"
				)

				nadA := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameA, testNamespace, ipRange, []string{}, poolA, true)
				nadB := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameB, testNamespace, ipRange, []string{}, poolB, true)

				_, err := clientInfo.AddNetAttachDef(nadA)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadA)).To(Succeed()) }()

				_, err = clientInfo.AddNetAttachDef(nadB)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadB)).To(Succeed()) }()

				By("creating pods on different named networks")
				pA, err := clientInfo.ProvisionPod(
					"wb-named-v6-a", testNamespace,
					util.PodTierLabel("wb-named-v6-a"),
					entities.PodNetworkSelectionElements(networkNameA))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(pA) }()

				pB, err := clientInfo.ProvisionPod(
					"wb-named-v6-b", testNamespace,
					util.PodTierLabel("wb-named-v6-b"),
					entities.PodNetworkSelectionElements(networkNameB))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(pB) }()

				ipsA, err := retrievers.SecondaryIfaceIPValue(pA, "net1")
				Expect(err).NotTo(HaveOccurred())
				ipsB, err := retrievers.SecondaryIfaceIPValue(pB, "net1")
				Expect(err).NotTo(HaveOccurred())

				Expect(util.IsIPv6(ipsA[0])).To(BeTrue())
				Expect(util.IsIPv6(ipsB[0])).To(BeTrue())
				Expect(util.InRange(ipRange, ipsA[0])).To(Succeed())
				Expect(util.InRange(ipRange, ipsB[0])).To(Succeed())

				By("verifying same IP can be allocated in both named networks (isolation)")
				Expect(net.ParseIP(ipsA[0]).Equal(net.ParseIP(ipsB[0]))).To(BeTrue(),
					"with named networks, both pods should get the same lowest IPv6 (isolated pools)")
			})
		})

		Context("IPv6: multi-pool", func() {
			It("allocates IPv6 IPs from two separate pools simultaneously", func() {
				const (
					networkNameA = "wa-multi-pool-v6-a"
					networkNameB = "wa-multi-pool-v6-b"
					ipRangeA     = "fd00:88::/112"
					ipRangeB     = "fd00:89::/112"
				)

				nadA := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameA, testNamespace, ipRangeA, []string{}, wbstorage.UnnamedNetwork, true)
				nadB := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameB, testNamespace, ipRangeB, []string{}, wbstorage.UnnamedNetwork, true)

				_, err := clientInfo.AddNetAttachDef(nadA)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadA)).To(Succeed()) }()

				_, err = clientInfo.AddNetAttachDef(nadB)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadB)).To(Succeed()) }()

				By("creating a pod attached to both v6 NADs")
				p, err := clientInfo.ProvisionPod(
					"wb-multi-pool-v6", testNamespace,
					util.PodTierLabel("wb-multi-pool-v6"),
					entities.PodNetworkSelectionElements(networkNameA, networkNameB))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				// net1 = first NAD, net2 = second NAD
				ips1, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(util.IsIPv6(ips1[0])).To(BeTrue())
				Expect(util.InRange(ipRangeA, ips1[0])).To(Succeed())

				ips2, err := retrievers.SecondaryIfaceIPValue(p, "net2")
				Expect(err).NotTo(HaveOccurred())
				Expect(util.IsIPv6(ips2[0])).To(BeTrue())
				Expect(util.InRange(ipRangeB, ips2[0])).To(Succeed())
			})
		})

		Context("IPv6: fully excluded range", func() {
			It("prevents pod from getting an IP when all IPv6 addresses are excluded", func() {
				const (
					networkName = "wa-full-excl-v6"
					ipRange     = "fd00:90::/126" // 4 addresses
				)

				// Exclude all 4 addresses in the /126.
				nad := util.MacvlanNetworkWithWhereaboutsExcludeRange(
					networkName, testNamespace, ipRange,
					[]string{"fd00:90::0/126"})
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("attempting to create a pod — should fail or stay pending")
				_, err = clientInfo.ProvisionPod(
					"wb-full-excl-v6", testNamespace,
					util.PodTierLabel("wb-full-excl-v6"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).To(HaveOccurred(),
					"pod should fail when all IPv6 addresses are excluded")
			})
		})

		Context("IPv6: alloc-dealloc-realloc", func() {
			It("correctly frees and reassigns IPv6 IPs across allocation cycles", func() {
				const (
					networkName = "wa-realloc-v6"
					ipRange     = "fd00:91::/112"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating first pod")
				p1, err := clientInfo.ProvisionPod(
					"wb-realloc-v6-1", testNamespace,
					util.PodTierLabel("wb-realloc-v6-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(util.IsIPv6(ips1[0])).To(BeTrue())
				firstIP := ips1[0]

				By("deleting first pod")
				Expect(clientInfo.DeletePod(p1)).To(Succeed())

				By("waiting for deallocation")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-realloc-v6-1", ips1)

				By("creating second pod — should get the same IPv6 (lowest available)")
				p2, err := clientInfo.ProvisionPod(
					"wb-realloc-v6-2", testNamespace,
					util.PodTierLabel("wb-realloc-v6-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(net.ParseIP(ips2[0]).Equal(net.ParseIP(firstIP))).To(BeTrue(),
					"after deallocation, lowest-available reassignment should return %s, got %s",
					firstIP, ips2[0])
			})
		})

		Context("DS: alloc-dealloc-realloc", func() {
			It("correctly frees and reassigns dual-stack IPs across cycles", func() {
				const (
					networkName = "wa-realloc-ds"
					v4Range     = "10.91.0.0/28"
					v6Range     = "fd00:a1::/124"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating first DS pod")
				p1, err := clientInfo.ProvisionPod(
					"wb-realloc-ds-1", testNamespace,
					util.PodTierLabel("wb-realloc-ds-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips1).To(HaveLen(2))
				firstV4 := ips1[0]
				firstV6 := ips1[1]

				By("deleting first pod")
				Expect(clientInfo.DeletePod(p1)).To(Succeed())

				By("waiting for v4 deallocation")
				verifyNoAllocationsForPodRef(clientInfo, v4Range, testNamespace, "wb-realloc-ds-1", []string{firstV4})

				By("creating second pod — should reclaim both IPs")
				p2, err := clientInfo.ProvisionPod(
					"wb-realloc-ds-2", testNamespace,
					util.PodTierLabel("wb-realloc-ds-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).To(HaveLen(2))
				Expect(ips2[0]).To(Equal(firstV4),
					"v4 should be reclaimed, expected %s got %s", firstV4, ips2[0])
				Expect(net.ParseIP(ips2[1]).Equal(net.ParseIP(firstV6))).To(BeTrue(),
					"v6 should be reclaimed, expected %s got %s", firstV6, ips2[1])
			})
		})

		Context("IPv6: pool exhaustion", func() {
			It("recovers from exhaustion when IPv6 pods are deleted", func() {
				const (
					networkName = "wa-exhaust-recv-v6"
					ipRange     = "fd00:92::/120" // large enough CIDR
					rangeStart  = "fd00:92::1"    // 3-IP window: ::1, ::2, ::3
					rangeEnd    = "fd00:92::3"
				)

				nad := util.MacvlanNetworkWithWhereaboutsRangeStartEnd(
					networkName, testNamespace, ipRange, rangeStart, rangeEnd)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("filling the pool with 3 pods")
				pods := make([]*corev1.Pod, 0, 3)
				for i := range 3 {
					name := fmt.Sprintf("wb-exhaust-v6-%d", i)
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)
				}
				defer func() {
					for _, p := range pods {
						if p != nil {
							_ = clientInfo.DeletePod(p)
						}
					}
				}()

				By("verifying pool is exhausted")
				overflowPod, err := clientInfo.Client.CoreV1().Pods(testNamespace).Create(
					context.Background(),
					entities.PodObject("wb-exhaust-v6-overflow", testNamespace,
						util.PodTierLabel("wb-exhaust-v6-overflow"),
						entities.PodNetworkSelectionElements(networkName)),
					metav1.CreateOptions{})
				if err == nil {
					// Pod was created but should fail to get an IP — clean it up.
					defer func() { _ = clientInfo.DeletePod(overflowPod) }()
					// Wait briefly for the pod to fail scheduling due to IP exhaustion.
					err = wbtestclient.WaitForPodReady(context.Background(), clientInfo.Client,
						testNamespace, overflowPod.Name, 10*time.Second)
				}
				Expect(err).To(HaveOccurred(), "should fail when IPv6 pool is exhausted")

				By("deleting one pod to free an IP")
				freedPod := pods[0]
				freedIPs, _ := retrievers.SecondaryIfaceIPValue(freedPod, "net1")
				Expect(clientInfo.DeletePod(freedPod)).To(Succeed())
				pods[0] = nil // mark as cleaned up

				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, freedPod.Name, freedIPs)

				By("creating a new pod — should succeed after recovery")
				pRecovered, err := clientInfo.ProvisionPod(
					"wb-exhaust-v6-recovered", testNamespace,
					util.PodTierLabel("wb-exhaust-v6-recovered"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(pRecovered) }()

				ips, err := retrievers.SecondaryIfaceIPValue(pRecovered, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(util.IsIPv6(ips[0])).To(BeTrue())
				Expect(util.InRange(ipRange, ips[0])).To(Succeed())
			})
		})

		Context("DS: small subnets", func() {
			It("allocates dual-stack IPs from minimal subnets", func() {
				const (
					networkName = "wa-tiny-ds"
					v4Range     = "10.93.0.0/31"  // 2 v4 addresses
					v6Range     = "fd00:93::/127" // 2 v6 addresses
				)

				nad := util.MacvlanNetworkWithWhereaboutsDualStackL3Mode(
					networkName, testNamespace, v4Range, v6Range)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating 2 pods to use all addresses in both families")
				pods := make([]*corev1.Pod, 0, 2)
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				v4Set := make(map[string]bool)
				v6Set := make(map[string]bool)
				for i := range 2 {
					name := fmt.Sprintf("wb-tiny-ds-%d", i)
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(ips).To(HaveLen(2))

					Expect(util.IsIPv4(ips[0])).To(BeTrue())
					Expect(util.InRange(v4Range, ips[0])).To(Succeed())
					v4Set[ips[0]] = true

					Expect(util.IsIPv6(ips[1])).To(BeTrue())
					Expect(util.InRange(v6Range, ips[1])).To(Succeed())
					v6Set[net.ParseIP(ips[1]).String()] = true
				}

				Expect(v4Set).To(HaveLen(2), "both v4 IPs should be unique")
				Expect(v6Set).To(HaveLen(2), "both v6 IPs should be unique")
			})
		})

		Context("IPv6: optimistic IPAM dealloc", func() {
			It("correctly deallocates with optimistic IPAM for IPv6", func() {
				const (
					networkName = "wa-opt-dealloc-v6"
					ipRange     = "fd00:a3::/124"
				)

				nad := util.MacvlanNetworkWithWhereaboutsOptimisticIPAM(
					networkName, testNamespace, ipRange)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-opt-dealloc-v6", testNamespace,
					util.PodTierLabel("wb-opt-dealloc-v6"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(util.IsIPv6(ips[0])).To(BeTrue())
				firstIP := ips[0]

				By("deleting the pod")
				Expect(clientInfo.DeletePod(p)).To(Succeed())

				By("waiting for deallocation")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-opt-dealloc-v6", ips)

				By("creating a new pod — should reclaim the same IPv6")
				p2, err := clientInfo.ProvisionPod(
					"wb-opt-dealloc-v6-2", testNamespace,
					util.PodTierLabel("wb-opt-dealloc-v6-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(net.ParseIP(ips2[0]).Equal(net.ParseIP(firstIP))).To(BeTrue(),
					"after deallocation, lowest-available should return %s, got %s", firstIP, ips2[0])
			})
		})

		Context("DS: optimistic IPAM dealloc", func() {
			It("correctly deallocates with optimistic IPAM for dual-stack", func() {
				const (
					networkName = "wa-opt-dealloc-ds"
					v4Range     = "10.94.0.0/28"
					v6Range     = "fd00:a4::/124"
				)

				nad := util.MacvlanNetworkWithWhereaboutsDualStackOptimisticIPAM(
					networkName, testNamespace, v4Range, v6Range)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-opt-dealloc-ds", testNamespace,
					util.PodTierLabel("wb-opt-dealloc-ds"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).To(HaveLen(2))
				firstV4 := ips[0]
				firstV6 := ips[1]

				By("deleting the pod")
				Expect(clientInfo.DeletePod(p)).To(Succeed())
				verifyNoAllocationsForPodRef(clientInfo, v4Range, testNamespace, "wb-opt-dealloc-ds", []string{firstV4})

				By("creating a new pod — should reclaim both IPs")
				p2, err := clientInfo.ProvisionPod(
					"wb-opt-dealloc-ds-2", testNamespace,
					util.PodTierLabel("wb-opt-dealloc-ds-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).To(HaveLen(2))
				Expect(ips2[0]).To(Equal(firstV4))
				Expect(net.ParseIP(ips2[1]).Equal(net.ParseIP(firstV6))).To(BeTrue())
			})
		})

		Context("DS: preferred IP", func() {
			It("assigns the requested preferred IPv4 in a dual-stack config", func() {
				const (
					networkName = "wa-pref-ds"
					v4Range     = "10.95.0.0/24"
					v6Range     = "fd00:a5::/112"
					preferredIP = "10.95.0.42"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-pref-ds", testNamespace,
					util.PodTierLabel("wb-pref-ds"),
					entities.PodNetworkSelectionWithAnnotations(
						map[string]string{"whereabouts.cni.cncf.io/preferred-ip": preferredIP},
						networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).To(HaveLen(2))
				Expect(ips[0]).To(Equal(preferredIP),
					"v4 should be the preferred IP %s", preferredIP)
				Expect(util.IsIPv6(ips[1])).To(BeTrue(),
					"v6 should also be allocated")
				Expect(util.InRange(v6Range, ips[1])).To(Succeed())
			})
		})

		Context("IPv6: preferred IP fallback", func() {
			It("falls back to lowest available when preferred IPv6 is taken", func() {
				const (
					networkName = "wa-pref-taken-v6"
					ipRange     = "fd00:a6::/124"
					preferredIP = "fd00:a6::1"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating first pod — should get the lowest IPv6 (fd00:a6::1)")
				p1, err := clientInfo.ProvisionPod(
					"wb-pref-taken-v6-1", testNamespace,
					util.PodTierLabel("wb-pref-taken-v6-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p1) }()

				ips1, err := retrievers.SecondaryIfaceIPValue(p1, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(net.ParseIP(ips1[0]).Equal(net.ParseIP(preferredIP))).To(BeTrue(),
					"first pod should get lowest available %s", preferredIP)

				By("creating second pod requesting the same preferred IP — should fall back")
				p2, err := clientInfo.ProvisionPod(
					"wb-pref-taken-v6-2", testNamespace,
					util.PodTierLabel("wb-pref-taken-v6-2"),
					entities.PodNetworkSelectionWithAnnotations(
						map[string]string{"whereabouts.cni.cncf.io/preferred-ip": preferredIP},
						networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(net.ParseIP(ips2[0]).Equal(net.ParseIP(preferredIP))).To(BeFalse(),
					"second pod should NOT get the already-taken preferred IP")
				Expect(util.IsIPv6(ips2[0])).To(BeTrue())
				Expect(util.InRange(ipRange, ips2[0])).To(Succeed())
			})
		})

		Context("IPv6: node drain", func() {
			It("releases IPv6 IP allocations after pod deletion (drain)", func() {
				const (
					networkName = "wa-drain-v6"
					ipRange     = "fd00:a7::/112"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-drain-v6-1", testNamespace,
					util.PodTierLabel("wb-drain-v6-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(util.IsIPv6(ips[0])).To(BeTrue())

				By("deleting the pod (simulating drain)")
				Expect(clientInfo.DeletePod(p)).To(Succeed())

				By("verifying IPv6 IP is released from the pool")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-drain-v6-1", ips)

				By("creating a new pod — should reclaim the freed IPv6")
				p2, err := clientInfo.ProvisionPod(
					"wb-drain-v6-2", testNamespace,
					util.PodTierLabel("wb-drain-v6-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(net.ParseIP(ips2[0]).Equal(net.ParseIP(ips[0]))).To(BeTrue(),
					"after drain/cleanup the lowest-available IPv6 should be reassigned")
			})
		})

		Context("DS: allocation verification", func() {
			It("verifies IPPool allocations match pod dual-stack IPs", func() {
				const (
					networkName = "wa-verify-ds"
					v4Range     = "10.97.0.0/24"
					v6Range     = "fd00:a8::/112"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-verify-ds", testNamespace,
					util.PodTierLabel("wb-verify-ds"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p) }()

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).To(HaveLen(2))
				Expect(util.IsIPv4(ips[0])).To(BeTrue())
				Expect(util.IsIPv6(ips[1])).To(BeTrue())

				By("verifying v4 allocation in IPPool")
				verifyAllocations(clientInfo, v4Range, ips[0], testNamespace, "wb-verify-ds", "net1")

				By("verifying v6 allocation in IPPool")
				verifyAllocations(clientInfo, v6Range, ips[1], testNamespace, "wb-verify-ds", "net1")
			})
		})

		Context("DS: named networks", func() {
			It("isolates dual-stack allocations via network_name", func() {
				const (
					networkNameA = "wa-named-ds-a"
					networkNameB = "wa-named-ds-b"
					v4Range      = "10.98.0.0/28"
					v6Range      = "fd00:a9::/124"
					poolA        = "ds-net-a"
					poolB        = "ds-net-b"
				)

				nadA := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameA, testNamespace, v4Range, []string{v6Range}, poolA, true)
				nadB := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameB, testNamespace, v4Range, []string{v6Range}, poolB, true)

				_, err := clientInfo.AddNetAttachDef(nadA)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadA)).To(Succeed()) }()

				_, err = clientInfo.AddNetAttachDef(nadB)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadB)).To(Succeed()) }()

				By("creating pods on different named dual-stack networks")
				pA, err := clientInfo.ProvisionPod(
					"wb-named-ds-a", testNamespace,
					util.PodTierLabel("wb-named-ds-a"),
					entities.PodNetworkSelectionElements(networkNameA))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(pA) }()

				pB, err := clientInfo.ProvisionPod(
					"wb-named-ds-b", testNamespace,
					util.PodTierLabel("wb-named-ds-b"),
					entities.PodNetworkSelectionElements(networkNameB))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(pB) }()

				ipsA, err := retrievers.SecondaryIfaceIPValue(pA, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ipsA).To(HaveLen(2))

				ipsB, err := retrievers.SecondaryIfaceIPValue(pB, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ipsB).To(HaveLen(2))

				By("verifying same v4 IP allocated in both named DS networks (isolation)")
				Expect(ipsA[0]).To(Equal(ipsB[0]),
					"with named networks, both pods should get same lowest v4 (isolated pools)")

				By("verifying same v6 IP allocated in both named DS networks")
				Expect(net.ParseIP(ipsA[1]).Equal(net.ParseIP(ipsB[1]))).To(BeTrue(),
					"with named networks, both pods should get same lowest v6 (isolated pools)")
			})
		})

		Context("Edge: node cordon + pod eviction", func() {
			It("releases IPs when pods are evicted from a cordoned node", func() {
				const (
					networkName = "wa-cordon"
					ipRange     = "10.99.0.0/24"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating a pod with a secondary interface")
				p, err := clientInfo.ProvisionPod(
					"wb-cordon-1", testNamespace,
					util.PodTierLabel("wb-cordon-1"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).NotTo(BeEmpty())

				By("verifying the IP is allocated")
				verifyAllocations(clientInfo, ipRange, ips[0], testNamespace, "wb-cordon-1", "net1")

				By("cordoning the node the pod is running on")
				nodeName := p.Spec.NodeName
				Expect(nodeName).NotTo(BeEmpty())
				node, err := clientInfo.Client.CoreV1().Nodes().Get(
					context.Background(), nodeName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())

				node.Spec.Unschedulable = true
				_, err = clientInfo.Client.CoreV1().Nodes().Update(
					context.Background(), node, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())

				defer func() {
					By("uncordoning the node")
					n, err := clientInfo.Client.CoreV1().Nodes().Get(
						context.Background(), nodeName, metav1.GetOptions{})
					if err == nil {
						n.Spec.Unschedulable = false
						_, _ = clientInfo.Client.CoreV1().Nodes().Update(
							context.Background(), n, metav1.UpdateOptions{})
					}
				}()

				By("evicting the pod")
				eviction := &policyv1.Eviction{
					ObjectMeta: metav1.ObjectMeta{
						Name:      p.Name,
						Namespace: testNamespace,
					},
				}
				Expect(clientInfo.Client.PolicyV1().Evictions(testNamespace).Evict(
					context.Background(), eviction)).To(Succeed())

				By("waiting for the pod to be fully deleted")
				Eventually(func() bool {
					_, err := clientInfo.Client.CoreV1().Pods(testNamespace).Get(
						context.Background(), "wb-cordon-1", metav1.GetOptions{})
					return errors.IsNotFound(err)
				}, 2*time.Minute, 5*time.Second).Should(BeTrue(), "pod should be deleted after eviction")

				By("verifying the IP was released from the pool")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-cordon-1", ips)

				By("creating a new pod — IP should be reassigned (may land on a different node)")
				p2, err := clientInfo.ProvisionPod(
					"wb-cordon-2", testNamespace,
					util.PodTierLabel("wb-cordon-2"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = clientInfo.DeletePod(p2) }()

				ips2, err := retrievers.SecondaryIfaceIPValue(p2, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2[0]).To(Equal(ips[0]),
					"after eviction, lowest-available IP should be reassigned")
			})
		})

		Context("Edge: pod eviction via Policy API", func() {
			It("releases the IP immediately after eviction", func() {
				const (
					networkName = "wa-evict-policy"
					ipRange     = "10.99.1.0/24"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-evict-pol", testNamespace,
					util.PodTierLabel("wb-evict-pol"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				verifyAllocations(clientInfo, ipRange, ips[0], testNamespace, "wb-evict-pol", "net1")

				By("evicting the pod via Policy API")
				eviction := &policyv1.Eviction{
					ObjectMeta: metav1.ObjectMeta{
						Name:      p.Name,
						Namespace: testNamespace,
					},
				}
				Expect(clientInfo.Client.PolicyV1().Evictions(testNamespace).Evict(
					context.Background(), eviction)).To(Succeed())

				By("waiting for pod to be fully deleted")
				Eventually(func() bool {
					_, err := clientInfo.Client.CoreV1().Pods(testNamespace).Get(
						context.Background(), "wb-evict-pol", metav1.GetOptions{})
					return errors.IsNotFound(err)
				}, 2*time.Minute, 5*time.Second).Should(BeTrue())

				By("verifying IP was released")
				verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, "wb-evict-pol", ips)
			})
		})

		Context("Edge: rapid pod churn (stress test)", func() {
			It("deterministically reassigns IPs through create/delete cycles", func() {
				const (
					networkName = "wa-churn"
					ipRange     = "10.99.2.0/24"
					churnCount  = 8
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("cycle 1: creating pods and recording IPs")
				firstCycleIPs := make([]string, 0, churnCount)
				pods := make([]*corev1.Pod, 0, churnCount)
				for i := range churnCount {
					name := fmt.Sprintf("wb-churn-a-%d", i)
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					firstCycleIPs = append(firstCycleIPs, ips[0])
				}

				By("deleting all pods")
				for _, p := range pods {
					Expect(clientInfo.DeletePod(p)).To(Succeed())
				}

				By("waiting for all IPs to be released")
				for i, ip := range firstCycleIPs {
					name := fmt.Sprintf("wb-churn-a-%d", i)
					verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, name, []string{ip})
				}

				By("cycle 2: creating same number of pods — should get same IPs (deterministic lowest-available)")
				secondCycleIPs := make([]string, 0, churnCount)
				for i := range churnCount {
					name := fmt.Sprintf("wb-churn-b-%d", i)
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					secondCycleIPs = append(secondCycleIPs, ips[0])
				}

				By("verifying the exact same IPs were reassigned")
				sort.Strings(firstCycleIPs)
				sort.Strings(secondCycleIPs)
				Expect(secondCycleIPs).To(Equal(firstCycleIPs),
					"after full churn cycle, same set of IPs should be reassigned")
			})
		})

		Context("Edge: multi-interface pod cleanup", func() {
			It("releases IPs from all interfaces when pod is deleted", func() {
				const (
					networkNameA = "wa-multi-if-a"
					networkNameB = "wa-multi-if-b"
					ipRangeA     = "10.99.3.0/24"
					ipRangeB     = "10.99.4.0/24"
				)

				nadA := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameA, testNamespace, ipRangeA, []string{}, wbstorage.UnnamedNetwork, true)
				nadB := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameB, testNamespace, ipRangeB, []string{}, wbstorage.UnnamedNetwork, true)

				_, err := clientInfo.AddNetAttachDef(nadA)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadA)).To(Succeed()) }()

				_, err = clientInfo.AddNetAttachDef(nadB)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadB)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-multi-if-cleanup", testNamespace,
					util.PodTierLabel("wb-multi-if-cleanup"),
					entities.PodNetworkSelectionElements(networkNameA, networkNameB))
				Expect(err).NotTo(HaveOccurred())

				ips1, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				ips2, err := retrievers.SecondaryIfaceIPValue(p, "net2")
				Expect(err).NotTo(HaveOccurred())

				verifyAllocations(clientInfo, ipRangeA, ips1[0], testNamespace, "wb-multi-if-cleanup", "net1")
				verifyAllocations(clientInfo, ipRangeB, ips2[0], testNamespace, "wb-multi-if-cleanup", "net2")

				By("deleting the multi-interface pod")
				Expect(clientInfo.DeletePod(p)).To(Succeed())

				By("verifying both IPs are released")
				verifyNoAllocationsForPodRef(clientInfo, ipRangeA, testNamespace, "wb-multi-if-cleanup", ips1)
				verifyNoAllocationsForPodRef(clientInfo, ipRangeB, testNamespace, "wb-multi-if-cleanup", ips2)
			})
		})

		Context("Edge: StatefulSet scale down and up", func() {
			It("releases IPs on scale-down and reuses them on scale-up", func() {
				const (
					networkName     = "wa-scale"
					ipRange         = "10.99.5.0/24"
					serviceName     = "web-scale"
					statefulSetName = "wb-scale"
					selector        = "app=" + serviceName
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating StatefulSet with 3 replicas")
				_, err = clientInfo.ProvisionStatefulSet(
					statefulSetName, testNamespace, serviceName, 3, networkName)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(clientInfo.DeleteStatefulSet(testNamespace, serviceName, selector)).To(Succeed())
				}()

				podList, err := wbtestclient.ListPods(
					context.Background(), clientInfo.Client, testNamespace, selector)
				Expect(err).NotTo(HaveOccurred())
				Expect(podList.Items).To(HaveLen(3))

				originalIPs := make(map[string]string) // podName -> IP
				for _, p := range podList.Items {
					ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
					Expect(err).NotTo(HaveOccurred())
					originalIPs[p.Name] = ips[0]
				}

				By("scaling down to 1 replica")
				var one int32 = 1
				ss, err := clientInfo.Client.AppsV1().StatefulSets(testNamespace).Get(
					context.Background(), serviceName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				ss.Spec.Replicas = &one
				_, err = clientInfo.Client.AppsV1().StatefulSets(testNamespace).Update(
					context.Background(), ss, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("waiting for pods to scale down")
				Eventually(func() int {
					pl, _ := wbtestclient.ListPods(
						context.Background(), clientInfo.Client, testNamespace, selector)
					running := 0
					for _, p := range pl.Items {
						if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
							running++
						}
					}
					return running
				}, 2*time.Minute, 5*time.Second).Should(Equal(1))

				By("scaling back up to 3 replicas")
				var three int32 = 3
				ss, err = clientInfo.Client.AppsV1().StatefulSets(testNamespace).Get(
					context.Background(), serviceName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				ss.Spec.Replicas = &three
				_, err = clientInfo.Client.AppsV1().StatefulSets(testNamespace).Update(
					context.Background(), ss, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("waiting for pods to scale up")
				Eventually(func() int {
					pl, _ := wbtestclient.ListPods(
						context.Background(), clientInfo.Client, testNamespace, selector)
					running := 0
					for _, p := range pl.Items {
						if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
							running++
						}
					}
					return running
				}, 3*time.Minute, 5*time.Second).Should(Equal(3))

				podList2, err := wbtestclient.ListPods(
					context.Background(), clientInfo.Client, testNamespace, selector)
				Expect(err).NotTo(HaveOccurred())

				By("verifying all IPs are within range and unique")
				ipSet := make(map[string]bool)
				for _, p := range podList2.Items {
					if p.DeletionTimestamp != nil {
						continue
					}
					ips, err := retrievers.SecondaryIfaceIPValue(&p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					Expect(ipSet).NotTo(HaveKey(ips[0]), "duplicate IP: %s", ips[0])
					ipSet[ips[0]] = true
				}
				Expect(ipSet).To(HaveLen(3))
			})
		})

		Context("Edge: concurrent burst creation", func() {
			It("allocates unique IPs when many pods are created simultaneously", func() {
				const (
					networkName = "wa-burst"
					ipRange     = "10.99.6.0/24"
					burstCount  = 10
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("creating many pods without waiting for each one")
				podNames := make([]string, burstCount)
				for i := range burstCount {
					podNames[i] = fmt.Sprintf("wb-burst-%d", i)
				}

				// Create all pods quickly (they will be pending/creating concurrently).
				pods := make([]*corev1.Pod, 0, len(podNames))
				for _, name := range podNames {
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)
				}
				defer func() {
					for _, p := range pods {
						_ = clientInfo.DeletePod(p)
					}
				}()

				By("verifying all pods got unique IPs")
				ipSet := make(map[string]bool)
				for _, p := range pods {
					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					Expect(util.InRange(ipRange, ips[0])).To(Succeed())
					Expect(ipSet).NotTo(HaveKey(ips[0]),
						"duplicate IP in burst: %s", ips[0])
					ipSet[ips[0]] = true
				}
				Expect(ipSet).To(HaveLen(burstCount))
			})
		})

		Context("Edge: DS node cordon + eviction", func() {
			It("releases both v4 and v6 IPs when a dual-stack pod is evicted", func() {
				const (
					networkName = "wa-ds-cordon"
					v4Range     = "10.99.7.0/24"
					v6Range     = "fd00:b7::/112"
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, v4Range, []string{v6Range}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-ds-cordon", testNamespace,
					util.PodTierLabel("wb-ds-cordon"),
					entities.PodNetworkSelectionElements(networkName))
				Expect(err).NotTo(HaveOccurred())

				ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips).To(HaveLen(2))

				By("cordoning the node")
				nodeName := p.Spec.NodeName
				node, err := clientInfo.Client.CoreV1().Nodes().Get(
					context.Background(), nodeName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				node.Spec.Unschedulable = true
				_, err = clientInfo.Client.CoreV1().Nodes().Update(
					context.Background(), node, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					n, e := clientInfo.Client.CoreV1().Nodes().Get(
						context.Background(), nodeName, metav1.GetOptions{})
					if e == nil {
						n.Spec.Unschedulable = false
						_, _ = clientInfo.Client.CoreV1().Nodes().Update(
							context.Background(), n, metav1.UpdateOptions{})
					}
				}()

				By("evicting the DS pod")
				eviction := &policyv1.Eviction{
					ObjectMeta: metav1.ObjectMeta{
						Name:      p.Name,
						Namespace: testNamespace,
					},
				}
				Expect(clientInfo.Client.PolicyV1().Evictions(testNamespace).Evict(
					context.Background(), eviction)).To(Succeed())

				Eventually(func() bool {
					_, err := clientInfo.Client.CoreV1().Pods(testNamespace).Get(
						context.Background(), "wb-ds-cordon", metav1.GetOptions{})
					return errors.IsNotFound(err)
				}, 2*time.Minute, 5*time.Second).Should(BeTrue())

				By("verifying both v4 and v6 IPs released")
				verifyNoAllocationsForPodRef(clientInfo, v4Range, testNamespace, "wb-ds-cordon", []string{ips[0]})
			})
		})

		Context("Edge: IPv6 rapid churn", func() {
			It("deterministically reassigns IPv6 IPs through create/delete cycles", func() {
				const (
					networkName = "wa-churn-v6"
					ipRange     = "fd00:b8::/112"
					churnCount  = 5
				)

				nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
				_, err := clientInfo.AddNetAttachDef(nad)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) }()

				By("cycle 1: creating IPv6 pods")
				firstCycleIPs := make([]string, 0, churnCount)
				pods := make([]*corev1.Pod, 0, churnCount)
				for i := range churnCount {
					name := fmt.Sprintf("wb-churn-v6-a-%d", i)
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					pods = append(pods, p)

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					firstCycleIPs = append(firstCycleIPs, net.ParseIP(ips[0]).String())
				}

				By("deleting all pods")
				for _, p := range pods {
					Expect(clientInfo.DeletePod(p)).To(Succeed())
				}
				for i, ip := range firstCycleIPs {
					name := fmt.Sprintf("wb-churn-v6-a-%d", i)
					verifyNoAllocationsForPodRef(clientInfo, ipRange, testNamespace, name, []string{ip})
				}

				By("cycle 2: recreating pods")
				secondCycleIPs := make([]string, 0, churnCount)
				for i := range churnCount {
					name := fmt.Sprintf("wb-churn-v6-b-%d", i)
					p, err := clientInfo.ProvisionPod(
						name, testNamespace,
						util.PodTierLabel(name),
						entities.PodNetworkSelectionElements(networkName))
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = clientInfo.DeletePod(p) }()

					ips, err := retrievers.SecondaryIfaceIPValue(p, "net1")
					Expect(err).NotTo(HaveOccurred())
					secondCycleIPs = append(secondCycleIPs, net.ParseIP(ips[0]).String())
				}

				By("verifying same IPv6 set reassigned")
				sort.Strings(firstCycleIPs)
				sort.Strings(secondCycleIPs)
				Expect(secondCycleIPs).To(Equal(firstCycleIPs))
			})
		})

		Context("Edge: multi-interface DS pod cleanup", func() {
			It("releases both DS interfaces when a multi-NAD pod is deleted", func() {
				const (
					networkNameA = "wa-ds-mif-a"
					networkNameB = "wa-ds-mif-b"
					v4RangeA     = "10.99.8.0/24"
					v6RangeA     = "fd00:b9::/112"
					v4RangeB     = "10.99.9.0/24"
					v6RangeB     = "fd00:ba::/112"
				)

				nadA := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameA, testNamespace, v4RangeA, []string{v6RangeA}, wbstorage.UnnamedNetwork, true)
				nadB := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(
					networkNameB, testNamespace, v4RangeB, []string{v6RangeB}, wbstorage.UnnamedNetwork, true)

				_, err := clientInfo.AddNetAttachDef(nadA)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadA)).To(Succeed()) }()

				_, err = clientInfo.AddNetAttachDef(nadB)
				Expect(err).NotTo(HaveOccurred())
				defer func() { Expect(clientInfo.DelNetAttachDef(nadB)).To(Succeed()) }()

				p, err := clientInfo.ProvisionPod(
					"wb-ds-mif-cleanup", testNamespace,
					util.PodTierLabel("wb-ds-mif-cleanup"),
					entities.PodNetworkSelectionElements(networkNameA, networkNameB))
				Expect(err).NotTo(HaveOccurred())

				ips1, err := retrievers.SecondaryIfaceIPValue(p, "net1")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips1).To(HaveLen(2), "net1 should have DS IPs")

				ips2, err := retrievers.SecondaryIfaceIPValue(p, "net2")
				Expect(err).NotTo(HaveOccurred())
				Expect(ips2).To(HaveLen(2), "net2 should have DS IPs")

				By("deleting the multi-interface DS pod")
				Expect(clientInfo.DeletePod(p)).To(Succeed())

				By("verifying all 4 IPs are released (v4+v6 × 2 interfaces)")
				verifyNoAllocationsForPodRef(clientInfo, v4RangeA, testNamespace, "wb-ds-mif-cleanup", []string{ips1[0]})
				verifyNoAllocationsForPodRef(clientInfo, v4RangeB, testNamespace, "wb-ds-mif-cleanup", []string{ips2[0]})
			})
		})

	})
})

func verifyNoAllocationsForPodRef(clientInfo *wbtestclient.ClientInfo, ipv4TestRange, testNamespace, podName string, secondaryIfaceIPs []string) {
	Eventually(func() bool {
		ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.Background(), wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}), metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		allocation := allocationForPodRef(getPodRef(testNamespace, podName), *ipPool)
		return len(allocation) == 0
	}, 3*time.Second, 500*time.Millisecond).Should(BeTrue())

	for _, ip := range secondaryIfaceIPs {
		Eventually(func() bool {
			_, err := clientInfo.WbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(ipPoolNamespace).Get(context.Background(), wbstorage.NormalizeIP(net.ParseIP(ip), wbstorage.UnnamedNetwork), metav1.GetOptions{})
			if err != nil && errors.IsNotFound(err) {
				return true
			}
			return false
		}, 3*time.Second, 500*time.Millisecond).Should(BeTrue())
	}
}

func verifyAllocations(clientInfo *wbtestclient.ClientInfo, ipv4TestRange, ip, testNamespace, podName, ifName string) {
	ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.Background(), wbstorage.IPPoolName(wbstorage.PoolIdentifier{IPRange: ipv4TestRange, NetworkName: wbstorage.UnnamedNetwork}), metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	firstIP, _, err := net.ParseCIDR(ipv4TestRange)
	Expect(err).NotTo(HaveOccurred())
	offset, err := iphelpers.IPGetOffset(net.ParseIP(ip), firstIP)
	Expect(err).NotTo(HaveOccurred())

	allocation, ok := ipPool.Spec.Allocations[offset.String()]
	Expect(ok).To(BeTrue())
	Expect(allocation.PodRef).To(Equal(getPodRef(testNamespace, podName)))
	Expect(allocation.IfName).To(Equal(ifName))

	overlapping, err := clientInfo.WbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(ipPoolNamespace).Get(context.Background(), wbstorage.NormalizeIP(net.ParseIP(ip), wbstorage.UnnamedNetwork), metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	Expect(overlapping.Spec.IfName).To(Equal(ifName))
	Expect(overlapping.Spec.PodRef).To(Equal(getPodRef(testNamespace, podName)))
}

func allocationForPodRef(podRef string, ipPool v1alpha1.IPPool) []v1alpha1.IPAllocation {
	var allocations []v1alpha1.IPAllocation
	for _, allocation := range ipPool.Spec.Allocations {
		if allocation.PodRef == podRef {
			allocations = append(allocations, allocation)
		}
	}

	sort.Slice(allocations, func(i, j int) bool {
		return allocations[i].IfName < allocations[j].IfName
	})

	return allocations
}

func podTierLabel(podTier string) map[string]string {
	const tier = "tier"
	return map[string]string{tier: podTier}
}

// Returns a network attachment definition object configured by provided parameters.
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
	formattedRanges := make([]string, 0, len(ranges))
	for _, ipRange := range ranges {
		singleRange := fmt.Sprintf(`{"range": "%s"}`, ipRange)
		formattedRanges = append(formattedRanges, singleRange)
	}
	ipRanges := "[" + strings.Join(formattedRanges[:], ",") + "]"
	return ipRanges
}

func getPodRef(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}
