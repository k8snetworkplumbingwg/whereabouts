package main

import (
	// "fmt"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/testutils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestWhereabouts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

func AllocateAndReleaseAddressesTest(ipVersion string, ipRange string, ipGateway string, expectedAddress string) {
	const ifname string = "eth0"
	const nspath string = "/some/where"

	conf := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "log_file" : "/tmp/whereabouts.log",
				  "log_level" : "debug",
		  "etcd_host": "127.0.0.1:2379",
		  "range": "%s",
		  "gateway": "%s",
		  "routes": [
			{ "dst": "0.0.0.0/0" }
		  ]
		}
	  }`, ipRange, ipGateway)

	args := &skel.CmdArgs{
		ContainerID: "dummy",
		Netns:       nspath,
		IfName:      ifname,
		StdinData:   []byte(conf),
	}

	// Allocate the IP
	r, raw, err := testutils.CmdAddWithArgs(args, func() error {
		return cmdAdd(args)
	})
	Expect(err).NotTo(HaveOccurred())
	// fmt.Printf("!bang raw: %s\n", raw)
	Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

	result, err := current.GetResult(r)
	Expect(err).NotTo(HaveOccurred())

	// Gomega is cranky about slices with different caps
	Expect(*result.IPs[0]).To(Equal(
		current.IPConfig{
			Version: ipVersion,
			Address: mustCIDR(expectedAddress),
			Gateway: net.ParseIP(ipGateway),
		}))

	// Release the IP
	err = testutils.CmdDelWithArgs(args, func() error {
		return cmdDel(args)
	})
	Expect(err).NotTo(HaveOccurred())

	// Now, create the same thing again, and expect the same IP
	// That way we know it dealloced the IP and assigned it again.
	r, _, err = testutils.CmdAddWithArgs(args, func() error {
		return cmdAdd(args)
	})
	Expect(err).NotTo(HaveOccurred())

	result, err = current.GetResult(r)
	Expect(err).NotTo(HaveOccurred())

	Expect(*result.IPs[0]).To(Equal(
		current.IPConfig{
			Version: ipVersion,
			Address: mustCIDR(expectedAddress),
			Gateway: net.ParseIP(ipGateway),
		}))

	// And we'll release the IP again.
	err = testutils.CmdDelWithArgs(args, func() error {
		return cmdDel(args)
	})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("Whereabouts operations", func() {
	It("allocates and releases addresses on ADD/DEL", func() {
		ipVersion := "4"
		ipRange := "192.168.1.0/24"
		ipGateway := "192.168.10.1"
		expectedAddress := "192.168.1.1/24"

		AllocateAndReleaseAddressesTest(ipVersion, ipRange, ipGateway, expectedAddress)

		ipVersion = "6"
		ipRange = "2001::1/116"
		ipGateway = "2001::f:1"
		expectedAddress = "2001::1/116"

		AllocateAndReleaseAddressesTest(ipVersion, ipRange, ipGateway, expectedAddress)
	})

	It("excludes a range of addresses", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        "log_file" : "/tmp/whereabouts.log",
				"log_level" : "debug",
        "etcd_host": "127.0.0.1:2379",
        "range": "192.168.1.0/24",
        "exclude": [
          "192.168.1.0/28",
          "192.168.1.16/28"
        ],
        "gateway": "192.168.10.1",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ]
      }
    }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.1.32/24"),
				Gateway: net.ParseIP("192.168.10.1"),
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args)
		})
		Expect(err).NotTo(HaveOccurred())

	})

	It("can still assign static parameters", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        "etcd_host": "127.0.0.1:2379",
        "range": "192.168.1.44/28",
        "gateway": "192.168.1.1",
        "addresses": [ {
            "address": "10.10.0.1/24",
            "gateway": "10.10.0.254"
          },
          {
            "address": "3ffe:ffff:0:01ff::1/64",
            "gateway": "3ffe:ffff:0::1"
          }],
        "routes": [
          { "dst": "0.0.0.0/0" },
          { "dst": "192.168.0.0/16", "gw": "10.10.5.1" },
          { "dst": "3ffe:ffff:0:01ff::1/64" }],
        "dns": {
          "nameservers" : ["8.8.8.8"],
          "domain": "example.com",
          "search": [ "example.com" ]
        }
      }
    }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps

		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.1.44/28"),
				Gateway: net.ParseIP("192.168.1.1"),
			}))

		Expect(*result.IPs[1]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("10.10.0.1/24"),
				Gateway: net.ParseIP("10.10.0.254"),
			}))

		Expect(*result.IPs[2]).To(Equal(
			current.IPConfig{
				Version: "6",
				Address: mustCIDR("3ffe:ffff:0:01ff::1/64"),
				Gateway: net.ParseIP("3ffe:ffff:0::1"),
			},
		))
		Expect(len(result.IPs)).To(Equal(3))

		Expect(result.Routes).To(Equal([]*types.Route{
			{Dst: mustCIDR("0.0.0.0/0")},
			{Dst: mustCIDR("192.168.0.0/16"), GW: net.ParseIP("10.10.5.1")},
			{Dst: mustCIDR("3ffe:ffff:0:01ff::1/64")},
		}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args)
		})
		Expect(err).NotTo(HaveOccurred())

	})

	It("allocates an address using start/end cidr notation", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
					  "log_level" : "debug",
			  "etcd_host": "127.0.0.1:2379",
			  "range": "192.168.1.5-192.168.1.25/24",
			  "gateway": "192.168.10.1"
			}
		  }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps

		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.1.5/24"),
				Gateway: net.ParseIP("192.168.10.1"),
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates an address using the range_start parameter", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
					  "log_level" : "debug",
			  "etcd_host": "127.0.0.1:2379",
			  "range": "192.168.1.0/24",
			  "range_start": "192.168.1.5",
			  "gateway": "192.168.10.1"
			}
		  }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps

		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.1.5/24"),
				Gateway: net.ParseIP("192.168.10.1"),
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails when there's an invalid range specified", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		// 192.168.1.5 should not be a member of 192.168.2.25/28
		conf := `{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
					  "log_level" : "debug",
			  "etcd_host": "127.0.0.1:2379",
			  "range": "192.168.1.5-192.168.2.25/28",
			  "gateway": "192.168.10.1"
			}
		  }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		_, _, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("invalid range start"))
	})

	It("fails when there's bad JSON", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        asdf
      }
    }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		_, _, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("LoadIPAMConfig - JSON Parsing Error"))
	})

	It("fails when there's invalid etcd credentials", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
		  "cniVersion": "0.3.1",
		  "name": "mynet",
		  "type": "ipvlan",
		  "master": "foo0",
		  "ipam": {
			"type": "whereabouts",
			"log_file" : "/tmp/whereabouts.log",
					"log_level" : "debug",
			"etcd_host": "127.0.0.1:2379",
			"etcd_username": "fakeuser",
			"etcd_password": "fakepassword",
			"etcd_key_file": "/tmp/fake/path/to/key",
			"etcd_cert_file": "/tmp/fake/path/to/cert",
			"range": "192.168.1.0/24",
			"gateway": "192.168.10.1",
			"routes": [
			  { "dst": "0.0.0.0/0" }
			]
		  }
		}`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
		}

		// Allocate the IP
		_, _, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args)
		})
		Expect(err).To(HaveOccurred())
	})

})

func mustCIDR(s string) net.IPNet {
	ip, n, err := net.ParseCIDR(s)
	n.IP = ip
	if err != nil {
		Fail(err.Error())
	}

	return *n
}
