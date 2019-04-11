package storage

import (
	//  "fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"testing"
	// "time"
)

func TestStorage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Storage operations", func() {
	It("works with mutex", func() {
		err := TestGetValue("127.0.0.1:2379")
		Expect(err).NotTo(HaveOccurred())
	})

	// It("gets a key", func() {

	// 	err := TestGetValue("127.0.0.1:2379")
	// 	Expect(err).NotTo(HaveOccurred())

	// 	// const ifname string = "eth0"
	// 	// const nspath string = "/some/where"

	// 	// ip, ipnet, err := net.ParseCIDR("192.168.2.200/24")
	// 	// Expect(err).NotTo(HaveOccurred())

	// 	// firstip, lastip, err := GetIPRange(ip, *ipnet)
	// 	// Expect(err).NotTo(HaveOccurred())

	// 	// Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.0"))
	// 	// Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.255"))

	// })
	// It("errors on etcd not responding", func() {

	// 	SetRequestTimeout(500 * time.Millisecond)
	// 	err := TestGetValue("invalid.address:1234")
	// 	Expect(err).To(HaveOccurred())

	// })
})
