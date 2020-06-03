package allocate

import (
	"fmt"
	"math/big"
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Allocation operations", func() {
	It("creates an IPv4 range properly for 30 bits network address", func() {

		ip, ipnet, err := net.ParseCIDR("192.168.21.100/30")
		ip, _ = AddressRange(ipnet)
		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.21.101"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.21.102"))

	})
	It("creates an IPv4 range properly for 24 bits network address with different range start", func() {

		ip, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		ip = net.ParseIP("192.168.2.23") // range start

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.23"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))

	})
	It("creates an IPv4 range properly for 27 bits network address", func() {

		ip, ipnet, err := net.ParseCIDR("192.168.2.200/27")
		ip, _ = AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.193"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.222"))

	})
	It("creates an IPv4 range properly for 24 bits network address", func() {

		ip, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		ip, _ = AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)

		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))

	})
	// Handy IPv6 CIDR calculator: https://www.ultratools.com/tools/ipv6CIDRToRangeResult?ipAddress=2001%3A%3A0%2F28
	It("creates an IPv6 range properly for 116 bits network address", func() {

		ip, ipnet, err := net.ParseCIDR("2001::0/116")
		ip, _ = AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("2001::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001::ffe"))

	})
	It("creates an IPv6 range properly for 96 bits network address", func() {

		ip, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/96")
		ip, _ = AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::ffff:fffe"))

	})
	It("creates an IPv6 range properly for 64 bits network address", func() {

		ip, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		ip, _ = AddressRange(ipnet)

		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(net.ParseIP(ip.String()), *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))

	})
	It("do not fail when the mask meets minimum required", func() {

		badip, badipnet, err := net.ParseCIDR("192.168.21.100/30")
		badip, _ = AddressRange(badipnet)
		Expect(err).NotTo(HaveOccurred())

		_, _, err = GetIPRange(badip, *badipnet)
		Expect(err).NotTo(HaveOccurred())

	})
	It("fails when the mask is too short", func() {

		badip, badipnet, err := net.ParseCIDR("192.168.21.100/31")
		badip, _ = AddressRange(badipnet)
		Expect(err).NotTo(HaveOccurred())

		_, _, err = GetIPRange(badip, *badipnet)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("Net mask is too short"))

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
