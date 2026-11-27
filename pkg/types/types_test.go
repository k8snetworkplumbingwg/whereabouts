package types

import (
	"encoding/json"
	"net"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTypes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Types Suite")
}

var _ = Describe("IPAMConfig", func() {
	Describe("UnmarshalJSON", func() {
		It("sets default OverlappingRanges to true", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24"}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.OverlappingRanges).To(BeTrue())
		})

		It("sets default SleepForRace to 0", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24"}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.SleepForRace).To(Equal(DefaultSleepForRace))
		})

		It("allows SleepForRace to be overridden", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24","sleep_for_race":5}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.SleepForRace).To(Equal(5))
		})

		It("parses range_start with leading zeroes (backward compat)", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24","range_start":"010.010.000.001"}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.RangeStart).To(Equal(net.ParseIP("10.10.0.1")))
		})

		It("parses range_end with leading zeroes", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24","range_end":"010.000.000.200"}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.RangeEnd).To(Equal(net.ParseIP("10.0.0.200")))
		})

		It("handles invalid range_start gracefully (nil)", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24","range_start":"not-an-ip"}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.RangeStart).To(BeNil())
		})

		It("handles empty range_start gracefully (nil)", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"10.0.0.0/24","range_start":""}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.RangeStart).To(BeNil())
		})

		It("parses IPv6 range_start", func() {
			var cfg IPAMConfig
			data := `{"type":"whereabouts","range":"fd00::/120","range_start":"fd00::5"}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.RangeStart.Equal(net.ParseIP("fd00::5"))).To(BeTrue())
		})

		It("returns error for malformed JSON", func() {
			var cfg IPAMConfig
			data := `{invalid json}`
			err := json.Unmarshal([]byte(data), &cfg)
			Expect(err).To(HaveOccurred())
		})

		It("parses all fields correctly", func() {
			var cfg IPAMConfig
			data := `{
				"type": "whereabouts",
				"range": "192.168.0.0/16",
				"log_file": "/var/log/test.log",
				"log_level": "debug",
				"network_name": "testnet",
				"gateway": "192.168.0.1",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"},
				"configuration_path": "/etc/cni/net.d/wb.conf",
				"leader_lease_duration": 2000,
				"leader_renew_deadline": 1500,
				"leader_retry_period": 600,
				"node_slice_size": "/28"
			}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.Type).To(Equal("whereabouts"))
			Expect(cfg.Range).To(Equal("192.168.0.0/16"))
			Expect(cfg.LogFile).To(Equal("/var/log/test.log"))
			Expect(cfg.LogLevel).To(Equal("debug"))
			Expect(cfg.NetworkName).To(Equal("testnet"))
			Expect(cfg.GatewayStr).To(Equal("192.168.0.1"))
			// Gateway is populated from the alias Gateway field (no json tag),
			// not from GatewayStr. The JSON "gateway" maps to GatewayStr only.
			// Gateway is set via backwardsCompatibleIPAddress on the alias
			// Gateway field which has no json tag, so it stays empty => nil.
			Expect(cfg.Gateway).To(BeNil())
			Expect(cfg.Kubernetes.KubeConfigPath).To(Equal("/etc/kubeconfig"))
			Expect(cfg.ConfigurationPath).To(Equal("/etc/cni/net.d/wb.conf"))
			Expect(cfg.LeaderLeaseDuration).To(Equal(2000))
			Expect(cfg.LeaderRenewDeadline).To(Equal(1500))
			Expect(cfg.LeaderRetryPeriod).To(Equal(600))
			Expect(cfg.NodeSliceSize).To(Equal("/28"))
		})

		It("parses ipRanges field", func() {
			var cfg IPAMConfig
			data := `{
				"type": "whereabouts",
				"ipRanges": [
					{"range": "10.0.0.0/24", "exclude": ["10.0.0.100/32"]},
					{"range": "fd00::/120"}
				]
			}`
			Expect(json.Unmarshal([]byte(data), &cfg)).To(Succeed())
			Expect(cfg.IPRanges).To(HaveLen(2))
			Expect(cfg.IPRanges[0].Range).To(Equal("10.0.0.0/24"))
			Expect(cfg.IPRanges[0].OmitRanges).To(ConsistOf("10.0.0.100/32"))
			Expect(cfg.IPRanges[1].Range).To(Equal("fd00::/120"))
		})

		It("handles empty input", func() {
			var cfg IPAMConfig
			err := json.Unmarshal([]byte(`{}`), &cfg)
			Expect(err).To(Succeed())
			Expect(cfg.OverlappingRanges).To(BeTrue()) // default
		})
	})

	Describe("GetPodRef", func() {
		It("formats namespace/name correctly", func() {
			cfg := IPAMConfig{PodNamespace: "kube-system", PodName: "my-pod"}
			Expect(cfg.GetPodRef()).To(Equal("kube-system/my-pod"))
		})

		It("handles empty namespace", func() {
			cfg := IPAMConfig{PodNamespace: "", PodName: "my-pod"}
			Expect(cfg.GetPodRef()).To(Equal("/my-pod"))
		})

		It("handles empty name", func() {
			cfg := IPAMConfig{PodNamespace: "ns", PodName: ""}
			Expect(cfg.GetPodRef()).To(Equal("ns/"))
		})

		It("handles both empty", func() {
			cfg := IPAMConfig{PodNamespace: "", PodName: ""}
			Expect(cfg.GetPodRef()).To(Equal("/"))
		})
	})
})

var _ = Describe("IPReservation", func() {
	Describe("String", func() {
		It("formats correctly", func() {
			r := IPReservation{
				IP:     net.ParseIP("10.0.0.1"),
				PodRef: "default/my-pod",
			}
			Expect(r.String()).To(Equal("IP: 10.0.0.1 is reserved for pod: default/my-pod"))
		})

		It("handles IPv6", func() {
			r := IPReservation{
				IP:     net.ParseIP("fd00::1"),
				PodRef: "ns/pod",
			}
			Expect(r.String()).To(ContainSubstring("fd00::1"))
			Expect(r.String()).To(ContainSubstring("ns/pod"))
		})
	})
})

var _ = Describe("sanitizeIP", func() {
	It("parses valid IPv4", func() {
		ip, err := sanitizeIP("10.0.0.1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip.Equal(net.ParseIP("10.0.0.1"))).To(BeTrue())
	})

	It("parses IPv4 with leading zeroes", func() {
		ip, err := sanitizeIP("010.010.000.001")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip.Equal(net.ParseIP("10.10.0.1"))).To(BeTrue())
	})

	It("parses valid IPv6", func() {
		ip, err := sanitizeIP("fd00::1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip.Equal(net.ParseIP("fd00::1"))).To(BeTrue())
	})

	It("returns error for invalid IP", func() {
		_, err := sanitizeIP("not-an-ip")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not a valid IP address"))
	})

	It("returns error for empty string", func() {
		_, err := sanitizeIP("")
		Expect(err).To(HaveOccurred())
	})

	It("parses IPv6 with leading zeroes", func() {
		ip, err := sanitizeIP("fd00:0000:0000::0001")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip.Equal(net.ParseIP("fd00::1"))).To(BeTrue())
	})
})

var _ = Describe("backwardsCompatibleIPAddress", func() {
	It("returns nil for invalid IP", func() {
		Expect(backwardsCompatibleIPAddress("garbage")).To(BeNil())
	})

	It("returns nil for empty string", func() {
		Expect(backwardsCompatibleIPAddress("")).To(BeNil())
	})

	It("returns parsed IP for valid input", func() {
		ip := backwardsCompatibleIPAddress("192.168.1.1")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("192.168.1.1"))).To(BeTrue())
	})

	It("handles sloppy parsing with leading zeroes", func() {
		ip := backwardsCompatibleIPAddress("010.000.001.001")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("10.0.1.1"))).To(BeTrue())
	})
})

var _ = Describe("ErrNoIPRanges", func() {
	It("has the expected error message", func() {
		Expect(ErrNoIPRanges.Error()).To(Equal("no IP ranges in whereabouts config"))
	})
})

var _ = Describe("Constants", func() {
	It("has expected default values", func() {
		Expect(DefaultLeaderLeaseDuration).To(Equal(1500))
		Expect(DefaultLeaderRenewDeadline).To(Equal(1000))
		Expect(DefaultLeaderRetryPeriod).To(Equal(500))
		Expect(DefaultOverlappingIPsFeatures).To(BeTrue())
		Expect(DefaultSleepForRace).To(Equal(0))
		Expect(MaxSleepForRace).To(Equal(10))
	})
})
