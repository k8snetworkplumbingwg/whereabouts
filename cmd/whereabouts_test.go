package main

import (
	"fmt"
	"net"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/testutils"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestWhereabouts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("Whereabouts operations", func() {
	It("allocates and releases addresses without addresses on ADD/DEL", func() {
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
        "etcd_host": "192.168.2.100",
        "range": "192.168.1.0/24",
        "gateway": "192.168.1.1",
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
		fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.2.200/32"),
				Gateway: net.ParseIP("192.168.1.1"),
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates and releases addresses with all static parameters", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        "etcd_host": "192.168.2.100",
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

		fmt.Println("!bang THIS IS A TEST.")

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

		// This one is the bogus stubbed in IP address.
		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.2.200/32"),
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
		Expect(err.Error()).To(HavePrefix("invalid character"))

	})

	// It("doesn't error when passed an unknown ID on DEL", func() {
	//   const ifname string = "eth0"
	//   const nspath string = "/some/where"

	//   conf := `{
	//     "cniVersion": "0.3.0",
	//     "name": "mynet",
	//     "type": "ipvlan",
	//     "master": "foo0",
	//     "ipam": {
	//       "type": "static",
	//       "addresses": [ {
	//         "address": "10.10.0.1/24",
	//         "gateway": "10.10.0.254"
	//       },
	//       {
	//         "address": "3ffe:ffff:0:01ff::1/64",
	//         "gateway": "3ffe:ffff:0::1"
	//       }],
	//       "routes": [
	//       { "dst": "0.0.0.0/0" },
	//       { "dst": "192.168.0.0/16", "gw": "10.10.5.1" },
	//       { "dst": "3ffe:ffff:0:01ff::1/64" }],
	//       "dns": {
	//         "nameservers" : ["8.8.8.8"],
	//         "domain": "example.com",
	//         "search": [ "example.com" ]
	//       }}}`

	//   args := &skel.CmdArgs{
	//     ContainerID: "dummy",
	//     Netns:       nspath,
	//     IfName:      ifname,
	//     StdinData:   []byte(conf),
	//   }

	//   // Release the IP
	//   err := testutils.CmdDelWithArgs(args, func() error {
	//     return cmdDel(args)
	//   })
	//   Expect(err).NotTo(HaveOccurred())
	// })

	// It("allocates and releases addresses with ADD/DEL, with ENV variables", func() {
	//   const ifname string = "eth0"
	//   const nspath string = "/some/where"

	//   conf := `{
	//     "cniVersion": "0.3.1",
	//     "name": "mynet",
	//     "type": "ipvlan",
	//     "master": "foo0",
	//     "ipam": {
	//       "type": "static",
	//       "routes": [
	//         { "dst": "0.0.0.0/0" },
	//         { "dst": "192.168.0.0/16", "gw": "10.10.5.1" }],
	//       "dns": {
	//         "nameservers" : ["8.8.8.8"],
	//         "domain": "example.com",
	//         "search": [ "example.com" ]
	//       }
	//     }
	//   }`

	//   args := &skel.CmdArgs{
	//     ContainerID: "dummy",
	//     Netns:       nspath,
	//     IfName:      ifname,
	//     StdinData:   []byte(conf),
	//     Args:        "IP=10.10.0.1/24;GATEWAY=10.10.0.254",
	//   }

	//   // Allocate the IP
	//   r, raw, err := testutils.CmdAddWithArgs(args, func() error {
	//     return cmdAdd(args)
	//   })
	//   Expect(err).NotTo(HaveOccurred())
	//   Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

	//   result, err := current.GetResult(r)
	//   Expect(err).NotTo(HaveOccurred())

	//   // Gomega is cranky about slices with different caps
	//   Expect(*result.IPs[0]).To(Equal(
	//     current.IPConfig{
	//       Version: "4",
	//       Address: mustCIDR("10.10.0.1/24"),
	//       Gateway: net.ParseIP("10.10.0.254"),
	//     }))

	//   Expect(len(result.IPs)).To(Equal(1))

	//   Expect(result.Routes).To(Equal([]*types.Route{
	//     {Dst: mustCIDR("0.0.0.0/0")},
	//     {Dst: mustCIDR("192.168.0.0/16"), GW: net.ParseIP("10.10.5.1")},
	//   }))

	//   // Release the IP
	//   err = testutils.CmdDelWithArgs(args, func() error {
	//     return cmdDel(args)
	//   })
	//   Expect(err).NotTo(HaveOccurred())
	// })

	// It("allocates and releases multiple addresses with ADD/DEL, with ENV variables", func() {
	//   const ifname string = "eth0"
	//   const nspath string = "/some/where"

	//   conf := `{
	//     "cniVersion": "0.3.1",
	//     "name": "mynet",
	//     "type": "ipvlan",
	//     "master": "foo0",
	//     "ipam": {
	//       "type": "static"
	//     }
	//   }`

	//   args := &skel.CmdArgs{
	//     ContainerID: "dummy",
	//     Netns:       nspath,
	//     IfName:      ifname,
	//     StdinData:   []byte(conf),
	//     Args:        "IP=10.10.0.1/24,11.11.0.1/24;GATEWAY=10.10.0.254",
	//   }

	//   // Allocate the IP
	//   r, raw, err := testutils.CmdAddWithArgs(args, func() error {
	//     return cmdAdd(args)
	//   })
	//   Expect(err).NotTo(HaveOccurred())
	//   Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

	//   result, err := current.GetResult(r)
	//   Expect(err).NotTo(HaveOccurred())

	//   // Gomega is cranky about slices with different caps
	//   Expect(*result.IPs[0]).To(Equal(
	//     current.IPConfig{
	//       Version: "4",
	//       Address: mustCIDR("10.10.0.1/24"),
	//       Gateway: net.ParseIP("10.10.0.254"),
	//     }))
	//   Expect(*result.IPs[1]).To(Equal(
	//     current.IPConfig{
	//       Version: "4",
	//       Address: mustCIDR("11.11.0.1/24"),
	//       Gateway: nil,
	//     }))

	//   Expect(len(result.IPs)).To(Equal(2))

	//   // Release the IP
	//   err = testutils.CmdDelWithArgs(args, func() error {
	//     return cmdDel(args)
	//   })
	//   Expect(err).NotTo(HaveOccurred())
	// })
})

func mustCIDR(s string) net.IPNet {
	ip, n, err := net.ParseCIDR(s)
	n.IP = ip
	if err != nil {
		Fail(err.Error())
	}

	return *n
}
