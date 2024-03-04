package reconciler

import (
	"context"
	"fmt"
	"regexp"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	fakek8sclient "k8s.io/client-go/kubernetes/fake"

	v1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"

	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	fakewbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned/fake"
)

var _ = Describe("MigrationPodRef", func() {
	const (
		firstIPInRange = "10.10.10.1"
		ipRange        = "10.10.10.0/16"
		namespace      = "default"
		networkName    = "net1"
		podName        = "pod1"
		timeout        = 10
		poolName       = "pool1"
	)

	var (
		reconcileLooper *ReconcileLooper
		k8sClientSet    k8sclient.Interface
		pod             *v1.Pod
	)

	Context("When migration is needed", func() {
		var (
			pool     *v1alpha1.IPPool
			wbClient wbclient.Interface
			ctx      context.Context
		)

		BeforeEach(func() {
			ctx = context.TODO()
			pod = generatePod(namespace, podName, ipInNetwork{ip: firstIPInRange, networkName: networkName})
			k8sClientSet = fakek8sclient.NewSimpleClientset(pod)
			pool = generateLegacyIPPoolSpec(ipRange, namespace, poolName, pod)
			wbClient = fakewbclient.NewSimpleClientset(pool)

			ownerPodRef := ComposePodRef(*pod)
			_, err := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(namespace).Create(context.TODO(), generateClusterWideIPReservation(namespace, firstIPInRange, ownerPodRef), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			reconcileLooper, err = NewReconcileLooperWithClient(context.TODO(), kubernetes.NewKubernetesClient(wbClient, k8sClientSet, timeout), timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should update PodRef for overlappingrangeipreservations and pools", func() {
			ipPools, err := reconcileLooper.k8sClient.ListIPPools(ctx)
			Expect(err).NotTo(HaveOccurred())
			err = reconcileLooper.migrationPodRef(ctx, ipPools)
			Expect(err).NotTo(HaveOccurred())

			found := false
			pattern := `^([^\s/]+)/([^\s/]+):([^\s/]+)$`
			// Allippools shall have been updated
			ipPools, err = reconcileLooper.k8sClient.ListIPPools(ctx)
			Expect(err).NotTo(HaveOccurred())
			for _, pool := range ipPools {
				for _, r := range pool.Allocations() {
					if !regexp.MustCompile(pattern).MatchString(r.PodRef) {
						found = true
					}
				}
			}
			Expect(found).To(BeFalse())
			// All overlappingrangeipreservations shall have been updated
			ips, err := reconcileLooper.k8sClient.ListOverlappingIPs(ctx)
			Expect(err).NotTo(HaveOccurred())
			for _, ip := range ips {
				if !regexp.MustCompile(pattern).MatchString(ip.Spec.PodRef) {
					found = true
				}
			}
			Expect(found).To(BeFalse())
		})
	})
})

func generateLegacyIPPoolSpec(ipRange string, namespace string, poolName string, pods ...*v1.Pod) *v1alpha1.IPPool {
	allocations := map[string]v1alpha1.IPAllocation{}
	for i, pod := range pods {
		allocations[fmt.Sprintf("%d", i+1)] = v1alpha1.IPAllocation{
			PodRef: fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
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
