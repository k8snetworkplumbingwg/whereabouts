package allocate

import (
	"fmt"
	"net"
	"testing"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Allocate Suite")
}

var _ = Describe("Allocation operations", func() {
	It("can IterateForAssignment on an IPv4 address", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.1.1/24")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(newip.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has NO leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("caa5::0/112")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(newip.Equal(net.ParseIP("caa5::1"))).To(BeTrue())

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has ALL leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("::1/126")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(newip.Equal(net.ParseIP("::1"))).To(BeTrue())

	})

	//

	It("can IterateForAssignment on an IPv6 address when the first hextet has TWO leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("fd::1/116")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(newip.Equal(net.ParseIP("fd::1"))).To(BeTrue())

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/126")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(newip.Equal(net.ParseIP("100::2:1"))).To(BeTrue())
	})

	It("can IterateForAssignment on an IPv4 address excluding a range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.0/30"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(newip.Equal(net.ParseIP("192.168.0.4"))).To(BeTrue())

	})

	It("can IterateForAssignment on an IPv4 address excluding a range which is a single IP", func() {
		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.1"}
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(newip.Equal(net.ParseIP("192.168.0.2"))).To(BeTrue())
	})

	It("correctly handles invalid syntax for an exclude range with IPv4", func() {
		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.1/123"}
		_, _, err = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).To(MatchError(HavePrefix("could not parse exclude range")))
	})

	It("can IterateForAssignment on an IPv6 address excluding a range", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2:1/126"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(newip.Equal(net.ParseIP("100::2:4"))).To(BeTrue())

	})

	It("can IterateForAssignment on an IPv6 address excluding a range which is a single IP", func() {
		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2:1"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(newip.Equal(net.ParseIP("100::2:2"))).To(BeTrue())
	})

	It("correctly handles invalid syntax for an exclude range with IPv6", func() {
		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2::1"}
		_, _, err = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).To(MatchError(HavePrefix("could not parse exclude range")))
	})

	It("can IterateForAssignment on an IPv6 address excluding a very large range", func() {

		firstip, ipnet, err := net.ParseCIDR("2001:db8::/30")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"2001:db8::0/32"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(newip.Equal(net.ParseIP("2001:db9::"))).To(BeTrue())

	})

	It("can IterateForAssignment on an IPv4 address excluding unsorted ranges", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/28")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.0/30", "192.168.0.6/31", "192.168.0.8/31", "192.168.0.4/30"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(newip.Equal(net.ParseIP("192.168.0.10"))).To(BeTrue())

		exrange = []string{"192.168.0.0/30", "192.168.0.14/31", "192.168.0.4/30", "192.168.0.6/31", "192.168.0.8/31"}
		newip, _, _ = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(newip.Equal(net.ParseIP("192.168.0.10"))).To(BeTrue())
	})

	It("can IterateForAssignment on an IPv4 address excluding a range and respect the requested range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("192.168.0.4"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.5"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.6"),
				PodRef: "default/pod1",
			},
		}
		exrange := []string{"192.168.0.0/30"}
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})
	It("can IterateForAssignment on an IPv4 address excluding the last allocatable IP and respect the requested range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("192.168.0.1"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.2"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.3"),
				PodRef: "default/pod1",
			},
		}
		exrange := []string{"192.168.0.4/30"}
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})

	It("can IterateForAssignment on an IPv6 address excluding a range and respect the requested range", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("100::2:1"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("100::2:2"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("100::2:3"),
				PodRef: "default/pod1",
			},
		}

		exrange := []string{"100::2:4/126"}
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})

	// Make sure that the network IP and the broadcast IP are excluded from the range.
	// According to https://github.com/k8snetworkplumbingwg/whereabouts, we look at a range "... excluding the first
	// network address and the last broadcast address".
	When("no range_end is specified", func() {
		It("the range start and end must be ignored if they are not within the bounds of the range for IPv4", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/29")
			Expect(err).NotTo(HaveOccurred())
			rangeStart := net.ParseIP("192.168.0.0") // Network address, out of bounds.
			newip, _, err := IterateForAssignment(*ipnet, rangeStart, nil, nil, nil, "0xdeadbeef", "", "", false)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("192.168.0.1"))).To(BeTrue())
		})
	})

	When("a range_end is specified", func() {
		It("the range start and end must be ignored if they are not within the bounds of the range for IPv4", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/29")
			Expect(err).NotTo(HaveOccurred())
			rangeStart := net.ParseIP("192.168.0.0") // Network address, out of bounds.
			rangeEnd := net.ParseIP("192.168.0.8")   // Broadcast address, out of bounds.
			newip, _, err := IterateForAssignment(*ipnet, rangeStart, rangeEnd, nil, nil, "0xdeadbeef", "", "", false)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("192.168.0.1"))).To(BeTrue())
		})
	})

	// Make sure that the end_range parameter is respected even when it is part of the exclude range.
	It("can IterateForAssignment on an IPv4 address excluding a range and respect endrange value", func() {
		_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
		Expect(err).NotTo(HaveOccurred())
		startip := net.ParseIP("192.168.0.1")
		lastip := net.ParseIP("192.168.0.6")

		ipres := []types.IPReservation{
			{
				IP:     net.ParseIP("192.168.0.1"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.2"),
				PodRef: "default/pod1",
			},
			{
				IP:     net.ParseIP("192.168.0.3"),
				PodRef: "default/pod1",
			},
		}
		exrange := []string{"192.168.0.4/30"}
		_, _, err = IterateForAssignment(*ipnet, startip, lastip, ipres, exrange, "0xdeadbeef", "", "", false)
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))
	})

	Context("test reserve lists", func() {
		When("an empty reserve list is provided", func() {
			It("is properly updated", func() {
				_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
				Expect(err).NotTo(HaveOccurred())
				startip := net.ParseIP("192.168.0.1")
				lastip := net.ParseIP("192.168.0.6")

				ipres := []types.IPReservation{}
				_, ipres, err = IterateForAssignment(*ipnet, startip, lastip, ipres, nil, "0xdeadbeef", "dummy-0", "", false)
				Expect(err).NotTo(HaveOccurred())
				Expect(ipres).To(HaveLen(1))
				Expect(ipres[0].IP.Equal(net.ParseIP("192.168.0.1"))).To(BeTrue())
			})
		})

		When("a reserve list is provided", func() {
			It("is properly updated", func() {
				_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
				Expect(err).NotTo(HaveOccurred())
				startip := net.ParseIP("192.168.0.1")
				lastip := net.ParseIP("192.168.0.6")

				ipres := []types.IPReservation{
					{
						IP:     net.ParseIP("192.168.0.1"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.2"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.3"),
						PodRef: "default/pod1",
					},
				}

				_, ipres, err = IterateForAssignment(*ipnet, startip, lastip, ipres, nil, "0xdeadbeef", "dummy-0", "", false)
				Expect(err).NotTo(HaveOccurred())
				Expect(ipres).To(HaveLen(4))
				Expect(ipres[3].IP.Equal(net.ParseIP("192.168.0.4"))).To(BeTrue())
			})
		})

		When("a reserve list with a hole is provided", func() {
			It("is properly updated", func() {
				_, ipnet, err := net.ParseCIDR("192.168.0.0/28")
				Expect(err).NotTo(HaveOccurred())
				startip := net.ParseIP("192.168.0.1")
				lastip := net.ParseIP("192.168.0.6")

				ipres := []types.IPReservation{
					{
						IP:     net.ParseIP("192.168.0.1"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.2"),
						PodRef: "default/pod1",
					},
					{
						IP:     net.ParseIP("192.168.0.5"),
						PodRef: "default/pod1",
					},
				}

				_, ipres, err = IterateForAssignment(*ipnet, startip, lastip, ipres, nil, "0xdeadbeef", "dummy-0", "", false)
				Expect(err).NotTo(HaveOccurred())
				Expect(ipres).To(HaveLen(4))
				Expect(ipres[3].IP.Equal(net.ParseIP("192.168.0.3"))).To(BeTrue())
			})
		})
	})

	Context("DeallocateIP", func() {
		It("removes the matching reservation and returns its IP", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
				{IP: net.ParseIP("192.168.1.2"), ContainerID: "bbb", PodRef: "default/pod2", IfName: "eth0"},
				{IP: net.ParseIP("192.168.1.3"), ContainerID: "ccc", PodRef: "default/pod3", IfName: "net1"},
			}
			updatedList, ip := DeallocateIP(reservelist, "bbb", "eth0")
			Expect(ip).To(Equal(net.ParseIP("192.168.1.2")))
			Expect(updatedList).To(HaveLen(2))
			// Swap-remove: last element replaces removed one
			Expect(updatedList[0].IP.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())
			Expect(updatedList[1].IP.Equal(net.ParseIP("192.168.1.3"))).To(BeTrue())
		})

		It("returns nil IP and unchanged list when containerID is not found", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
			}
			updatedList, ip := DeallocateIP(reservelist, "zzz", "eth0")
			Expect(ip).To(BeNil())
			Expect(updatedList).To(HaveLen(1))
		})

		It("matches on both containerID and ifName", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
				{IP: net.ParseIP("192.168.1.2"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "net1"},
			}
			updatedList, ip := DeallocateIP(reservelist, "aaa", "net1")
			Expect(ip).To(Equal(net.ParseIP("192.168.1.2")))
			Expect(updatedList).To(HaveLen(1))
			Expect(updatedList[0].IfName).To(Equal("eth0"))
		})

		It("handles single-element list", func() {
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.1"), ContainerID: "aaa", PodRef: "default/pod1", IfName: "eth0"},
			}
			updatedList, ip := DeallocateIP(reservelist, "aaa", "eth0")
			Expect(ip).To(Equal(net.ParseIP("192.168.1.1")))
			Expect(updatedList).To(BeEmpty())
		})
	})

	Context("AssignIP idempotency", func() {
		It("returns the existing IP when podRef and ifName already have an allocation", func() {
			ipamConf := types.RangeConfiguration{
				Range:      "192.168.1.0/24",
				OmitRanges: []string{},
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.5"), ContainerID: "old-id", PodRef: "default/mypod", IfName: "eth0"},
			}
			result, updatedList, err := AssignIP(ipamConf, reservelist, "new-id", "default/mypod", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("192.168.1.5"))).To(BeTrue())
			Expect(updatedList).To(HaveLen(1))
			// containerID should be updated to the new one
			Expect(updatedList[0].ContainerID).To(Equal("new-id"))
		})

		It("allocates a new IP when podRef matches but ifName differs", func() {
			ipamConf := types.RangeConfiguration{
				Range:      "192.168.1.0/24",
				OmitRanges: []string{},
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.5"), ContainerID: "aaa", PodRef: "default/mypod", IfName: "eth0"},
			}
			result, updatedList, err := AssignIP(ipamConf, reservelist, "bbb", "default/mypod", "net1")
			Expect(err).NotTo(HaveOccurred())
			// Should get a different IP (192.168.1.1 as the lowest available)
			Expect(result.IP.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())
			Expect(updatedList).To(HaveLen(2))
		})
	})

	Describe("AssignmentError", func() {
		It("returns a descriptive error message", func() {
			_, ipnet, _ := net.ParseCIDR("10.0.0.0/24")
			err := AssignmentError{
				firstIP:       net.ParseIP("10.0.0.1"),
				lastIP:        net.ParseIP("10.0.0.254"),
				ipnet:         *ipnet,
				excludeRanges: []string{"10.0.0.100-10.0.0.110"},
			}
			msg := err.Error()
			Expect(msg).To(ContainSubstring("Could not allocate IP in range"))
			Expect(msg).To(ContainSubstring("10.0.0.1"))
			Expect(msg).To(ContainSubstring("10.0.0.254"))
			Expect(msg).To(ContainSubstring("10.0.0.0/24"))
			Expect(msg).To(ContainSubstring("10.0.0.100-10.0.0.110"))
			Expect(msg).To(ContainSubstring("the pool may be exhausted"))
		})
	})

	// ── L3 mode tests (#enable_l3) ──────────────────────────────────────────
	Context("L3 mode (enable_l3)", func() {
		It("allocates the network address (.0) when L3=true", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/24")
			Expect(err).NotTo(HaveOccurred())
			newip, _, err := IterateForAssignment(*ipnet, nil, nil, nil, nil, "c1", "default/pod1", "eth0", true)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("192.168.0.0"))).To(BeTrue())
		})

		It("allocates the broadcast address (.255) when all others are taken and L3=true", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.252/30")
			Expect(err).NotTo(HaveOccurred())
			// Reserve .252, .253, .254 — only .255 (broadcast) should remain in L3.
			ipres := []types.IPReservation{
				{IP: net.ParseIP("192.168.0.252"), PodRef: "default/p1"},
				{IP: net.ParseIP("192.168.0.253"), PodRef: "default/p2"},
				{IP: net.ParseIP("192.168.0.254"), PodRef: "default/p3"},
			}
			newip, _, err := IterateForAssignment(*ipnet, nil, nil, ipres, nil, "c4", "default/p4", "eth0", true)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("192.168.0.255"))).To(BeTrue())
		})

		It("L2 mode (default) skips network and broadcast addresses", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/30")
			Expect(err).NotTo(HaveOccurred())
			// /30 in L2: .1 and .2 are usable, .0 (network) and .3 (broadcast) are not.
			newip, _, err := IterateForAssignment(*ipnet, nil, nil, nil, nil, "c1", "default/pod1", "eth0", false)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("192.168.0.1"))).To(BeTrue())
		})

		It("L3 mode exhausts all IPs in /30 (4 IPs total)", func() {
			_, ipnet, err := net.ParseCIDR("192.168.0.0/30")
			Expect(err).NotTo(HaveOccurred())
			var ipres []types.IPReservation
			for i := range 4 {
				var newip net.IP
				newip, ipres, err = IterateForAssignment(*ipnet, nil, nil, ipres, nil, "c1", fmt.Sprintf("default/pod%d", i), "eth0", true)
				Expect(err).NotTo(HaveOccurred())
				Expect(newip).NotTo(BeNil())
			}
			Expect(ipres).To(HaveLen(4))
			// 5th allocation should fail.
			_, _, err = IterateForAssignment(*ipnet, nil, nil, ipres, nil, "c1", "default/pod4", "eth0", true)
			Expect(err).To(HaveOccurred())
		})

		It("L3 mode works with IPv6", func() {
			_, ipnet, err := net.ParseCIDR("fd00::0/126")
			Expect(err).NotTo(HaveOccurred())
			newip, _, err := IterateForAssignment(*ipnet, nil, nil, nil, nil, "c1", "default/pod1", "eth0", true)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("fd00::"))).To(BeTrue())
		})

		It("L3 mode respects rangeStart/rangeEnd", func() {
			_, ipnet, err := net.ParseCIDR("10.0.0.0/24")
			Expect(err).NotTo(HaveOccurred())
			rangeStart := net.ParseIP("10.0.0.10")
			rangeEnd := net.ParseIP("10.0.0.20")
			newip, _, err := IterateForAssignment(*ipnet, rangeStart, rangeEnd, nil, nil, "c1", "default/pod1", "eth0", true)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("10.0.0.10"))).To(BeTrue())
		})

		It("L3 mode respects exclude ranges", func() {
			_, ipnet, err := net.ParseCIDR("10.0.0.0/30")
			Expect(err).NotTo(HaveOccurred())
			exrange := []string{"10.0.0.0/31"} // Excludes .0 and .1
			newip, _, err := IterateForAssignment(*ipnet, nil, nil, nil, exrange, "c1", "default/pod1", "eth0", true)
			Expect(err).NotTo(HaveOccurred())
			Expect(newip.Equal(net.ParseIP("10.0.0.2"))).To(BeTrue())
		})

		It("AssignIP with L3=true allocates from network address", func() {
			ipamConf := types.RangeConfiguration{
				Range: "192.168.0.0/30",
				L3:    true,
			}
			result, _, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("192.168.0.0"))).To(BeTrue())
		})
	})

	// ── /32 and /31 allocation tests (#573) ──────────────────────────────────
	Context("/32 and /31 allocation (#573)", func() {
		It("allocates the single IP in a /32 range", func() {
			ipamConf := types.RangeConfiguration{
				Range: "10.0.0.5/32",
			}
			result, reservelist, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
			Expect(reservelist).To(HaveLen(1))
		})

		It("reports exhaustion after single /32 IP is taken", func() {
			ipamConf := types.RangeConfiguration{
				Range: "10.0.0.5/32",
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("10.0.0.5"), ContainerID: "c1", PodRef: "default/pod1", IfName: "eth0"},
			}
			_, _, err := AssignIP(ipamConf, reservelist, "c2", "default/pod2", "eth0")
			Expect(err).To(HaveOccurred())
		})

		It("allocates both IPs in a /31 range (RFC 3021)", func() {
			ipamConf := types.RangeConfiguration{
				Range: "10.0.0.4/31",
			}
			result1, reservelist, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result1.IP.Equal(net.ParseIP("10.0.0.4"))).To(BeTrue())

			result2, reservelist, err := AssignIP(ipamConf, reservelist, "c2", "default/pod2", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result2.IP.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
			Expect(reservelist).To(HaveLen(2))
		})

		It("allocates the single IPv6 /128 address", func() {
			ipamConf := types.RangeConfiguration{
				Range: "fd00::1/128",
			}
			result, _, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("fd00::1"))).To(BeTrue())
		})

		It("allocates both IPs in an IPv6 /127 range", func() {
			ipamConf := types.RangeConfiguration{
				Range: "fd00::2/127",
			}
			result1, reservelist, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result1.IP.Equal(net.ParseIP("fd00::2"))).To(BeTrue())

			result2, _, err := AssignIP(ipamConf, reservelist, "c2", "default/pod2", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result2.IP.Equal(net.ParseIP("fd00::3"))).To(BeTrue())
		})
	})

	// ── Preferred/Sticky IP tests (#621) ─────────────────────────────────────
	Context("Preferred/Sticky IP (#621)", func() {
		It("assigns preferred IP when available", func() {
			ipamConf := types.RangeConfiguration{
				Range:       "192.168.1.0/24",
				PreferredIP: net.ParseIP("192.168.1.100"),
			}
			result, reservelist, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("192.168.1.100"))).To(BeTrue())
			Expect(reservelist).To(HaveLen(1))
			Expect(reservelist[0].PodRef).To(Equal("default/pod1"))
		})

		It("falls back to lowest-available when preferred is already reserved", func() {
			ipamConf := types.RangeConfiguration{
				Range:       "192.168.1.0/24",
				PreferredIP: net.ParseIP("192.168.1.100"),
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.100"), ContainerID: "c0", PodRef: "default/other", IfName: "eth0"},
			}
			result, _, err := AssignIP(ipamConf, reservelist, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())
		})

		It("falls back when preferred IP is in exclude range", func() {
			ipamConf := types.RangeConfiguration{
				Range:       "192.168.1.0/24",
				OmitRanges:  []string{"192.168.1.100/32"},
				PreferredIP: net.ParseIP("192.168.1.100"),
			}
			result, _, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())
		})

		It("falls back when preferred IP is outside the CIDR range", func() {
			ipamConf := types.RangeConfiguration{
				Range:       "192.168.1.0/24",
				PreferredIP: net.ParseIP("10.0.0.1"),
			}
			result, _, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			// Should get lowest available since preferred is out of range
			Expect(result.IP.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())
		})

		It("preferred IP works with IPv6", func() {
			ipamConf := types.RangeConfiguration{
				Range:       "fd00::/120",
				PreferredIP: net.ParseIP("fd00::50"),
			}
			result, _, err := AssignIP(ipamConf, nil, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("fd00::50"))).To(BeTrue())
		})

		It("idempotent allocation takes precedence over preferred IP", func() {
			ipamConf := types.RangeConfiguration{
				Range:       "192.168.1.0/24",
				PreferredIP: net.ParseIP("192.168.1.100"),
			}
			reservelist := []types.IPReservation{
				{IP: net.ParseIP("192.168.1.50"), ContainerID: "c1", PodRef: "default/pod1", IfName: "eth0"},
			}
			// Same pod+interface — should return existing allocation, not preferred.
			result, _, err := AssignIP(ipamConf, reservelist, "c1", "default/pod1", "eth0")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IP.Equal(net.ParseIP("192.168.1.50"))).To(BeTrue())
		})
	})
})
