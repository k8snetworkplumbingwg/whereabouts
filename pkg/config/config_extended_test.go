package config

import (
	"encoding/json"
	"net"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// Extended tests added to cover ParsePrevResult, handleEnvArgs, and other gaps.

var _ = Describe("ParsePrevResult", func() {
	It("returns nil, nil when no prevResult is present", func() {
		data := []byte(`{"name":"test","cniVersion":"1.0.0"}`)
		result, err := ParsePrevResult(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns error for invalid JSON", func() {
		data := []byte(`{invalid}`)
		_, err := ParsePrevResult(data)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to unmarshal prevResult"))
	})

	It("parses a valid prevResult", func() {
		prevResult := map[string]interface{}{
			"cniVersion": "1.0.0",
			"ips": []interface{}{
				map[string]interface{}{
					"address": "10.0.0.2/24",
				},
			},
			"interfaces": []interface{}{
				map[string]interface{}{
					"name":    "eth0",
					"sandbox": "/proc/1/ns/net",
				},
			},
		}
		raw := map[string]interface{}{
			"name":       "test",
			"cniVersion": "1.0.0",
			"prevResult": prevResult,
		}
		data, err := json.Marshal(raw)
		Expect(err).NotTo(HaveOccurred())

		result, err := ParsePrevResult(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.IPs).To(HaveLen(1))
	})

	It("returns nil for null prevResult", func() {
		data := []byte(`{"name":"test","cniVersion":"1.0.0","prevResult":null}`)
		result, err := ParsePrevResult(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns error when prevResult has invalid structure", func() {
		data := []byte(`{"prevResult":{"cniVersion":"1.0.0","ips":"not-an-array"}}`)
		_, err := ParsePrevResult(data)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("handleEnvArgs", func() {
	It("adds IP from IP env arg", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{},
		}
		args := types.IPAMEnvArgs{
			IP: "10.0.0.5/24",
		}
		numV6, numV4, err := handleEnvArgs(n, 0, 0, args)
		Expect(err).NotTo(HaveOccurred())
		Expect(numV4).To(Equal(1))
		Expect(numV6).To(Equal(0))
		Expect(n.IPAM.Addresses).To(HaveLen(1))
		Expect(n.IPAM.Addresses[0].Version).To(Equal("4"))
	})

	It("adds multiple IPs from comma-separated IP env arg", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{},
		}
		args := types.IPAMEnvArgs{
			IP: "10.0.0.5/24,fd00::5/120",
		}
		numV6, numV4, err := handleEnvArgs(n, 0, 0, args)
		Expect(err).NotTo(HaveOccurred())
		Expect(numV4).To(Equal(1))
		Expect(numV6).To(Equal(1))
		Expect(n.IPAM.Addresses).To(HaveLen(2))
	})

	It("returns error for invalid IP in IP env arg", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{},
		}
		args := types.IPAMEnvArgs{
			IP: "not-valid-cidr",
		}
		_, _, err := handleEnvArgs(n, 0, 0, args)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid CIDR"))
	})

	It("matches GATEWAY env arg to existing address", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{
				Addresses: []types.Address{
					{
						AddressStr: "10.0.0.5/24",
					},
				},
			},
		}
		// Parse the address properly
		n.IPAM.Addresses[0].Address.IP = []byte{10, 0, 0, 5}
		n.IPAM.Addresses[0].Address.Mask = []byte{255, 255, 255, 0}

		args := types.IPAMEnvArgs{
			GATEWAY: "10.0.0.1",
		}
		_, _, err := handleEnvArgs(n, 0, 0, args)
		Expect(err).NotTo(HaveOccurred())
		Expect(n.IPAM.Addresses[0].Gateway).NotTo(BeNil())
	})

	It("returns error for invalid gateway", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{},
		}
		args := types.IPAMEnvArgs{
			GATEWAY: "not-an-ip",
		}
		_, _, err := handleEnvArgs(n, 0, 0, args)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid gateway"))
	})

	It("does nothing when IP and GATEWAY are empty", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{},
		}
		args := types.IPAMEnvArgs{}
		numV6, numV4, err := handleEnvArgs(n, 0, 0, args)
		Expect(err).NotTo(HaveOccurred())
		Expect(numV4).To(Equal(0))
		Expect(numV6).To(Equal(0))
	})
})

var _ = Describe("isNetworkRelevant", func() {
	It("returns true for whereabouts type", func() {
		cfg := &types.IPAMConfig{Type: "whereabouts"}
		Expect(isNetworkRelevant(cfg)).To(BeTrue())
	})

	It("returns false for other types", func() {
		cfg := &types.IPAMConfig{Type: "host-local"}
		Expect(isNetworkRelevant(cfg)).To(BeFalse())
	})

	It("returns false for empty type", func() {
		cfg := &types.IPAMConfig{Type: ""}
		Expect(isNetworkRelevant(cfg)).To(BeFalse())
	})
})

var _ = Describe("InvalidPluginError", func() {
	It("formats error message correctly", func() {
		err := NewInvalidPluginError("host-local")
		Expect(err.Error()).To(ContainSubstring("host-local"))
		Expect(err.Error()).To(ContainSubstring("IPAM type must be"))
	})
})

var _ = Describe("FileNotFoundError", func() {
	It("has the expected error message", func() {
		err := NewFileNotFoundError()
		Expect(err.Error()).To(Equal("config file not found"))
	})
})

var _ = Describe("storageError", func() {
	It("returns error about missing kubeconfig", func() {
		err := storageError()
		Expect(err.Error()).To(ContainSubstring("kubeconfig"))
		Expect(err.Error()).To(ContainSubstring("required"))
	})
})

var _ = Describe("pathExists", func() {
	It("returns true for existing path", func() {
		tmpFile, err := os.CreateTemp("", "wb-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		Expect(pathExists(tmpFile.Name())).To(BeTrue())
	})

	It("returns false for non-existing path", func() {
		Expect(pathExists("/nonexistent/path/file.txt")).To(BeFalse())
	})
})

var _ = Describe("LoadIPAMConfig edge cases", func() {
	It("returns error for missing ipam key", func() {
		data := []byte(`{"name":"test","cniVersion":"1.0.0"}`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing 'ipam' key"))
	})

	It("returns error for non-whereabouts IPAM type", func() {
		data := []byte(`{"name":"test","cniVersion":"1.0.0","ipam":{"type":"host-local"}}`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())

		var invalidErr *InvalidPluginError
		Expect(err).To(BeAssignableToTypeOf(invalidErr))
	})

	It("returns error for missing kubeconfig", func() {
		data := []byte(`{
			"name":"test",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.0/24"
			}
		}`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubeconfig"))
	})

	It("returns error for invalid range CIDR", func() {
		data := []byte(`{
			"name":"test",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"not-a-cidr",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid range"))
	})

	It("returns error for invalid gateway", func() {
		data := []byte(`{
			"name":"test",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.0/24",
				"gateway":"not-an-ip",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("gateway"))
	})

	It("returns error for malformed JSON", func() {
		data := []byte(`not json at all`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("JSON Parsing Error"))
	})

	It("returns error for invalid exclude range CIDR", func() {
		data := []byte(`{
			"name":"test",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.0/24",
				"exclude":["not-a-cidr"],
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		_, _, err := LoadIPAMConfig(data, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid CIDR in exclude"))
	})

	It("parses range format with dash (start-end/cidr)", func() {
		data := []byte(`{
			"name":"test",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.10-10.0.0.100/24",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		cfg, _, err := LoadIPAMConfig(data, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.IPRanges).To(HaveLen(1))
		Expect(cfg.IPRanges[0].RangeStart.String()).To(Equal("10.0.0.10"))
		Expect(cfg.IPRanges[0].RangeEnd.String()).To(Equal("10.0.0.100"))
	})

	It("sets default leader election values", func() {
		data := []byte(`{
			"name":"test",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.0/24",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		cfg, _, err := LoadIPAMConfig(data, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.LeaderLeaseDuration).To(Equal(types.DefaultLeaderLeaseDuration))
		Expect(cfg.LeaderRenewDeadline).To(Equal(types.DefaultLeaderRenewDeadline))
		Expect(cfg.LeaderRetryPeriod).To(Equal(types.DefaultLeaderRetryPeriod))
	})

	It("sets name from Net name", func() {
		data := []byte(`{
			"name":"my-net",
			"cniVersion":"1.0.0",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.0/24",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		cfg, _, err := LoadIPAMConfig(data, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Name).To(Equal("my-net"))
	})
})

var _ = Describe("LoadIPAMConfiguration", func() {
	It("parses conflist format", func() {
		data := []byte(`{
			"name":"my-net",
			"cniVersion":"1.0.0",
			"plugins":[{
				"type":"bridge",
				"bridge":"mybridge",
				"ipam":{
					"type":"whereabouts",
					"range":"10.0.0.0/24",
					"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
				}
			}]
		}`)
		cfg, err := LoadIPAMConfiguration(data, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.IPRanges).To(HaveLen(1))
	})

	It("parses single plugin format", func() {
		data := []byte(`{
			"name":"my-net",
			"cniVersion":"1.0.0",
			"type":"bridge",
			"ipam":{
				"type":"whereabouts",
				"range":"10.0.0.0/24",
				"kubernetes": {"kubeconfig": "/etc/kubeconfig"}
			}
		}`)
		cfg, err := LoadIPAMConfiguration(data, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
	})

	It("returns error for invalid JSON", func() {
		data := []byte(`{invalid}`)
		_, err := LoadIPAMConfiguration(data, "")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("configureStatic", func() {
	It("returns error for invalid address CIDR", func() {
		n := &types.Net{
			IPAM: &types.IPAMConfig{
				Addresses: []types.Address{
					{AddressStr: "not-a-cidr"},
				},
			},
		}
		err := configureStatic(n, types.IPAMEnvArgs{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid CIDR in addresses"))
	})

	It("rejects multiple v4 addresses with old CNI version", func() {
		n := &types.Net{
			CNIVersion: "0.2.0",
			IPAM: &types.IPAMConfig{
				Addresses: []types.Address{
					{AddressStr: "10.0.0.1/24"},
					{AddressStr: "10.0.0.2/24"},
				},
			},
		}
		err := configureStatic(n, types.IPAMEnvArgs{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does not support more than 1 address"))
	})

	It("allows multiple v4 addresses with new CNI version", func() {
		n := &types.Net{
			CNIVersion: "1.0.0",
			IPAM: &types.IPAMConfig{
				Addresses: []types.Address{
					{AddressStr: "10.0.0.1/24"},
					{AddressStr: "10.0.0.2/24"},
				},
			},
		}
		err := configureStatic(n, types.IPAMEnvArgs{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("classifies IPv6 addresses correctly", func() {
		n := &types.Net{
			CNIVersion: "1.0.0",
			IPAM: &types.IPAMConfig{
				Addresses: []types.Address{
					{AddressStr: "fd00::1/120"},
				},
			},
		}
		err := configureStatic(n, types.IPAMEnvArgs{})
		Expect(err).NotTo(HaveOccurred())
		Expect(n.IPAM.Addresses[0].Version).To(Equal("6"))
	})
})

var _ = Describe("canonicalizeIP", func() {
	It("canonicalizes IPv4-mapped-IPv6 to IPv4", func() {
		// 16-byte IPv4-mapped IPv6 representation of 10.0.0.1
		ipAddr := net.IP([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 10, 0, 0, 1})
		err := canonicalizeIP(&ipAddr)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipAddr).To(HaveLen(4))
		Expect(ipAddr.String()).To(Equal("10.0.0.1"))
	})

	It("canonicalizes plain IPv4", func() {
		ipAddr := net.ParseIP("192.168.1.1").To4()
		err := canonicalizeIP(&ipAddr)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipAddr).To(HaveLen(4))
	})

	It("canonicalizes IPv6", func() {
		ip := net.ParseIP("fd00::1")
		err := canonicalizeIP(&ip)
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(HaveLen(16))
	})

	It("returns error for nil IP bytes", func() {
		ip := net.IP(nil)
		err := canonicalizeIP(&ip)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not v4 nor v6"))
	})

	It("returns error for empty IP bytes", func() {
		ip := net.IP([]byte{})
		err := canonicalizeIP(&ip)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("GetFlatIPAM path validation", func() {
	It("rejects configuration_path with path traversal", func() {
		ipam := &types.IPAMConfig{
			ConfigurationPath: "../../../etc/passwd",
		}
		_, _, err := GetFlatIPAM(false, ipam)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("path traversal"))
	})

	It("rejects bare dotdot path traversal", func() {
		ipam := &types.IPAMConfig{
			ConfigurationPath: "..",
		}
		_, _, err := GetFlatIPAM(false, ipam)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("path traversal"))
	})

	It("accepts clean configuration_path", func() {
		ipam := &types.IPAMConfig{
			ConfigurationPath: "/etc/cni/net.d/whereabouts.d/whereabouts.conf",
		}
		// This will return FileNotFoundError since the file doesn't exist,
		// but it should NOT return a path traversal error.
		_, _, err := GetFlatIPAM(false, ipam)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("path traversal"))
	})
})
