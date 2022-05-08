package poolconsistency

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8snetplumbersv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

func TestIPPoolConsistencyChecker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Check IP Pool consistency")
}

var _ = Describe("IP Pool consistency checker", func() {
	Context("Stale IPs", func() {
		Context("empty IP pool", func() {
			var (
				pool storage.IPPool
			)

			BeforeEach(func() {
				pool = NewMockedPool()
			})

			table.DescribeTable("does not have stale IPs", func(podList []corev1.Pod) {
				Expect(NewPoolConsistencyCheck(pool, podList).StaleIPs()).To(BeEmpty())
			},
				table.Entry("without any live pod", []corev1.Pod{}),
				table.Entry("independently of live pods", []corev1.Pod{
					newPod("1", "2", "111.111.111.111"),
				}))
		})

		Context("IP pool with allocations", func() {
			const ip = "192.168.200.2"
			var pool storage.IPPool

			BeforeEach(func() {
				pool = NewMockedPool(types.IPReservation{
					IP:          net.ParseIP(ip),
					ContainerID: "abc",
					PodRef:      "cba",
					IsAllocated: true,
				})
			})

			It("an IP pool whose allocations point to live pods with different addresses", func() {
				const staleIP = "192.168.123.200"
				livePodList := []corev1.Pod{
					newPod("pod", "default", staleIP),
				}
				Expect(NewPoolConsistencyCheck(pool, livePodList).StaleIPs()).To(ConsistOf(ip))
			})
		})
	})

	Context("Missing IPs", func() {
		Context("no running pods", func() {
			const ip = "192.168.200.2"

			var podList []corev1.Pod

			table.DescribeTable("does not have stale IPs", func(ipPool storage.IPPool) {
				Expect(NewPoolConsistencyCheck(ipPool, podList).MissingIPs()).To(BeEmpty())
			},
				table.Entry("with an empty IPPool", NewMockedPool()),
				table.Entry("even when the IPPool features allocations", NewMockedPool(
					types.IPReservation{IP: net.ParseIP(ip)})))
		})

		Context("live pods whose IPs came from whereabouts", func() {
			const ip = "192.168.200.2"

			var livePodList []corev1.Pod

			BeforeEach(func() {
				livePodList = []corev1.Pod{newPod("1", "2", ip)}
			})

			It("a pool consistent with the live pods is free of missing IPs", func() {
				Expect(
					NewPoolConsistencyCheck(
						NewMockedPool(ipReservation(ip)), livePodList).MissingIPs()).To(BeEmpty())
			})

			It("a pool that is *not* consistent with the live pods has missing IPs", func() {
				const staleIP = "192.168.123.200"
				Expect(
					NewPoolConsistencyCheck(
						NewMockedPool(ipReservation(staleIP)), livePodList).MissingIPs()).To(ConsistOf(ip))
			})
		})
	})
})

func ipReservation(ipAddr string) types.IPReservation {
	return types.IPReservation{IP: net.ParseIP(ipAddr)}
}

type mockedPool struct {
	reservations []types.IPReservation
}

func NewMockedPool(ipReservations ...types.IPReservation) *mockedPool {
	return &mockedPool{reservations: ipReservations}
}

func (mp *mockedPool) Allocations() []types.IPReservation {
	return mp.reservations
}

func (mp *mockedPool) Update(context.Context, []types.IPReservation) error {
	return nil
}

func newPod(name string, namespace string, ips ...string) corev1.Pod {
	var ifaceStatus []k8snetplumbersv1.NetworkStatus
	for i, ip := range ips {
		ifaceStatus = append(ifaceStatus, k8snetplumbersv1.NetworkStatus{
			Name:      fmt.Sprintf("net%d", i+1),
			Interface: fmt.Sprintf("net%d", i+1),
			IPs:       []string{ip},
		})
	}

	serializedIfaceStatus, _ := json.Marshal(&ifaceStatus)
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{k8snetplumbersv1.NetworkStatusAnnot: string(serializedIfaceStatus)},
		},
	}
}
