package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/testutils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	fakek8sclient "k8s.io/client-go/kubernetes/fake"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/allocate"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclientset "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned/fake"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	whereaboutstypes "github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const whereaboutsConfigFile = "whereabouts.kubeconfig"

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Whereabouts Suite",
		[]Reporter{})
}

func AllocateAndReleaseAddressesTest(ipVersion string, ipamConf *whereaboutstypes.IPAMConfig, expectedAddresses []string) {
	const (
		ifname       string = "eth0"
		nspath       string = "/some/where"
		cniVersion          = "0.3.1"
		podName             = "dummyPOD"
		podNamespace        = "dummyNS"
	)

	addressArgs := []*skel.CmdArgs{}

	cniConf, err := newCNINetConf(cniVersion, ipamConf)
	Expect(err).NotTo(HaveOccurred())
	Expect(ipamConf.IPRanges).NotTo(BeEmpty())
	wbClient := *kubernetes.NewKubernetesClient(
		fake.NewSimpleClientset(
			ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)),
		fakek8sclient.NewSimpleClientset(),
		0)
	for i := 0; i < len(expectedAddresses); i++ {
		args := &skel.CmdArgs{
			ContainerID: fmt.Sprintf("dummy-%d", i),
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   cniConf,
			Args:        cniArgs(podNamespace, podName),
		}
		client := mutateK8sIPAM(args.ContainerID, ipamConf, wbClient)

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, client, cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: ipVersion,
				Address: mustCIDR(expectedAddresses[i]),
				Gateway: ipamConf.Gateway,
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, client)
		})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		// Now, create the same thing again, and expect the same IP
		// That way we know it dealloced the IP and assigned it again.
		r, _, err = testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, client, cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())

		result, err = current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: ipVersion,
				Address: mustCIDR(expectedAddresses[i]),
				Gateway: ipamConf.Gateway,
			}))

		addressArgs = append(addressArgs, args)
	}

	for _, args := range addressArgs {
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		// And we'll release the IP again.
		err := testutils.CmdDelWithArgs(args, func() error {
			client := newK8sIPAM(
				args.ContainerID,
				ipamConf,
				fakek8sclient.NewSimpleClientset(),
				fake.NewSimpleClientset(
					ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))
			return cmdDel(args, client)
		})
		Expect(err).NotTo(HaveOccurred())
	}
}

var _ = Describe("Whereabouts operations", func() {
	const (
		podName      = "dummyPOD"
		podNamespace = "dummyNS"
	)

	var (
		tmpDir         string
		kubeConfigPath string
		k8sClient      *kubernetes.KubernetesIPAM
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("/tmp", "whereabouts")
		Expect(err).ToNot(HaveOccurred())
		kubeConfigPath = fmt.Sprintf("%s/%s", tmpDir, whereaboutsConfigFile)
		Expect(os.WriteFile(kubeConfigPath, kubeconfig(), fs.ModePerm)).To(Succeed())
	})

	AfterEach(func() {
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				panic("error cleaning up tmp dir. Cannot proceed with tests")
			}
		}()
	})

	It("allocates and releases addresses on ADD/DEL", func() {
		ipVersion := "4"
		ipRange := "192.168.1.0/24"
		ipGateway := "192.168.10.1"
		expectedAddress := "192.168.1.1/24"

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress})

		ipVersion = "6"
		ipRange = "2001::1/116"
		ipGateway = "2001::f:1"
		expectedAddress = "2001::1/116"

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress},
		)
	})

	It("allocates and releases addresses on ADD/DEL with a Kubernetes backend", func() {
		ipVersion := "4"
		ipRange := "192.168.1.11-192.168.1.23/24"
		ipGateway := "192.168.10.1"
		expectedAddress := "192.168.1.1/24"

		expectedAddresses := []string{
			"192.168.1.11/24",
			"192.168.1.12/24",
			"192.168.1.13/24",
			"192.168.1.14/24",
			"192.168.1.15/24",
			"192.168.1.16/24",
			"192.168.1.17/24",
			"192.168.1.18/24",
			"192.168.1.19/24",
			"192.168.1.20/24",
			"192.168.1.21/24",
			"192.168.1.22/24",
		}

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			expectedAddresses,
		)

		ipVersion = "6"
		ipRange = "2001::1/116"
		ipGateway = "2001::f:1"
		expectedAddress = "2001::1/116"
		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress},
		)
	})

	It("allocates and releases an IPv6 address with left-hand zeroes on ADD/DEL with a Kubernetes backend", func() {

		ipVersion := "6"
		ipRange := "fd::1/116"
		ipGateway := "fd::f:1"
		expectedAddress := "fd::1/116"

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress},
		)
	})

	It("allocates and releases an IPv6 range that ends with zeroes with a Kubernetes backend", func() {

		ipVersion := "6"
		ipRange := "2001:db8:480:603d:0304:0403:000:0000-2001:db8:480:603d:0304:0403:0000:0004/64"
		ipGateway := "2001:db8:480:603d::1"
		expectedAddress := "2001:db8:480:603d:0304:0403:000:0000/64"

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress},
		)

		ipRange = "2001:db8:5422:0005::-2001:db8:5422:0005:7fff:ffff:ffff:ffff/64"
		ipGateway = "2001:db8:5422:0005::1"
		expectedAddress = "2001:db8:5422:0005::/64"

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress},
		)
	})

	It("allocates IPv6 addresses with DNS-1123 conformant naming with a Kubernetes backend", func() {

		ipVersion := "6"
		ipRange := "fd00:0:0:10:0:0:3:1-fd00:0:0:10:0:0:3:6/64"
		ipGateway := "2001::f:1"
		expectedAddress := "fd00:0:0:10:0:0:3:1/64"

		AllocateAndReleaseAddressesTest(
			ipVersion,
			ipamConfig(podName, podNamespace, ipRange, ipGateway, kubeConfigPath),
			[]string{expectedAddress},
		)
	})

	It("excludes a range of addresses", func() {
		const (
			ifname string = "eth0"
			nspath string = "/some/where"
		)

		conf := fmt.Sprintf(`{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        %s,
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
    }`, configureBackend(tmpDir))

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
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
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("excludes a range of IPv6 addresses", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        "log_file" : "/tmp/whereabouts.log",
		"log_level" : "debug",
        %s,
        "range": "2001::1/116",
        "exclude": [
          "2001::0/128",
          "2001::1/128",
          "2001::2/128"
        ],
        "gateway": "2001::f:1",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ]
      }
    }`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "6",
				Address: mustCIDR("2001::3/116"),
				Gateway: net.ParseIP("2001::f:1"),
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, k8sClient)
		})

		Expect(err).NotTo(HaveOccurred())
	})

	It("excludes a range of IPv6 addresses, omitting broadcast", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        %s,
		"range": "caa5::0/112",
        "exclude": ["caa5::0/113"],
        "gateway": "2001::f:1",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ]
      }
    }`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		Expect(*result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "6",
				Address: mustCIDR("caa5::8000/112"),
				Gateway: net.ParseIP("2001::f:1"),
			}))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, k8sClient)
		})

		Expect(err).NotTo(HaveOccurred())
	})

	It("can still assign static parameters", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "whereabouts",
        %s,
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
    }`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
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
				Address: mustCIDR("192.168.1.33/28"),
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
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())

	})

	It("allocates an address using IPRanges notation", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
			  "log_level" : "debug",
			  %s,
			  "ipRanges": [{
			    "range": "192.168.10.1/24"
			  }]
			}
		}`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps

		Expect(result.IPs).NotTo(BeEmpty())
		Expect(result.IPs[0].Address).To(Equal(mustCIDR("192.168.10.1/24")))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates DualStack address using IPRanges notation", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
			  "log_level" : "debug",
			  %s,
			  "ipRanges": [{
			    "range": "192.168.10.1/24"
			  }, {
			    "range": "abcd::1/64"
			  }]
			}
		}`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).To(HaveLen(2))
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName),
				ipPool(ipamConf.IPRanges[1].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps

		Expect(result.IPs).To(HaveLen(2))
		Expect(result.IPs[0].Address).To(Equal(mustCIDR("192.168.10.1/24")))
		Expect(result.IPs[1].Address).To(Equal(mustCIDR("abcd::1/64")))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates addresses using both IPRanges and range notations", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
			  "log_level" : "debug",
			  %s,
			  "ipRanges": [{
			    "range": "192.168.10.1/24"
			  }],
			  "range": "abcd::1/64"
			}
		}`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).To(HaveLen(2))
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName),
				ipPool(ipamConf.IPRanges[1].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps

		Expect(result.IPs).To(HaveLen(2))
		Expect(result.IPs[0].Address).To(Equal(mustCIDR("abcd::1/64")))
		Expect(result.IPs[1].Address).To(Equal(mustCIDR("192.168.10.1/24")))

		// Release the IP
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates an address using start/end cidr notation", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
              "log_level" : "debug",
			  %s,
			  "range": "192.168.1.5-192.168.1.25/24",
			  "gateway": "192.168.10.1"
			}
		  }`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
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
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates an address using the range_start parameter", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
              "log_level" : "debug",
			  %s,
			  "range": "192.168.1.0/24",
			  "range_start": "192.168.1.5",
			  "gateway": "192.168.10.1"
			}
		  }`, backend)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		k8sClient = newK8sIPAM(
			args.ContainerID,
			ipamConf,
			fakek8sclient.NewSimpleClientset(),
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)))

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, k8sClient, cniVersion)
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
			return cmdDel(args, k8sClient)
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("allocates addresses using range_end as an upper limit", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		backend := fmt.Sprintf(`"kubernetes": {"kubeconfig": "%s"}`, kubeConfigPath)
		conf := fmt.Sprintf(`{
			"cniVersion": "0.3.1",
			"name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
			  "type": "whereabouts",
			  "log_file" : "/tmp/whereabouts.log",
			  "log_level" : "debug",
			  %s,
			  "range": "192.168.1.0/24",
			  "range_start": "192.168.1.5",
			  "range_end": "192.168.1.12",
			  "gateway": "192.168.10.1"
			}
		  }`, backend)

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		wbClient := *kubernetes.NewKubernetesClient(
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)),
			fakek8sclient.NewSimpleClientset(),
			0)

		var ipArgs []*skel.CmdArgs
		// allocate 8 IPs (192.168.1.5 - 192.168.1.12); the entirety of the pool defined above
		for i := 0; i < 8; i++ {
			args := &skel.CmdArgs{
				ContainerID: fmt.Sprintf("dummy-%d", i),
				Netns:       nspath,
				IfName:      ifname,
				StdinData:   []byte(conf),
				Args:        cniArgs(podNamespace, podName),
			}

			k8sClient = mutateK8sIPAM(args.ContainerID, ipamConf, wbClient)
			r, raw, err := testutils.CmdAddWithArgs(args, func() error {
				return cmdAdd(args, k8sClient, cniVersion)
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

			result, err := current.GetResult(r)
			Expect(err).NotTo(HaveOccurred())

			Expect(*result.IPs[0]).To(Equal(
				current.IPConfig{
					Version: "4",
					Address: mustCIDR(fmt.Sprintf("192.168.1.%d/24", 5+i)),
					Gateway: net.ParseIP("192.168.10.1"),
				}))
			ipArgs = append(ipArgs, args)
		}

		// assigning more IPs should result in error due to the defined range_start - range_end
		args := &skel.CmdArgs{
			ContainerID: "dummy-failure",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}
		_, _, err = testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient), "0.3.1")
		})
		Expect(err).To(HaveOccurred())
		// ensure the error is of the correct type
		switch e := errors.Unwrap(err); e.(type) {
		case allocate.AssignmentError:
		default:
			Fail(fmt.Sprintf("expected AssignmentError, got: %s", e))
		}

		// Release assigned IPs
		for _, args := range ipArgs {
			err := testutils.CmdDelWithArgs(args, func() error {
				return cmdDel(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient))
			})
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("detects IPv4 addresses used in other ranges, to allow for overlapping IP address ranges", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		// ----------------------------- range 1

		conf := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "datastore": "kubernetes",
		  "log_file" : "/tmp/whereabouts.log",
			"log_level" : "debug",
		  "kubernetes": {"kubeconfig": "%s"},
		  "range": "192.168.22.0/24"
		}
	  }`, kubeConfigPath)

		args := &skel.CmdArgs{
			ContainerID: "dummyfirstrange",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		wbClient := *kubernetes.NewKubernetesClient(
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)),
			fakek8sclient.NewSimpleClientset(),
			0)

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient), cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.22.1/24"),
			}))

		// ----------------------------- range 2

		confsecond := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "datastore": "kubernetes",
		  "log_file" : "/tmp/whereabouts.log",
			"log_level" : "debug",
		  "kubernetes": {"kubeconfig": "%s"},
		  "range": "192.168.22.0/28"
		}
	  }`, kubeConfigPath)

		argssecond := &skel.CmdArgs{
			ContainerID: "dummysecondrange",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(confsecond),
			Args:        cniArgs(podNamespace, podName),
		}

		secondIPAMConf, secondCNIVersion, err := config.LoadIPAMConfig([]byte(confsecond), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())

		// Allocate the IP
		r, raw, err = testutils.CmdAddWithArgs(argssecond, func() error {
			return cmdAdd(argssecond, mutateK8sIPAM(argssecond.ContainerID, secondIPAMConf, wbClient), secondCNIVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err = current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.22.2/28"),
			}))

		// ------------------------ deallocation

		// Release the IP, first range
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient))
		})
		Expect(err).NotTo(HaveOccurred())

		// Release the IP, second range
		err = testutils.CmdDelWithArgs(argssecond, func() error {
			return cmdDel(argssecond, mutateK8sIPAM(argssecond.ContainerID, secondIPAMConf, wbClient))
		})
		Expect(err).NotTo(HaveOccurred())

	})

	It("detects IPv6 addresses used in other ranges, to allow for overlapping IP address ranges", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		// ----------------------------- range 1

		conf := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "datastore": "kubernetes",
		  "log_file" : "/tmp/whereabouts.log",
			"log_level" : "debug",
		  "kubernetes": {"kubeconfig": "%s"},
		  "range": "2001::2:3:0/124"
		}
	  }`, kubeConfigPath)

		args := &skel.CmdArgs{
			ContainerID: "dummyfirstrange",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		wbClient := *kubernetes.NewKubernetesClient(
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)),
			fakek8sclient.NewSimpleClientset(),
			0)

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient), cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "6",
				Address: mustCIDR("2001::2:3:1/124"),
			}))

		// ----------------------------- range 2

		confsecond := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "datastore": "kubernetes",
		  "log_file" : "/tmp/whereabouts.log",
			"log_level" : "debug",
		  "kubernetes": {"kubeconfig": "%s"},
		  "range": "2001::2:3:0/126"
		}
	  }`, kubeConfigPath)

		argssecond := &skel.CmdArgs{
			ContainerID: "dummysecondrange",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(confsecond),
			Args:        cniArgs(podNamespace, podName),
		}

		secondIPAMConf, secondCNIVersion, err := config.LoadIPAMConfig([]byte(confsecond), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())

		// Allocate the IP
		r, raw, err = testutils.CmdAddWithArgs(argssecond, func() error {
			return cmdAdd(argssecond, mutateK8sIPAM(argssecond.ContainerID, secondIPAMConf, wbClient), secondCNIVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err = current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "6",
				Address: mustCIDR("2001::2:3:2/126"),
			}))

		// ------------------------ deallocation

		// Release the IP, first range
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient))
		})
		Expect(err).NotTo(HaveOccurred())

		// Release the IP, second range
		err = testutils.CmdDelWithArgs(argssecond, func() error {
			return cmdDel(argssecond, mutateK8sIPAM(argssecond.ContainerID, secondIPAMConf, wbClient))
		})

		Expect(err).NotTo(HaveOccurred())
	})

	It("allows IP collisions across ranges when enable_overlapping_ranges is set to false", func() {
		const ifname string = "eth0"
		const nspath string = "/some/where"

		// ----------------------------- range 1

		conf := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "datastore": "kubernetes",
		  "log_file" : "/tmp/whereabouts.log",
			"log_level" : "debug",
		  "kubernetes": {"kubeconfig": "%s"},
		  "enable_overlapping_ranges": false,
		  "range": "192.168.33.0/24"
		}
	  }`, kubeConfigPath)

		args := &skel.CmdArgs{
			ContainerID: "dummyfirstrange",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(conf),
			Args:        cniArgs(podNamespace, podName),
		}

		ipamConf, cniVersion, err := config.LoadIPAMConfig([]byte(conf), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConf.IPRanges).NotTo(BeEmpty())
		wbClient := *kubernetes.NewKubernetesClient(
			fake.NewSimpleClientset(
				ipPool(ipamConf.IPRanges[0].Range, podNamespace, ipamConf.NetworkName)),
			fakek8sclient.NewSimpleClientset(),
			0)

		// Allocate the IP
		r, raw, err := testutils.CmdAddWithArgs(args, func() error {
			return cmdAdd(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient), cniVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err := current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.33.1/24"),
			}))

		// ----------------------------- range 2

		confsecond := fmt.Sprintf(`{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
		  "type": "whereabouts",
		  "datastore": "kubernetes",
		  "log_file" : "/tmp/whereabouts.log",
			"log_level" : "debug",
		  "kubernetes": {"kubeconfig": "%s"},
		  "range": "192.168.33.0/28"
		}
	  }`, kubeConfigPath)

		argssecond := &skel.CmdArgs{
			ContainerID: "dummysecondrange",
			Netns:       nspath,
			IfName:      ifname,
			StdinData:   []byte(confsecond),
			Args:        cniArgs(podNamespace, podName),
		}

		secondIPAMConf, secondCNIVersion, err := config.LoadIPAMConfig([]byte(confsecond), cniArgs(podNamespace, podName), "")
		Expect(err).NotTo(HaveOccurred())

		// Allocate the IP
		r, raw, err = testutils.CmdAddWithArgs(argssecond, func() error {
			return cmdAdd(argssecond, mutateK8sIPAM(argssecond.ContainerID, secondIPAMConf, wbClient), secondCNIVersion)
		})
		Expect(err).NotTo(HaveOccurred())
		// fmt.Printf("!bang raw: %s\n", raw)
		Expect(strings.Index(string(raw), "\"version\":")).Should(BeNumerically(">", 0))

		result, err = current.GetResult(r)
		Expect(err).NotTo(HaveOccurred())

		// Gomega is cranky about slices with different caps
		ExpectWithOffset(1, *result.IPs[0]).To(Equal(
			current.IPConfig{
				Version: "4",
				Address: mustCIDR("192.168.33.1/28"),
			}))

		// ------------------------ deallocation

		// Release the IP, first range
		err = testutils.CmdDelWithArgs(args, func() error {
			return cmdDel(args, mutateK8sIPAM(args.ContainerID, ipamConf, wbClient))
		})
		Expect(err).NotTo(HaveOccurred())

		// Release the IP, second range
		err = testutils.CmdDelWithArgs(argssecond, func() error {
			return cmdDel(argssecond, mutateK8sIPAM(argssecond.ContainerID, secondIPAMConf, wbClient))
		})
		Expect(err).NotTo(HaveOccurred())
	})
})

func cniArgs(podNamespace string, podName string) string {
	return fmt.Sprintf("IgnoreUnknown=1;K8S_POD_NAMESPACE=%s;K8S_POD_NAME=%s", podNamespace, podName)
}

func newK8sIPAM(containerID string, ipamConf *whereaboutstypes.IPAMConfig, k8sCoreClient k8sclient.Interface, wbClient wbclientset.Interface) *kubernetes.KubernetesIPAM {
	k8sIPAM, err := kubernetes.NewKubernetesIPAMWithNamespace(containerID, *ipamConf, ipamConf.PodNamespace)
	if err != nil {
		return nil
	}
	k8sIPAM.Client = *kubernetes.NewKubernetesClient(wbClient, k8sCoreClient, 0)
	return k8sIPAM
}

func mutateK8sIPAM(containerID string, ipamConf *whereaboutstypes.IPAMConfig, client kubernetes.Client) *kubernetes.KubernetesIPAM {
	k8sIPAM, err := kubernetes.NewKubernetesIPAMWithNamespace(containerID, *ipamConf, ipamConf.PodNamespace)
	if err != nil {
		return nil
	}
	k8sIPAM.Client = client
	return k8sIPAM
}

func mustCIDR(s string) net.IPNet {
	ip, n, err := net.ParseCIDR(s)
	n.IP = ip
	if err != nil {
		Fail(err.Error())
	}
	return *n
}

func ipamConfig(podName string, namespace string, ipRange string, gw string, kubeconfigPath string) *whereaboutstypes.IPAMConfig {
	const (
		cniVersion = "0.3.1"
		netName    = "net1"
	)

	ipamConf := &whereaboutstypes.IPAMConfig{
		Name:                netName,
		Type:                "whereabouts",
		Range:               ipRange,
		GatewayStr:          gw,
		LeaderRenewDeadline: 5,
		LeaderLeaseDuration: 10,
		LeaderRetryPeriod:   2,
		Kubernetes: whereaboutstypes.KubernetesConfig{
			KubeConfigPath: kubeconfigPath,
		},
	}
	bytes, err := json.Marshal(&whereaboutstypes.Net{
		Name:       netName,
		CNIVersion: cniVersion,
		IPAM:       ipamConf,
	})
	if err != nil {
		return nil
	}

	ipamConfWithDefaults, _, err := config.LoadIPAMConfig(bytes, cniArgs(namespace, podName), "")
	if err != nil {
		return nil
	}
	return ipamConfWithDefaults
}

func configureBackend(dir string) string {
	return fmt.Sprintf(
		`"kubernetes": {"kubeconfig": "%s"}`,
		fmt.Sprintf("%s/%s", dir, whereaboutsConfigFile))
}

func kubeconfig() []byte {
	return []byte(`
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJzVENDQVZlZ0F3SUJBZ0lCQURBS0JnZ3Foa2pPUFFRREFqQXdNUkF3RGdZRFZRUUtFd2RsYm5aMFpYTjAKTVJ3d0dnWURWUVFERXhObGJuWjBaWE4wTFdWdWRtbHliMjV0Wlc1ME1CNFhEVEl5TURneE5qRTBOVGN3TmxvWApEVE15TURneE16RTBOVGN3Tmxvd01ERVFNQTRHQTFVRUNoTUhaVzUyZEdWemRERWNNQm9HQTFVRUF4TVRaVzUyCmRHVnpkQzFsYm5acGNtOXViV1Z1ZERCWk1CTUdCeXFHU000OUFnRUdDQ3FHU000OUF3RUhBMElBQkJkVzBDKy8KZEpvWE5NOXpreVBOaW5kVlZleUppaVd6MkFLQnlKSjM0eUVWN1lpMVc1ZlhCNXpUZGY5dUhVOUVmZGRpN2NHcAo2Sm5qMTl1N2I5QVQySWVqWWpCZ01BNEdBMVVkRHdFQi93UUVBd0lDcERBUEJnTlZIUk1CQWY4RUJUQURBUUgvCk1CMEdBMVVkRGdRV0JCUit1WU54TEEyMWNsSGdlS082N2dqV3drWThpakFlQmdOVkhSRUVGekFWZ2hObGJuWjAKWlhOMExXVnVkbWx5YjI1dFpXNTBNQW9HQ0NxR1NNNDlCQU1DQTBnQU1FVUNJRU5ZWmxUSklqWlZWUUt5ZDN2YgptcmJBWWpsWFRrUDlzTjVmT1BIWjM0UHZBaUVBdkZESk8xbmNYTVFCWW01RTNhdGpVOFRBSG9ma2EzK0IzM2JkCjhMNnNaZzg9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K
    server: https://127.0.0.1:39165
  name: envtest
contexts:
- context:
    cluster: envtest
    user: envtest
  name: envtest
current-context: envtest
kind: Config
preferences: {}
users:
- name: envtest
  user:
    client-certificate-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJrakNDQVRpZ0F3SUJBZ0lCQVRBS0JnZ3Foa2pPUFFRREFqQXdNUkF3RGdZRFZRUUtFd2RsYm5aMFpYTjAKTVJ3d0dnWURWUVFERXhObGJuWjBaWE4wTFdWdWRtbHliMjV0Wlc1ME1CNFhEVEl5TURneE5qRTBOVGN4TUZvWApEVEl5TURneU16RTBOVGN4TUZvd0t6RVhNQlVHQTFVRUNoTU9jM2x6ZEdWdE9tMWhjM1JsY25NeEVEQU9CZ05WCkJBTVRCMlJsWm1GMWJIUXdXVEFUQmdjcWhrak9QUUlCQmdncWhrak9QUU1CQndOQ0FBVFFiUnF3a1NQeGxkZUQKSDh0WElDN3pRVjN1MU90TE14SStoa1VsN3puaEdXWmh1M1dSV1V4SEFVKzVyY2xUMHlxeEVzUDZ6TFVyNFk1bApEVEE2cDVJeW8wZ3dSakFPQmdOVkhROEJBZjhFQkFNQ0JhQXdFd1lEVlIwbEJBd3dDZ1lJS3dZQkJRVUhBd0l3Ckh3WURWUjBqQkJnd0ZvQVUzQis0dThmOWZkTmxhNU1Td2xPVHlvYmdEVmN3Q2dZSUtvWkl6ajBFQXdJRFNBQXcKUlFJZ2V4b0JWS2pYenppemlKUWtma2F3c2w5aUJWQkl5ZWxXK2dRK2JPV2RFZ0lDSVFEa3lGcjJCR0tSei9lcAp3NGhTSmJDVmtZNjVJdE5ZZ3RKMVJaOGtEeXE2bXc9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    client-key-data: LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCk1JR0hBZ0VBTUJNR0J5cUdTTTQ5QWdFR0NDcUdTTTQ5QXdFSEJHMHdhd0lCQVFRZ1FwcThkWVB0UlNOa2tUMHQKakh1SXNMYnpCaGU4bkV1R0xzU2x2MDNVVzFhaFJBTkNBQVRRYlJxd2tTUHhsZGVESDh0WElDN3pRVjN1MU90TApNeEkraGtVbDd6bmhHV1podTNXUldVeEhBVSs1cmNsVDB5cXhFc1A2ekxVcjRZNWxEVEE2cDVJeQotLS0tLUVORCBQUklWQVRFIEtFWS0tLS0tCg==
`)
}

func ipPool(ipRange string, namespace string, networkName string, podReferences ...string) *v1alpha1.IPPool {
	return &v1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            kubernetes.IPPoolName(kubernetes.PoolIdentifier{IpRange: ipRange, NetworkName: networkName}),
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Spec: v1alpha1.IPPoolSpec{
			Range:       ipRange,
			Allocations: allocations(podReferences...),
		},
	}
}

func allocations(podReferences ...string) map[string]v1alpha1.IPAllocation {
	poolAllocations := map[string]v1alpha1.IPAllocation{}
	for i, podRef := range podReferences {
		poolAllocations[fmt.Sprintf("%d", i)] = v1alpha1.IPAllocation{
			ContainerID: "",
			PodRef:      podRef,
		}
	}
	return poolAllocations
}

func newCNINetConf(cniVersion string, ipamConfig *whereaboutstypes.IPAMConfig) ([]byte, error) {
	netConf := whereaboutstypes.NetConfList{
		CNIVersion:   cniVersion,
		Name:         ipamConfig.Name,
		DisableCheck: true,
		Plugins: []*whereaboutstypes.Net{
			{
				Name:       ipamConfig.Name,
				CNIVersion: cniVersion,
				IPAM:       ipamConfig,
			},
		},
	}

	return json.Marshal(&netConf)
}
