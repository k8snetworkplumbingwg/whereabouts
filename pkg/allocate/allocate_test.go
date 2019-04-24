package allocate

import (
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"net"
	"testing"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Allocation operations", func() {
	It("creates a range properly", func() {

		const ifname string = "eth0"
		const nspath string = "/some/where"

		ip, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())

		firstip, lastip, err := GetIPRange(ip, *ipnet)
		Expect(err).NotTo(HaveOccurred())

		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.0"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.255"))

	})
	It("fails when the mask is too short", func() {

		const ifname string = "eth0"
		const nspath string = "/some/where"

		badip, badipnet, err := net.ParseCIDR("10.0.0.100/2")
		Expect(err).NotTo(HaveOccurred())

		_, _, err = GetIPRange(badip, *badipnet)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("Net mask is too short"))

	})

})
