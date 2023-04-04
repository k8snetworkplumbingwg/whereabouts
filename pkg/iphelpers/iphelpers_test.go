package iphelpers

import (
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	leftIPIsSmaller        = -1
	leftAndRightIPAreEqual = 0
	leftIPIsLarger         = 1
)

func TestIPHelpers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("CompareIPs operations", func() {
	It("compares IPv4.To4() addresses", func() {
		left := net.ParseIP("192.168.0.0")
		right := net.ParseIP("192.169.0.0")
		Expect(CompareIPs(left.To4(), right.To4())).To(Equal(leftIPIsSmaller))
	})

	It("compares IPv4.To16() addresses", func() {
		left := net.ParseIP("192.169.0.0")
		right := net.ParseIP("192.168.0.0")
		Expect(CompareIPs(left.To16(), right.To16())).To(Equal(leftIPIsLarger))
	})

	It("compares IPv4 mixed addresses", func() {
		left := net.ParseIP("192.168.0.0")
		right := net.ParseIP("192.168.0.0")
		Expect(CompareIPs(left.To16(), right.To4())).To(Equal(leftAndRightIPAreEqual))
	})

	It("compares IPv6 addresses when left is smaller than right", func() {
		left := net.ParseIP("2000::")
		right := net.ParseIP("2000::1")
		Expect(CompareIPs(left, right)).To(Equal(leftIPIsSmaller))
	})

	It("compares IPv6 addresses when left is larger than right", func() {
		left := net.ParseIP("2000::1")
		right := net.ParseIP("2000::")
		Expect(CompareIPs(left, right)).To(Equal(leftIPIsLarger))
	})

	It("compares IPv6 addresses when left == right", func() {
		left := net.ParseIP("2000::1")
		right := net.ParseIP("2000::1")
		Expect(CompareIPs(left, right)).To(Equal(leftAndRightIPAreEqual))
	})
})

var _ = Describe("IPGetOffset operations", func() {
	It("correctly calculates the offset between two IPv4 IPs", func() {
		ip1 := net.ParseIP("192.168.1.1")
		ip2 := net.ParseIP("192.168.1.0")
		offset := IPGetOffset(ip1, ip2)
		Expect(offset).To(Equal(uint64(1)))
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
	})
})

var _ = Describe("IP helper utility functions", func() {
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
})
