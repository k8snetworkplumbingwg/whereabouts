package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

func TestAllocate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}

var _ = Describe("Allocation operations", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "whereabouts")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

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
          "kubernetes": {
            "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
          },
          "range": "192.168.1.5-192.168.1.25/24",
          "gateway": "192.168.10.1"
        }
      }`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(conf), 0755)).To(Succeed())

		ipamconfig, _, err := LoadIPAMConfig([]byte(conf), "", confPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamconfig.LogLevel).To(Equal("debug"))
		Expect(ipamconfig.LogFile).To(Equal("/tmp/whereabouts.log"))
		Expect(ipamconfig.IPRanges[0].Range).To(Equal("192.168.1.0/24"))
		Expect(ipamconfig.IPRanges[0].RangeStart).To(Equal(net.ParseIP("192.168.1.5")))
		Expect(ipamconfig.IPRanges[0].RangeEnd).To(Equal(net.ParseIP("192.168.1.25")))
		Expect(ipamconfig.Gateway).To(Equal(net.ParseIP("192.168.10.1")))
		Expect(ipamconfig.LeaderLeaseDuration).To(Equal(1500))
		Expect(ipamconfig.LeaderRenewDeadline).To(Equal(1000))
		Expect(ipamconfig.LeaderRetryPeriod).To(Equal(500))

	})

	It("throws an error when no flat-files are found", func() {
		_, _, err := GetFlatIPAM(true, &types.IPAMConfig{})
		Expect(err).To(MatchError(NewFileNotFoundError()))
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

		err := os.WriteFile("/tmp/whereabouts.conf", []byte(globalconf), 0755)
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
		Expect(ipamconfig.IPRanges[0].Range).To(Equal("192.168.2.0/24"))
		Expect(ipamconfig.IPRanges[0].RangeStart.String()).To(Equal("192.168.2.223"))
		// Gateway should remain unchanged from conf due to preference for primary config
		Expect(ipamconfig.Gateway).To(Equal(net.ParseIP("192.168.10.1")))
		Expect(ipamconfig.Kubernetes.KubeConfigPath).To(Equal("/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"))

		Expect(ipamconfig.LeaderLeaseDuration).To(Equal(3000))
		Expect(ipamconfig.LeaderRenewDeadline).To(Equal(2000))
		Expect(ipamconfig.LeaderRetryPeriod).To(Equal(1000))
	})

	It("overlapping range can be set", func() {
		var globalConf = `{
			"datastore": "kubernetes",
			"kubernetes": {
				"kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
			},
			"log_file": "/tmp/whereabouts.log",
			"log_level": "debug",
			"gateway": "192.168.5.5",
			"enable_overlapping_ranges": false
		}`
		Expect(os.WriteFile("/tmp/whereabouts.conf", []byte(globalConf), 0755)).To(Succeed())

		ipamconfig, _, err := LoadIPAMConfig([]byte(generateIPAMConfWithOverlappingRanges()), "")
		Expect(err).NotTo(HaveOccurred())

		Expect(ipamconfig.OverlappingRanges).To(BeTrue())
	})

	It("overlapping range can be disabled", func() {
		var globalConf = `{
			"datastore": "kubernetes",
			"kubernetes": {
				"kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
			},
			"log_file": "/tmp/whereabouts.log",
			"log_level": "debug",
			"gateway": "192.168.5.5",
			"enable_overlapping_ranges": true
		}`
		Expect(os.WriteFile("/tmp/whereabouts.conf", []byte(globalConf), 0755)).To(Succeed())

		ipamconfig, _, err := LoadIPAMConfig([]byte(generateIPAMConfWithoutOverlappingRanges()), "")
		Expect(err).NotTo(HaveOccurred())

		Expect(ipamconfig.OverlappingRanges).To(BeFalse())
	})

	It("can load a config list", func() {
		conf := `{
        "cniVersion": "0.3.0",
        "disableCheck": true,
        "plugins": [
            {
                "type": "macvlan",
                "master": "eth0",
                "mode": "bridge",
                "ipam": {
                    "type": "whereabouts",
                    "leader_lease_duration": 1500,
                    "leader_renew_deadline": 1000,
                    "leader_retry_period": 500,
                    "range": "192.168.1.5-192.168.1.25/24",
                    "gateway": "192.168.10.1",
                    "log_level": "debug",
                    "log_file": "/tmp/whereabouts.log",
					"kubernetes": {
					  "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
					}
                }
            }
        ]
    }`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(conf), 0755)).To(Succeed())

		ipamconfig, err := LoadIPAMConfiguration([]byte(conf), "", confPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamconfig.LogLevel).To(Equal("debug"))
		Expect(ipamconfig.LogFile).To(Equal("/tmp/whereabouts.log"))
		Expect(ipamconfig.IPRanges[0].Range).To(Equal("192.168.1.0/24"))
		Expect(ipamconfig.IPRanges[0].RangeStart).To(Equal(net.ParseIP("192.168.1.5")))
		Expect(ipamconfig.IPRanges[0].RangeEnd).To(Equal(net.ParseIP("192.168.1.25")))
		Expect(ipamconfig.Gateway).To(Equal(net.ParseIP("192.168.10.1")))
		Expect(ipamconfig.LeaderLeaseDuration).To(Equal(1500))
		Expect(ipamconfig.LeaderRenewDeadline).To(Equal(1000))
		Expect(ipamconfig.LeaderRetryPeriod).To(Equal(500))
	})

	It("throws an error when passed a non-whereabouts IPAM config", func() {
		const wrongPluginType = "static"
		conf := fmt.Sprintf(`{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
      "ipam": {
        "type": "%s"
      }
      }`, wrongPluginType)

		_, _, err := LoadIPAMConfig([]byte(conf), "")
		Expect(err).To(MatchError(&InvalidPluginError{ipamType: wrongPluginType}))
	})

	It("allows for leading zeroes in the range in start/end range format", func() {
		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
        "ipam": {
          "type": "whereabouts",
          "log_file" : "/tmp/whereabouts.log",
          "log_level" : "debug",
          "kubernetes": {
            "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
          },
          "range": "00192.00168.1.5-000000192.168.1.25/24",
          "gateway": "192.168.10.1"
        }
      }`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(conf), 0755)).To(Succeed())

		ipamConfig, _, err := LoadIPAMConfig([]byte(conf), "", confPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConfig.IPRanges[0].Range).To(Equal("192.168.1.0/24"))
		Expect(ipamConfig.IPRanges[0].RangeStart).To(Equal(net.ParseIP("192.168.1.5")))
		Expect(ipamConfig.IPRanges[0].RangeEnd).To(Equal(net.ParseIP("192.168.1.25")))
	})

	It("allows for leading zeroes in the range", func() {
		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
        "ipam": {
          "type": "whereabouts",
          "log_file" : "/tmp/whereabouts.log",
          "log_level" : "debug",
          "kubernetes": {
            "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
          },
          "range": "00192.00168.1.0/24",
          "gateway": "192.168.10.1"
        }
      }`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(conf), 0755)).To(Succeed())

		ipamConfig, _, err := LoadIPAMConfig([]byte(conf), "", confPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConfig.IPRanges[0].Range).To(Equal("192.168.1.0/24"))
		Expect(ipamConfig.IPRanges[0].RangeStart).To(Equal(net.ParseIP("192.168.1.0")))
	})

	It("allows for leading zeroes in the range when the start range is provided", func() {
		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
        "ipam": {
          "type": "whereabouts",
          "log_file" : "/tmp/whereabouts.log",
          "log_level" : "debug",
          "kubernetes": {
            "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
          },
          "range": "00192.00168.1.0/24",
          "range_start": "00192.00168.1.44",
          "gateway": "192.168.10.1"
        }
      }`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(conf), 0755)).To(Succeed())

		ipamConfig, _, err := LoadIPAMConfig([]byte(conf), "", confPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConfig.IPRanges[0].Range).To(Equal("192.168.1.0/24"))
		Expect(ipamConfig.IPRanges[0].RangeStart).To(Equal(net.ParseIP("192.168.1.44")))
	})

	It("allows for leading zeroes in the range when the start and end ranges are provided", func() {
		conf := `{
      "cniVersion": "0.3.1",
      "name": "mynet",
      "type": "ipvlan",
      "master": "foo0",
        "ipam": {
          "type": "whereabouts",
          "log_file" : "/tmp/whereabouts.log",
          "log_level" : "debug",
          "kubernetes": {
            "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
          },
          "range": "00192.00168.1.0/24",
          "range_start": "00192.00168.1.44",
          "range_end": "00192.00168.01.209",
          "gateway": "192.168.10.1"
        }
      }`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(conf), 0755)).To(Succeed())

		ipamConfig, _, err := LoadIPAMConfig([]byte(conf), "", confPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipamConfig.IPRanges[0].Range).To(Equal("192.168.1.0/24"))
		Expect(ipamConfig.IPRanges[0].RangeStart).To(Equal(net.ParseIP("192.168.1.44")))
		Expect(ipamConfig.IPRanges[0].RangeEnd).To(Equal(net.ParseIP("192.168.1.209")))
	})

	It("errors when an invalid range specified", func() {
		invalidConf := `{
			"cniVersion": "0.3.1",
            "name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
				"type": "whereabouts",
				"log_file" : "/tmp/whereabouts.log",
				"log_level" : "debug",
				"range": "192.168.1.5-192.168.2.25/28",
				"gateway": "192.168.10.1"
			}
		}`

		confPath := filepath.Join(tmpDir, "whereabouts.conf")
		Expect(os.WriteFile(confPath, []byte(invalidConf), 0755)).To(Succeed())

		_, _, err := LoadIPAMConfig([]byte(invalidConf), "", confPath)
		Expect(err).To(MatchError("invalid range start for CIDR 192.168.2.16/28: 192.168.1.5"))
	})

	It("errors when an invalid IPAM struct is specified", func() {
		invalidConf := `{
			"cniVersion": "0.3.1",
            "name": "mynet",
			"type": "ipvlan",
			"master": "foo0",
			"ipam": {
				asdf
			}
		}`
		_, _, err := LoadIPAMConfig([]byte(invalidConf), "")
		Expect(err).To(
			MatchError(
				HavePrefix(
					"LoadIPAMConfig - JSON Parsing Error: invalid character 'a' looking for beginning of object key string")))
	})

	// ── Gateway exclusion tests (#601) ──────────────────────────────────────
	Context("gateway exclusion (exclude_gateway)", func() {
		It("adds gateway to exclude ranges when exclude_gateway is true", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "192.168.1.0/24",
					"gateway": "192.168.1.1",
					"exclude_gateway": true
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges).To(HaveLen(1))
			Expect(ipamConf.IPRanges[0].OmitRanges).To(ContainElement("192.168.1.1/32"))
		})

		It("does NOT add gateway to exclude ranges when exclude_gateway is false (default)", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "192.168.1.0/24",
					"gateway": "192.168.1.1"
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges).To(HaveLen(1))
			Expect(ipamConf.IPRanges[0].OmitRanges).To(BeEmpty())
		})

		It("uses /128 suffix for IPv6 gateway exclusion", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "fd00::/120",
					"gateway": "fd00::1",
					"exclude_gateway": true
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges[0].OmitRanges).To(ContainElement("fd00::1/128"))
		})

		It("works without gateway configured (L3 use case)", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "192.168.1.0/24",
					"exclude_gateway": true
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges[0].OmitRanges).To(BeEmpty())
		})

		It("preserves existing exclude ranges alongside gateway", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "192.168.1.0/24",
					"gateway": "192.168.1.1",
					"exclude_gateway": true,
					"exclude": ["192.168.1.10/32", "192.168.1.20/32"]
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges[0].OmitRanges).To(ContainElement("192.168.1.10/32"))
			Expect(ipamConf.IPRanges[0].OmitRanges).To(ContainElement("192.168.1.20/32"))
			Expect(ipamConf.IPRanges[0].OmitRanges).To(ContainElement("192.168.1.1/32"))
		})
	})

	// ── L3 mode tests ───────────────────────────────────────────────────────
	Context("L3 mode (enable_l3)", func() {
		It("propagates enable_l3 from top-level to all IP ranges", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"enable_l3": true,
					"ipRanges": [{
						"range": "10.0.0.0/24"
					}, {
						"range": "10.0.1.0/24"
					}]
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges).To(HaveLen(2))
			Expect(ipamConf.IPRanges[0].L3).To(BeTrue())
			Expect(ipamConf.IPRanges[1].L3).To(BeTrue())
		})

		It("does not set L3 on ranges when enable_l3 is false (default)", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "10.0.0.0/24"
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges[0].L3).To(BeFalse())
		})

		It("allows per-range L3 setting for mixed L2/L3 setups", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"ipRanges": [{
						"range": "10.0.0.0/24",
						"enable_l3": true
					}, {
						"range": "10.0.1.0/24"
					}]
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges[0].L3).To(BeTrue())
			Expect(ipamConf.IPRanges[1].L3).To(BeFalse())
		})

		It("L3 pools work without a gateway", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"enable_l3": true,
					"range": "10.0.0.0/24"
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.Gateway).To(BeNil())
			Expect(ipamConf.IPRanges[0].L3).To(BeTrue())
		})
	})

	// ── Optimistic IPAM config tests (#510) ──────────────────────────────────
	Context("optimistic IPAM config (optimistic_ipam)", func() {
		It("parses optimistic_ipam field", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "10.0.0.0/24",
					"optimistic_ipam": true
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.OptimisticIPAM).To(BeTrue())
		})

		It("defaults optimistic_ipam to false", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "10.0.0.0/24"
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.OptimisticIPAM).To(BeFalse())
		})
	})

	// ── /32 and /31 config parsing tests (#573) ──────────────────────────────
	Context("/32 and /31 config parsing (#573)", func() {
		It("loads a /32 range configuration", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "10.0.0.5/32"
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges).To(HaveLen(1))
			Expect(ipamConf.IPRanges[0].Range).To(Equal("10.0.0.5/32"))
		})

		It("loads a /31 range configuration", func() {
			conf := `{
				"cniVersion": "0.3.1",
				"name": "mynet",
				"type": "ipvlan",
				"master": "foo0",
				"ipam": {
					"type": "whereabouts",
					"kubernetes": {"kubeconfig": "/etc/cni/net.d/whereabouts.kubeconfig"},
					"range": "10.0.0.4/31"
				}
			}`
			ipamConf, _, err := LoadIPAMConfig([]byte(conf), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ipamConf.IPRanges).To(HaveLen(1))
			Expect(ipamConf.IPRanges[0].Range).To(Equal("10.0.0.4/31"))
		})
	})
})

func generateIPAMConfWithOverlappingRanges() string {
	return `{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
			"range": "192.168.2.230/24",
			"configuration_path": "/tmp/whereabouts.conf",
			"type": "whereabouts",
			"enable_overlapping_ranges": true
		}
	}`
}

func generateIPAMConfWithoutOverlappingRanges() string {
	return `{
		"cniVersion": "0.3.1",
		"name": "mynet",
		"type": "ipvlan",
		"master": "foo0",
		"ipam": {
			"range": "192.168.2.230/24",
			"configuration_path": "/tmp/whereabouts.conf",
			"type": "whereabouts",
			"enable_overlapping_ranges": false
		}
	}`
}
