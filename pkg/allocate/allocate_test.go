package allocate

import (
	"fmt"
	"math"
	"math/big"
	"net"
	"testing"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Allocation utility functions", func() {
	/*
		func byteSliceAdd(ar1, ar2 []byte) ([]byte, error) {
		func byteSliceSub(ar1, ar2 []byte) ([]byte, error) {
		func ipAddrToUint64(ip net.IP) uint64 {
		func ipAddrFromUint64(num uint64) net.IP {
		func IPAddOffset(ip net.IP, offset uint64) net.IP {
		func IPGetOffset(ip1, ip2 net.IP) uint64 {
	*/
	It("tests byteSliceAdd normal case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 10}
		bSum, err := byteSliceAdd(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11, 11}))
	})

	It("tests byteSliceAdd carry case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		bSum, err := byteSliceAdd(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0}))
	})

	It("tests byteSliceSub normal case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 10}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1}
		bSum, err := byteSliceSub(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9, 9}))
	})

	It("tests byteSliceSub carry case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		bSum, err := byteSliceSub(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255}))
	})

	It("can convert ipAddrToUint64", func() {
		b := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 255, 255}
		bNum := ipAddrToUint64(net.IP(b))
		Expect(bNum).To(Equal(uint64(0x1ffff)))
	})

	It("can convert ipAddrFromUint64", func() {
		uintNum := uint64(0x1ffff)
		ip := net.IP([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 255, 255})
		bNum := ipAddrFromUint64(uintNum)
		Expect(bNum).To(Equal(ip))
	})

	It("confirms the IPAddOffset normal case", func() {
		ip1 := net.ParseIP("192.168.2.23")
		ip2 := IPAddOffset(ip1, uint64(1))
		Expect(ip2).To(Equal(net.ParseIP("192.168.2.24")))

		ip3 := net.ParseIP("ff02::1")
		ip4 := IPAddOffset(ip3, uint64(1))
		Expect(ip4).To(Equal(net.ParseIP("ff02::2")))
	})

	It("confirms the IPAddOffset carry case", func() {
		ip1 := net.ParseIP("192.168.2.255")
		ip2 := IPAddOffset(ip1, uint64(1))
		Expect(ip2).To(Equal(net.ParseIP("192.168.3.0")))

		ip3 := net.ParseIP("192.168.255.255")
		ip4 := IPAddOffset(ip3, uint64(1))
		Expect(ip4).To(Equal(net.ParseIP("192.169.0.0")))

		ip5 := net.ParseIP("ff02::ff")
		ip6 := IPAddOffset(ip5, uint64(1))
		Expect(ip6).To(Equal(net.ParseIP("ff02::0:100")))

		ip7 := net.ParseIP("ff02::ffff")
		ip8 := IPAddOffset(ip7, uint64(1))
		Expect(ip8).To(Equal(net.ParseIP("ff02::1:0")))

		ip9 := net.ParseIP("ff02::ffff:ffff")
		ip10 := IPAddOffset(ip9, uint64(1))
		Expect(ip10).To(Equal(net.ParseIP("ff02::1:0:0")))
	})

	It("confirms the IPAddOffset error case", func() {
		ip1 := net.ParseIP("192.168.2.255")
		ip2 := IPAddOffset(ip1, uint64(math.MaxUint32+1))
		Expect(ip2).To(BeNil())

		ip3 := net.ParseIP("ff02::1")
		ip4 := IPAddOffset(ip3, uint64(math.MaxUint32+1))
		Expect(ip4).NotTo(BeNil())
	})

	It("confirms the IPGetOffset normal case", func() {
		ip1 := net.ParseIP("192.168.2.255")
		ip2 := net.ParseIP("192.168.2.1")
		offset1 := IPGetOffset(ip1, ip2)
		Expect(offset1).To(Equal(uint64(254)))

		ip3 := net.ParseIP("ff02::ff")
		ip4 := net.ParseIP("ff02::1")
		offset2 := IPGetOffset(ip3, ip4)
		Expect(offset2).To(Equal(uint64(254)))
	})

	It("confirms the IPGetOffset carry case", func() {
		ip1 := net.ParseIP("192.168.3.0")
		ip2 := net.ParseIP("192.168.2.1")
		offset1 := IPGetOffset(ip1, ip2)
		Expect(offset1).To(Equal(uint64(255)))

		ip3 := net.ParseIP("ff02::100")
		ip4 := net.ParseIP("ff02::1")
		offset2 := IPGetOffset(ip3, ip4)
		Expect(offset2).To(Equal(uint64(255)))

		ip5 := net.ParseIP("ff02::1:0")
		ip6 := net.ParseIP("ff02::1")
		offset3 := IPGetOffset(ip5, ip6)
		Expect(offset3).To(Equal(uint64(0xffff)))
	})

	It("confirms the IPGetOffset error case", func() {
		// cannot get offset from v4/v6
		ip1 := net.ParseIP("192.168.3.0")
		ip2 := net.ParseIP("ff02::1")
		offset1 := IPGetOffset(ip1, ip2)
		Expect(offset1).To(Equal(uint64(0)))

		// cannot get offset from v6/v4
		ip3 := net.ParseIP("ff02::1")
		ip4 := net.ParseIP("192.168.3.0")
		offset2 := IPGetOffset(ip3, ip4)
		Expect(offset2).To(Equal(uint64(0)))

		// cannot get offset between different length array (ar[4] and ar[16])
		ip5 := net.ParseIP("192.168.4.0")
		ip6 := net.ParseIP("192.168.3.0")
		offset3 := IPGetOffset(ip5.To4(), ip6)
		Expect(offset3).To(Equal(uint64(0)))
	})
})

var _ = Describe("Allocation operations", func() {
	It("creates an IPv4 range properly for 30 bits network address", func() {

		_, ipnet, err := net.ParseCIDR("192.168.21.100/30")
		ip, _ := AddressRange(ipnet)
		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.21.101"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.21.102"))

	})
	It("creates an IPv4 range properly for 24 bits network address with different range start", func() {

		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		ip := net.ParseIP("192.168.2.23") // range start

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.23"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))

	})
	It("creates an IPv4 range properly for 27 bits network address", func() {

		_, ipnet, err := net.ParseCIDR("192.168.2.200/27")
		ip, _ := AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.193"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.222"))

	})
	It("creates an IPv4 range properly for 24 bits network address", func() {

		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		ip, _ := AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)

		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))

	})
	// Handy IPv6 CIDR calculator: https://www.ultratools.com/tools/ipv6CIDRToRangeResult?ipAddress=2001%3A%3A0%2F28
	It("creates an IPv6 range properly for 116 bits network address", func() {

		_, ipnet, err := net.ParseCIDR("2001::0/116")
		ip, _ := AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("2001::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001::fff"))

	})

	It("creates an IPv6 range when the first hextet has leading zeroes", func() {

		_, ipnet, err := net.ParseCIDR("fd:db8:abcd:0012::0/96")
		ip, _ := AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("fd:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("fd:db8:abcd:12::ffff:ffff"))

	})

	It("can IterateForAssignment on an IPv4 address", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.1.1/24")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("192.168.1.1"))

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has NO leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("caa5::0/112")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("caa5::1"))

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has ALL leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("::1/126")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("::1"))

	})

	//

	It("can IterateForAssignment on an IPv6 address when the first hextet has TWO leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("fd::1/116")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("fd::1"))

	})

	It("can IterateForAssignment on an IPv6 address when the first hextet has leading zeroes", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/126")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		var exrange []string
		newip, _, err := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(newip)).To(Equal("100::2:1"))
	})

	It("can IterateForAssignment on an IPv4 address excluding a range", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/29")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.0/30"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.4"))

	})

	It("can IterateForAssignment on an IPv6 address excluding a range", func() {

		firstip, ipnet, err := net.ParseCIDR("100::2:1/125")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"100::2:1/126"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(fmt.Sprint(newip)).To(Equal("100::2:4"))

	})

	It("can IterateForAssignment on an IPv6 address excluding a very large range", func() {

		firstip, ipnet, err := net.ParseCIDR("2001:db8::/30")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"2001:db8::0/32"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(fmt.Sprint(newip)).To(Equal("2001:db9::"))

	})

	It("can IterateForAssignment on an IPv4 address excluding unsorted ranges", func() {

		firstip, ipnet, err := net.ParseCIDR("192.168.0.0/28")
		Expect(err).NotTo(HaveOccurred())

		// figure out the range start.
		calculatedrangestart := net.ParseIP(firstip.Mask(ipnet.Mask).String())

		var ipres []types.IPReservation
		exrange := []string{"192.168.0.0/30", "192.168.0.6/31", "192.168.0.8/31", "192.168.0.4/30"}
		newip, _, _ := IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.10"))

		exrange = []string{"192.168.0.0/30", "192.168.0.14/31", "192.168.0.4/30", "192.168.0.6/31", "192.168.0.8/31"}
		newip, _, _ = IterateForAssignment(*ipnet, calculatedrangestart, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(fmt.Sprint(newip)).To(Equal("192.168.0.10"))
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
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "")
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
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "")
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
		_, _, err = IterateForAssignment(*ipnet, firstip, nil, ipres, exrange, "0xdeadbeef", "")
		Expect(err).To(MatchError(HavePrefix("Could not allocate IP in range")))

	})

	It("creates an IPv6 range properly for 96 bits network address", func() {

		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/96")
		ip, _ := AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::ffff:ffff"))

	})
	It("creates an IPv6 range properly for 64 bits network address", func() {

		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		ip, _ := AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:ffff"))

	})
	It("do not fail when the mask meets minimum required", func() {

		_, badipnet, err := net.ParseCIDR("192.168.21.100/30")
		badip, _ := AddressRange(badipnet)
		Expect(err).NotTo(HaveOccurred())

		_, _, err = GetIPRange(badip, *badipnet)
		Expect(err).NotTo(HaveOccurred())

	})
	It("fails when the mask is too short", func() {

		_, badipnet, err := net.ParseCIDR("192.168.21.100/31")
		badip, _ := AddressRange(badipnet)
		Expect(err).NotTo(HaveOccurred())

		_, _, err = GetIPRange(badip, *badipnet)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("net mask is too short"))

	})
})

func AddressRange(network *net.IPNet) (net.IP, net.IP) {
	// the first IP is easy
	firstIP := network.IP

	// the last IP is the network address OR NOT the mask address
	prefixLen, bits := network.Mask.Size()
	if prefixLen == bits {
		// Easy!
		// But make sure that our two slices are distinct, since they
		// would be in all other cases.
		lastIP := make([]byte, len(firstIP))
		copy(lastIP, firstIP)
		return firstIP, lastIP
	}

	firstIPInt, bits := ipToInt(firstIP)
	hostLen := uint(bits) - uint(prefixLen)
	lastIPInt := big.NewInt(1)
	lastIPInt.Lsh(lastIPInt, hostLen)
	lastIPInt.Sub(lastIPInt, big.NewInt(1))
	lastIPInt.Or(lastIPInt, firstIPInt)

	return firstIP, intToIP(lastIPInt, bits)
}
func ipToInt(ip net.IP) (*big.Int, int) {
	val := &big.Int{}
	val.SetBytes([]byte(ip))
	if len(ip) == net.IPv4len {
		return val, 32
	} else if len(ip) == net.IPv6len {
		return val, 128
	} else {
		panic(fmt.Errorf("Unsupported address length %d", len(ip)))
	}
}

func intToIP(ipInt *big.Int, bits int) net.IP {
	ipBytes := ipInt.Bytes()
	ret := make([]byte, bits/8)
	// Pack our IP bytes into the end of the return array,
	// since big.Int.Bytes() removes front zero padding.
	for i := 1; i <= len(ipBytes); i++ {
		ret[len(ret)-i] = ipBytes[len(ipBytes)-i]
	}
	return net.IP(ret)
}
