package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	multusv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sclient "k8s.io/client-go/kubernetes"
	fakek8sclient "k8s.io/client-go/kubernetes/fake"

	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	fakewbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned/fake"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
)

func TestIPReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconcile IP address allocation in the system")
}

var _ = Describe("Whereabouts IP reconciler", func() {
	const (
		firstIPInRange = "10.10.10.1"
		ipRange        = "10.10.10.0/16"
		namespace      = "default"
		networkName    = "net1"
		podName        = "pod1"
		timeout        = 10
	)

	var (
		reconcileLooper *ReconcileLooper
		k8sClientSet    k8sclient.Interface
	)

	Context("reconciling IP pools with a single running pod", func() {
		var pod *v1.Pod

		BeforeEach(func() {
			pod = generatePod(namespace, podName, ipInNetwork{ip: firstIPInRange, networkName: networkName})
			k8sClientSet = fakek8sclient.NewSimpleClientset(pod)
		})

		Context("with IP from a single IPPool", func() {
			const poolName = "pool1"

			var (
				pool     *v1alpha1.IPPool
				wbClient wbclient.Interface
			)

			BeforeEach(func() {
				pool = generateIPPoolSpec(ipRange, namespace, poolName, pod.Name)
				wbClient = fakewbclient.NewSimpleClientset(pool)
			})

			Context("the pod dies", func() {
				BeforeEach(func() {
					Expect(k8sClientSet.CoreV1().Pods(namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})).NotTo(HaveOccurred())
				})

				Context("reconciling the IPPool", func() {
					BeforeEach(func() {
						var err error
						reconcileLooper, err = NewReconcileLooperWithClient(context.TODO(), kubernetes.NewKubernetesClient(wbClient, k8sClientSet, timeout), timeout)
						Expect(err).NotTo(HaveOccurred())
					})

					It("should report the deleted IP reservation", func() {
						Expect(reconcileLooper.ReconcileIPPools(context.TODO())).To(Equal([]net.IP{net.ParseIP("10.10.10.1")}))
					})

					It("the pool's orphaned IP should be deleted after the reconcile loop", func() {
						_, err := reconcileLooper.ReconcileIPPools(context.TODO())
						Expect(err).NotTo(HaveOccurred())
						poolAfterCleanup, err := wbClient.WhereaboutsV1alpha1().IPPools(namespace).Get(context.TODO(), pool.GetName(), metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						Expect(poolAfterCleanup.Spec.Allocations).To(BeEmpty())
					})
				})
			})
		})
	})

	Context("reconciling an IP pool with multiple pods attached", func() {
		const (
			livePodIndex    = 1
			numberOfPods    = 2
			secondIPInRange = "10.10.10.2"
		)

		var pods []v1.Pod

		BeforeEach(func() {
			pods = nil
			ips := []string{firstIPInRange, secondIPInRange}
			for i := 0; i < numberOfPods; i++ {
				pod := generatePod(namespace, fmt.Sprintf("pod%d", i+1), ipInNetwork{
					ip:          ips[i],
					networkName: networkName,
				})
				if i == livePodIndex {
					k8sClientSet = fakek8sclient.NewSimpleClientset(pod)
				}
				pods = append(pods, *pod)
			}
		})

		Context("each with IP from the same IPPool", func() {
			const poolName = "pool1"

			var (
				pool     *v1alpha1.IPPool
				wbClient wbclient.Interface
			)

			BeforeEach(func() {
				var podNames []string
				for _, pod := range pods {
					podNames = append(podNames, pod.Name)
				}
				pool = generateIPPoolSpec(ipRange, namespace, poolName, podNames...)
				wbClient = fakewbclient.NewSimpleClientset(pool)
			})

			Context("reconciling the IPPool", func() {
				BeforeEach(func() {
					var err error
					reconcileLooper, err = NewReconcileLooperWithClient(context.TODO(), kubernetes.NewKubernetesClient(wbClient, k8sClientSet, timeout), timeout)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should report the dead pod's IP address as deleted", func() {
					deletedIPAddrs, err := reconcileLooper.ReconcileIPPools(context.TODO())
					Expect(err).NotTo(HaveOccurred())
					Expect(deletedIPAddrs).To(Equal([]net.IP{net.ParseIP("10.10.10.1")}))
				})

				It("the IPPool should have only the IP reservation of the live pod", func() {
					deletedIPAddrs, err := reconcileLooper.ReconcileIPPools(context.TODO())
					Expect(err).NotTo(HaveOccurred())
					Expect(deletedIPAddrs).NotTo(BeEmpty())

					poolAfterCleanup, err := wbClient.WhereaboutsV1alpha1().IPPools(namespace).Get(context.TODO(), pool.GetName(), metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					remainingAllocation := map[string]v1alpha1.IPAllocation{
						"2": {
							PodRef: fmt.Sprintf("%s/%s", namespace, pods[livePodIndex].Name),
						},
					}
					Expect(poolAfterCleanup.Spec.Allocations).To(Equal(remainingAllocation))
				})
			})
		})
	})

	Context("reconciling cluster wide IPs - overlapping IPs", func() {
		const (
			numberOfPods       = 3
			firstNetworkName   = "network1"
			firstNetworkRange  = "10.10.10.0/16"
			firstPoolName      = "pool1"
			podIndexToRemove   = 0
			secondIPInRange    = "10.10.10.2"
			secondNetworkName  = "network2"
			secondNetworkRange = "10.10.10.0/24" // overlaps w/ firstNetworkRange
			secondPoolName     = "pool2"
			thirdIPInRange     = "10.10.10.3"
		)

		var (
			pods           []*v1.Pod
			pools          []v1alpha1.IPPool
			clusterWideIPs []v1alpha1.OverlappingRangeIPReservation
			wbClient       wbclient.Interface
		)

		wrapToRuntimeObject := func(pods ...*v1.Pod) []runtime.Object {
			var runtimeObjects []runtime.Object
			for _, pod := range pods {
				runtimeObjects = append(runtimeObjects, pod)
			}
			return runtimeObjects
		}

		BeforeEach(func() {
			ips := []string{firstIPInRange, secondIPInRange, thirdIPInRange}
			networks := []string{firstNetworkName, secondNetworkName}
			for i := 0; i < numberOfPods; i++ {
				pod := generatePod(namespace, fmt.Sprintf("pod%d", i+1), ipInNetwork{
					ip:          ips[i],
					networkName: networks[i%2], // pod1 and pod3 connected to network1; pod2 connected to network2
				})
				pods = append(pods, pod)
			}
			k8sClientSet = fakek8sclient.NewSimpleClientset(wrapToRuntimeObject(pods...)...)
		})

		BeforeEach(func() {
			firstPool := generateIPPoolSpec(firstNetworkRange, namespace, firstPoolName, pods[0].GetName(), pods[2].GetName())
			secondPool := generateIPPoolSpec(secondNetworkRange, namespace, secondPoolName, pods[1].GetName())
			wbClient = fakewbclient.NewSimpleClientset(firstPool, secondPool)
			pools = append(pools, *firstPool, *secondPool)
		})

		BeforeEach(func() {
			podIPs := []string{firstIPInRange, secondIPInRange, thirdIPInRange}
			for i := 0; i < numberOfPods; i++ {
				var clusterWideIP v1alpha1.OverlappingRangeIPReservation
				ownerPodRef := fmt.Sprintf("%s/%s", namespace, pods[i].GetName())
				_, err := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(namespace).Create(context.TODO(), generateClusterWideIPReservation(namespace, podIPs[i], ownerPodRef), metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				clusterWideIPs = append(clusterWideIPs, clusterWideIP)
			}
		})

		It("will delete an orphaned IP address", func() {
			Expect(k8sClientSet.CoreV1().Pods(namespace).Delete(context.TODO(), pods[podIndexToRemove].Name, metav1.DeleteOptions{})).NotTo(HaveOccurred())
			newReconciler, err := NewReconcileLooperWithClient(context.TODO(), kubernetes.NewKubernetesClient(wbClient, k8sClientSet, timeout), timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(newReconciler.ReconcileOverlappingIPAddresses(context.TODO())).To(Succeed())

			expectedClusterWideIPs := 2
			clusterWideIPAllocations, err := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(namespace).List(context.TODO(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterWideIPAllocations.Items).To(HaveLen(expectedClusterWideIPs))
		})
	})

	Context("a pod in pending state, without an IP in its network-status", func() {
		const poolName = "pool1"

		var (
			pod      *v1.Pod
			pool     *v1alpha1.IPPool
			wbClient wbclient.Interface
		)

		BeforeEach(func() {
			var err error
			pod, err = k8sClientSet.CoreV1().Pods(namespace).Create(
				context.TODO(),
				generatePendingPod(namespace, podName),
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			pool = generateIPPoolSpec(ipRange, namespace, poolName, pod.Name)
			wbClient = fakewbclient.NewSimpleClientset(pool)
			reconcileLooper, err = NewReconcileLooperWithClient(context.TODO(), kubernetes.NewKubernetesClient(wbClient, k8sClientSet, timeout), timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		It("cannot be reconciled", func() {
			Expect(reconcileLooper.ReconcileIPPools(context.TODO())).To(BeEmpty())
		})
	})
})

// mock the pool
type dummyPool struct {
	orphans []types.IPReservation
	pool    v1alpha1.IPPool
}

func (dp dummyPool) Allocations() []types.IPReservation {
	return dp.orphans
}

func (dp dummyPool) Update(context.Context, []types.IPReservation) error {
	return nil
}

var _ = Describe("IPReconciler", func() {
	var ipReconciler *ReconcileLooper

	newIPReconciler := func(orphanedIPs ...OrphanedIPReservations) *ReconcileLooper {
		reconciler := &ReconcileLooper{
			orphanedIPs: orphanedIPs,
		}

		return reconciler
	}

	When("there are no IP addresses to reconcile", func() {
		BeforeEach(func() {
			ipReconciler = newIPReconciler()
		})

		It("does not delete anything", func() {
			reconciledIPs, err := ipReconciler.ReconcileIPPools(context.TODO())
			Expect(err).NotTo(HaveOccurred())
			Expect(reconciledIPs).To(BeEmpty())
		})
	})

	When("there are IP addresses to reconcile", func() {
		const (
			firstIPInRange = "192.168.14.1"
			ipCIDR         = "192.168.14.0/24"
			namespace      = "default"
			podName        = "pod1"
		)

		BeforeEach(func() {
			podRef := "default/pod1"
			reservations := generateIPReservation(firstIPInRange, podRef)

			pool := generateIPPool(ipCIDR, podRef)
			orphanedIPAddr := OrphanedIPReservations{
				Pool:        dummyPool{orphans: reservations, pool: pool},
				Allocations: reservations,
			}

			ipReconciler = newIPReconciler(orphanedIPAddr)
		})

		It("does delete the orphaned IP address", func() {
			reconciledIPs, err := ipReconciler.ReconcileIPPools(context.TODO())
			Expect(err).NotTo(HaveOccurred())
			Expect(reconciledIPs).To(Equal([]net.IP{net.ParseIP(firstIPInRange)}))
		})

		Context("and they are actually multiple IPs", func() {
			BeforeEach(func() {
				podRef := "default/pod2"
				reservations := generateIPReservation("192.168.14.2", podRef)

				pool := generateIPPool(ipCIDR, podRef, "default/pod2", "default/pod3")
				orphanedIPAddr := OrphanedIPReservations{
					Pool:        dummyPool{orphans: reservations, pool: pool},
					Allocations: reservations,
				}

				ipReconciler = newIPReconciler(orphanedIPAddr)
			})

			It("does delete *only the orphaned* the IP address", func() {
				reconciledIPs, err := ipReconciler.ReconcileIPPools(context.TODO())
				Expect(err).NotTo(HaveOccurred())
				Expect(reconciledIPs).To(ConsistOf([]net.IP{net.ParseIP("192.168.14.2")}))
			})
		})

		Context("but the IP reservation owner does not match", func() {
			var reservationPodRef string
			BeforeEach(func() {
				reservationPodRef = "default/pod2"
				podRef := "default/pod1"
				reservations := generateIPReservation(firstIPInRange, podRef)
				erroredReservations := generateIPReservation(firstIPInRange, reservationPodRef)

				pool := generateIPPool(ipCIDR, podRef)
				orphanedIPAddr := OrphanedIPReservations{
					Pool:        dummyPool{orphans: reservations, pool: pool},
					Allocations: erroredReservations,
				}

				ipReconciler = newIPReconciler(orphanedIPAddr)
			})

			It("errors when attempting to clean up the IP address", func() {
				reconciledIPs, err := ipReconciler.ReconcileIPPools(context.TODO())
				Expect(err).To(MatchError(fmt.Sprintf("did not find reserved IP for container %s", reservationPodRef)))
				Expect(reconciledIPs).To(BeEmpty())
			})
		})
	})
})

func generateIPPoolSpec(ipRange string, namespace string, poolName string, podNames ...string) *v1alpha1.IPPool {
	allocations := map[string]v1alpha1.IPAllocation{}
	for i, podName := range podNames {
		allocations[fmt.Sprintf("%d", i+1)] = v1alpha1.IPAllocation{
			PodRef: fmt.Sprintf("%s/%s", namespace, podName),
		}
	}
	return &v1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: poolName, ResourceVersion: "1"},
		Spec: v1alpha1.IPPoolSpec{
			Range:       ipRange,
			Allocations: allocations,
		},
	}
}

func generateClusterWideIPReservation(namespace string, ip string, ownerPodRef string) *v1alpha1.OverlappingRangeIPReservation {
	return &v1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: ip},
		Spec: v1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: ownerPodRef,
		},
	}
}

type ipInNetwork struct {
	ip          string
	networkName string
}

func generatePod(namespace string, podName string, ipNetworks ...ipInNetwork) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   namespace,
			Annotations: generatePodAnnotations(ipNetworks...),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    podName,
					Image:   "alpine",
					Command: []string{"/bin/bash", "-c", "sleep 2000000000000"},
				},
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
}

func generatePendingPod(namespace string, podName string, ipNetworks ...ipInNetwork) *v1.Pod {
	pod := generatePod(namespace, podName, ipNetworks...)
	pod.Status.Phase = v1.PodPending
	return pod
}

func generatePodAnnotations(ipNetworks ...ipInNetwork) map[string]string {
	var networks []string
	for _, ipNetworkInfo := range ipNetworks {
		networks = append(networks, ipNetworkInfo.networkName)
	}
	networkAnnotations := map[string]string{
		multusv1.NetworkAttachmentAnnot: strings.Join(networks, ","),
		multusv1.NetworkStatusAnnot:     generatePodNetworkStatusAnnotation(ipNetworks...),
	}
	return networkAnnotations
}

func generatePodNetworkStatusAnnotation(ipNetworks ...ipInNetwork) string {
	var networkStatus []multusv1.NetworkStatus
	for i, ipNetworkInfo := range ipNetworks {
		networkStatus = append(networkStatus, multusv1.NetworkStatus{
			Name:      ipNetworkInfo.networkName,
			Interface: fmt.Sprintf("net%d", i+1),
			IPs:       []string{ipNetworkInfo.ip},
		})
	}
	networkStatusStr, err := json.Marshal(networkStatus)
	if err != nil {
		return ""
	}

	return string(networkStatusStr)
}

func generateIPPool(cidr string, podRefs ...string) v1alpha1.IPPool {
	allocations := map[string]v1alpha1.IPAllocation{}
	for i, podRef := range podRefs {
		allocations[fmt.Sprintf("%d", i)] = v1alpha1.IPAllocation{PodRef: podRef}
	}

	return v1alpha1.IPPool{
		Spec: v1alpha1.IPPoolSpec{
			Range:       cidr,
			Allocations: allocations,
		},
	}
}

func generateIPReservation(ip string, podRef string) []types.IPReservation {
	return []types.IPReservation{
		{
			IP:     net.ParseIP(ip),
			PodRef: podRef,
		},
	}
}
