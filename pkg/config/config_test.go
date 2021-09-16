package config

import (
	// "fmt"
	"io/ioutil"
	"net"
	// "os"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Allocation operations", func() {
	It("can load a basic config", func() {

		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
        "ipam": {
          "type": "whereabouts",
          "log_file" : "/tmp/whereabouts.log",
          "log_level" : "debug",
          "etcd_host": "foo",
          "range": "192.168.1.5-192.168.1.25/24",
          "gateway": "192.168.10.1"
        }
      }`

		ipamconfig, _, err := LoadIPAMConfig([]byte(conf), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamconfig.LogLevel).To(Equal("debug"))
		Expect(ipamconfig.LogFile).To(Equal("/tmp/whereabouts.log"))
		Expect(ipamconfig.EtcdHost).To(Equal("foo"))
		Expect(ipamconfig.Range).To(Equal("192.168.1.0/24"))
		Expect(ipamconfig.RangeStart).To(Equal(net.ParseIP("192.168.1.5")))
		Expect(ipamconfig.RangeEnd).To(Equal(net.ParseIP("192.168.1.25")))
		Expect(ipamconfig.Gateway).To(Equal(net.ParseIP("192.168.10.1")))
		Expect(ipamconfig.LeaderLeaseDuration).To(Equal(1500))
		Expect(ipamconfig.LeaderRenewDeadline).To(Equal(1000))
		Expect(ipamconfig.LeaderRetryPeriod).To(Equal(500))

	})

	It("can load a global flat-file config", func() {

		globalconf := `{
      "datastore": "kubernetes",
      "kubernetes": {
        "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
      },
      "log_file": "/tmp/whereabouts.log",
      "log_level": "debug",
      "gateway": "192.168.5.5"
    }`

		err := ioutil.WriteFile("/tmp/whereabouts.conf", []byte(globalconf), 0755)
		Expect(err).NotTo(HaveOccurred())

		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "configuration_path": "/tmp/whereabouts.conf",
        "type": "whereabouts",
        "range": "192.168.2.230/24",
        "range_start": "192.168.2.223",
        "gateway": "192.168.10.1",
        "leader_lease_duration": 3000,
        "leader_renew_deadline": 2000,
        "leader_retry_period": 1000
      }
      }`

		ipamconfig, _, err := LoadIPAMConfig([]byte(conf), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamconfig.LogLevel).To(Equal("debug"))
		Expect(ipamconfig.LogFile).To(Equal("/tmp/whereabouts.log"))
		Expect(ipamconfig.Range).To(Equal("192.168.2.0/24"))
		Expect(ipamconfig.RangeStart.String()).To(Equal("192.168.2.223"))
		// Gateway should remain unchanged from conf due to preference for primary config
		Expect(ipamconfig.Gateway).To(Equal(net.ParseIP("192.168.10.1")))
		Expect(ipamconfig.Datastore).To(Equal("kubernetes"))
		Expect(ipamconfig.Kubernetes.KubeConfigPath).To(Equal("/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"))

		Expect(ipamconfig.LeaderLeaseDuration).To(Equal(3000))
		Expect(ipamconfig.LeaderRenewDeadline).To(Equal(2000))
		Expect(ipamconfig.LeaderRetryPeriod).To(Equal(1000))

	})

})
