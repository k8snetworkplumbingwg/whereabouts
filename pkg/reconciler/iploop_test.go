package reconciler

import (
	"context"
	"fmt"
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	whereaboutsv1alpha1 "github.com/dougbtv/whereabouts/pkg/api/v1alpha1"
	"github.com/dougbtv/whereabouts/pkg/types"
)

func TestIPReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconcile IP address allocation in the system")
}

// mock the pool
type dummyPool struct {
	orphans []types.IPReservation
	pool    whereaboutsv1alpha1.IPPool
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
			cancelFunc:  func() {},
			ctx:         context.TODO(),
			orphanedIPs: orphanedIPs,
		}

		return reconciler
	}

	When("there are no IP addresses to reconcile", func() {
		BeforeEach(func() {
			ipReconciler = newIPReconciler()
		})

		It("does not delete anything", func() {
			reconciledIPs, err := ipReconciler.ReconcileIPPools()
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
			reconciledIPs, err := ipReconciler.ReconcileIPPools()
			Expect(err).NotTo(HaveOccurred())

			reapedIPAddr := types.IPReservation{IP: net.ParseIP(firstIPInRange), PodRef: generatePodRef(namespace, podName)}
			Expect(reconciledIPs).To(ConsistOf(reapedIPAddr))
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
				reconciledIPs, err := ipReconciler.ReconcileIPPools()
				Expect(err).NotTo(HaveOccurred())

				reapedIPAddr := types.IPReservation{IP: net.ParseIP("192.168.14.2"), PodRef: generatePodRef(namespace, "pod2")}
				Expect(reconciledIPs).To(ConsistOf(reapedIPAddr))
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
				reconciledIPs, err := ipReconciler.ReconcileIPPools()
				Expect(err).To(MatchError(fmt.Sprintf("Did not find reserved IP for container %s", reservationPodRef)))
				Expect(reconciledIPs).To(BeEmpty())
			})
		})
	})
})

func generateIPPool(cidr string, podRefs ...string) whereaboutsv1alpha1.IPPool {
	allocations := map[string]whereaboutsv1alpha1.IPAllocation{}
	for i, podRef := range podRefs {
		allocations[fmt.Sprintf("%d", i)] = whereaboutsv1alpha1.IPAllocation{PodRef: podRef}
	}

	return whereaboutsv1alpha1.IPPool{
		Spec: whereaboutsv1alpha1.IPPoolSpec{
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

func generatePodRef(namespace, podName string) string {
	return fmt.Sprintf("%s/%s", namespace, podName)
}
