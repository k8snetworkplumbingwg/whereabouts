package allocate

import (
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

var _ = Describe("AssignIPForClaim", func() {
	var (
		ipRange types.RangeConfiguration
	)

	BeforeEach(func() {
		ipRange = types.RangeConfiguration{
			Range:      "192.168.1.0/24",
			RangeStart: net.ParseIP("192.168.1.1"),
			RangeEnd:   net.ParseIP("192.168.1.10"),
		}
	})

	It("reuses a preferred claim IP for a new pod", func() {
		existing := []types.IPReservation{{
			IP:           net.ParseIP("192.168.1.5"),
			ContainerID:  "old-container",
			PodRef:       "ns/old-pod",
			IfName:       "net1",
			IPAMClaimRef: "ns/vm-net1",
		}}
		assigned, updated, err := AssignIPForClaim(
			ipRange, existing, "new-container", "ns/new-pod", "net1", "ns/vm-net1",
			[]net.IP{net.ParseIP("192.168.1.5")},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(assigned.IP.String()).To(Equal("192.168.1.5"))
		Expect(updated).To(HaveLen(1))
		Expect(updated[0].PodRef).To(Equal("ns/new-pod"))
		Expect(updated[0].ContainerID).To(Equal("new-container"))
		Expect(updated[0].IPAMClaimRef).To(Equal("ns/vm-net1"))
	})

	It("allocates a fresh IP and tags it with the claim ref", func() {
		assigned, updated, err := AssignIPForClaim(
			ipRange, nil, "c1", "ns/pod", "net1", "ns/claim-a", nil,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(assigned.IP.String()).To(Equal("192.168.1.1"))
		Expect(updated).To(HaveLen(1))
		Expect(updated[0].IPAMClaimRef).To(Equal("ns/claim-a"))
	})

	It("falls back to legacy AssignIP behavior without a claim", func() {
		assigned, updated, err := AssignIP(ipRange, nil, "c1", "ns/pod", "net1")
		Expect(err).NotTo(HaveOccurred())
		Expect(assigned.IP.String()).To(Equal("192.168.1.1"))
		Expect(updated[0].IPAMClaimRef).To(BeEmpty())
	})
})
