// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package iphelpers

import (
	"math/big"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ---------------------------------------------------------------
// IsIPv4
// ---------------------------------------------------------------
var _ = Describe("IsIPv4", func() {
	It("returns true for 4-byte IPv4", func() {
		Expect(IsIPv4(net.ParseIP("10.0.0.1").To4())).To(BeTrue())
	})

	It("returns true for 16-byte IPv4-mapped (net.ParseIP default)", func() {
		// net.ParseIP returns 16-byte representation; To4() still works.
		Expect(IsIPv4(net.ParseIP("192.168.1.1"))).To(BeTrue())
	})

	It("returns false for IPv6", func() {
		Expect(IsIPv4(net.ParseIP("fd00::1"))).To(BeFalse())
	})

	It("returns false for nil IP", func() {
		Expect(IsIPv4(nil)).To(BeFalse())
	})

	It("returns false for empty IP", func() {
		Expect(IsIPv4(net.IP{})).To(BeFalse())
	})

	It("returns true for loopback IPv4", func() {
		Expect(IsIPv4(net.ParseIP("127.0.0.1"))).To(BeTrue())
	})

	It("returns false for IPv6 loopback", func() {
		Expect(IsIPv4(net.ParseIP("::1"))).To(BeFalse())
	})

	It("returns true for 0.0.0.0", func() {
		Expect(IsIPv4(net.ParseIP("0.0.0.0"))).To(BeTrue())
	})

	It("returns false for ::", func() {
		Expect(IsIPv4(net.ParseIP("::"))).To(BeFalse())
	})
})

// ---------------------------------------------------------------
// IPAddOffset edge cases
// ---------------------------------------------------------------
var _ = Describe("IPAddOffset edge cases", func() {
	It("returns nil for nil IP", func() {
		result := IPAddOffset(nil, big.NewInt(1))
		Expect(result).To(BeNil())
	})

	It("adds 0 offset and returns same IP", func() {
		ip := net.ParseIP("10.0.0.5").To4()
		result := IPAddOffset(ip, big.NewInt(0))
		Expect(result).NotTo(BeNil())
		Expect(result.Equal(ip)).To(BeTrue())
	})

	It("adds offset to IPv4", func() {
		ip := net.ParseIP("10.0.0.0").To4()
		result := IPAddOffset(ip, big.NewInt(10))
		Expect(result).NotTo(BeNil())
		Expect(result.Equal(net.ParseIP("10.0.0.10"))).To(BeTrue())
	})

	It("adds offset to IPv6", func() {
		ip := net.ParseIP("fd00::")
		result := IPAddOffset(ip, big.NewInt(255))
		Expect(result).NotTo(BeNil())
		Expect(result.Equal(net.ParseIP("fd00::ff"))).To(BeTrue())
	})

	It("handles large IPv6 offset", func() {
		ip := net.ParseIP("fd00::")
		// 2^64
		offset := new(big.Int).Lsh(big.NewInt(1), 64)
		result := IPAddOffset(ip, offset)
		Expect(result).NotTo(BeNil())
		Expect(result.Equal(net.ParseIP("fd00::1:0:0:0:0"))).To(BeTrue())
	})
})

// ---------------------------------------------------------------
// IPGetOffset edge cases
// ---------------------------------------------------------------
var _ = Describe("IPGetOffset edge cases", func() {
	It("returns 0 for same IPs", func() {
		offset, err := IPGetOffset(net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(offset.Int64()).To(Equal(int64(0)))
	})

	It("returns absolute offset (positive direction)", func() {
		offset, err := IPGetOffset(net.ParseIP("10.0.0.5"), net.ParseIP("10.0.0.1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(offset.Int64()).To(Equal(int64(4)))
	})

	It("returns absolute offset (negative direction)", func() {
		offset, err := IPGetOffset(net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.5"))
		Expect(err).NotTo(HaveOccurred())
		Expect(offset.Int64()).To(Equal(int64(4)))
	})

	It("returns error for mixed IPv4 and IPv6", func() {
		_, err := IPGetOffset(net.ParseIP("10.0.0.1").To4(), net.ParseIP("fd00::1"))
		Expect(err).To(HaveOccurred())
	})

	It("returns error for nil IPs", func() {
		_, err := IPGetOffset(nil, net.ParseIP("10.0.0.1"))
		Expect(err).To(HaveOccurred())
	})

	It("works for IPv6 addresses", func() {
		offset, err := IPGetOffset(net.ParseIP("fd00::1"), net.ParseIP("fd00::ff"))
		Expect(err).NotTo(HaveOccurred())
		Expect(offset.Int64()).To(Equal(int64(254)))
	})
})

// ---------------------------------------------------------------
// CompareIPs edge cases
// ---------------------------------------------------------------
var _ = Describe("CompareIPs edge cases", func() {
	It("returns 0 for nil IPs", func() {
		Expect(CompareIPs(nil, net.ParseIP("10.0.0.1"))).To(Equal(0))
	})

	It("returns 0 for both nil", func() {
		Expect(CompareIPs(nil, nil)).To(Equal(0))
	})

	It("returns 0 for empty IPs", func() {
		Expect(CompareIPs(net.IP{}, net.ParseIP("10.0.0.1"))).To(Equal(0))
	})
})

// ---------------------------------------------------------------
// NetworkIP edge cases
// ---------------------------------------------------------------
var _ = Describe("NetworkIP edge cases", func() {
	It("returns network address for /24", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.5/24")
		result := NetworkIP(*ipNet)
		Expect(result.Equal(net.ParseIP("10.0.0.0"))).To(BeTrue())
	})

	It("returns network address for /32", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.5/32")
		result := NetworkIP(*ipNet)
		Expect(result.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
	})

	It("returns network address for IPv6 /64", func() {
		_, ipNet, _ := net.ParseCIDR("fd00::1/64")
		result := NetworkIP(*ipNet)
		Expect(result.Equal(net.ParseIP("fd00::"))).To(BeTrue())
	})

	It("returns nil for zero-value IPNet", func() {
		result := NetworkIP(net.IPNet{})
		Expect(result).To(BeNil())
	})
})

// ---------------------------------------------------------------
// IncIP / DecIP edge cases
// ---------------------------------------------------------------
var _ = Describe("IncIP and DecIP edge cases", func() {
	It("IncIP of max IPv4 returns same", func() {
		ip := net.ParseIP("255.255.255.255").To4()
		result := IncIP(ip)
		Expect(result.Equal(ip)).To(BeTrue())
	})

	It("DecIP of min IPv4 returns same", func() {
		ip := net.ParseIP("0.0.0.0").To4()
		result := DecIP(ip)
		Expect(result.Equal(ip)).To(BeTrue())
	})

	It("IncIP rolls over byte boundary", func() {
		ip := net.ParseIP("10.0.0.255").To4()
		result := IncIP(ip)
		Expect(result.Equal(net.ParseIP("10.0.1.0"))).To(BeTrue())
	})

	It("DecIP rolls over byte boundary", func() {
		ip := net.ParseIP("10.0.1.0").To4()
		result := DecIP(ip)
		Expect(result.Equal(net.ParseIP("10.0.0.255"))).To(BeTrue())
	})

	It("IncIP handles nil", func() {
		result := IncIP(nil)
		Expect(result).To(BeNil())
	})

	It("DecIP handles nil", func() {
		result := DecIP(nil)
		Expect(result).To(BeNil())
	})
})

// ---------------------------------------------------------------
// SubnetBroadcastIP
// ---------------------------------------------------------------
var _ = Describe("SubnetBroadcastIP edge cases", func() {
	It("returns broadcast for /24", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/24")
		result := SubnetBroadcastIP(*ipNet)
		Expect(result.Equal(net.ParseIP("10.0.0.255"))).To(BeTrue())
	})

	It("returns same IP for /32", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.5/32")
		result := SubnetBroadcastIP(*ipNet)
		Expect(result.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
	})

	It("returns nil for zero-value IPNet", func() {
		result := SubnetBroadcastIP(net.IPNet{})
		Expect(result).To(BeNil())
	})
})

// ---------------------------------------------------------------
// HasUsableIPs
// ---------------------------------------------------------------
var _ = Describe("HasUsableIPs", func() {
	It("returns true for /24", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/24")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})

	It("returns true for /30", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/30")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})

	It("returns true for /31 (RFC 3021 point-to-point)", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/31")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})

	It("returns true for /32 (single address)", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/32")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})

	It("returns true for IPv6 /128 (single address)", func() {
		_, ipNet, _ := net.ParseCIDR("fd00::1/128")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})

	It("returns true for IPv6 /127 (RFC 3021)", func() {
		_, ipNet, _ := net.ParseCIDR("fd00::/127")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})

	It("returns true for IPv6 /126", func() {
		_, ipNet, _ := net.ParseCIDR("fd00::/126")
		Expect(HasUsableIPs(*ipNet)).To(BeTrue())
	})
})

// ---------------------------------------------------------------
// DivideRangeBySize edge cases
// ---------------------------------------------------------------
var _ = Describe("DivideRangeBySize edge cases", func() {
	It("returns error for non-aligned network address", func() {
		_, err := DivideRangeBySize("10.0.0.1/24", "/28")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not a valid network address"))
	})

	It("returns error when slice size smaller than network prefix", func() {
		_, err := DivideRangeBySize("10.0.0.0/24", "/16")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must be greater or equal"))
	})

	It("returns error for invalid slice size string", func() {
		_, err := DivideRangeBySize("10.0.0.0/24", "abc")
		Expect(err).To(HaveOccurred())
	})

	It("returns error for invalid CIDR", func() {
		_, err := DivideRangeBySize("not-a-cidr", "/24")
		Expect(err).To(HaveOccurred())
	})

	It("returns single entry when prefix equals slice size", func() {
		subnets, err := DivideRangeBySize("10.0.0.0/24", "/24")
		Expect(err).NotTo(HaveOccurred())
		Expect(subnets).To(HaveLen(1))
		Expect(subnets[0]).To(Equal("10.0.0.0/24"))
	})

	It("handles slash-prefixed slice size", func() {
		subnets, err := DivideRangeBySize("10.0.0.0/24", "/26")
		Expect(err).NotTo(HaveOccurred())
		Expect(subnets).To(HaveLen(4))
	})

	It("returns error for slice size exceeding address length", func() {
		_, err := DivideRangeBySize("10.0.0.0/24", "/33")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exceeds address length"))
	})
})

// ---------------------------------------------------------------
// GetIPRange
// ---------------------------------------------------------------
var _ = Describe("GetIPRange edge cases", func() {
	It("uses custom rangeStart and rangeEnd within range", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/24")
		first, last, err := GetIPRange(*ipNet, net.ParseIP("10.0.0.10"), net.ParseIP("10.0.0.200"))
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Equal(net.ParseIP("10.0.0.10"))).To(BeTrue())
		Expect(last.Equal(net.ParseIP("10.0.0.200"))).To(BeTrue())
	})

	It("ignores rangeStart outside subnet", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/24")
		first, last, err := GetIPRange(*ipNet, net.ParseIP("10.0.1.1"), nil)
		Expect(err).NotTo(HaveOccurred())
		// rangeStart is outside → uses first usable IP.
		Expect(first.Equal(net.ParseIP("10.0.0.1"))).To(BeTrue())
		Expect(last.Equal(net.ParseIP("10.0.0.254"))).To(BeTrue())
	})

	It("ignores rangeEnd outside subnet", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/24")
		first, last, err := GetIPRange(*ipNet, nil, net.ParseIP("10.0.1.1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Equal(net.ParseIP("10.0.0.1"))).To(BeTrue())
		Expect(last.Equal(net.ParseIP("10.0.0.254"))).To(BeTrue())
	})

	It("uses nil rangeStart and rangeEnd (full range)", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/24")
		first, last, err := GetIPRange(*ipNet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Equal(net.ParseIP("10.0.0.1"))).To(BeTrue())
		Expect(last.Equal(net.ParseIP("10.0.0.254"))).To(BeTrue())
	})

	It("returns valid range for /31 (RFC 3021)", func() {
		_, ipNet, _ := net.ParseCIDR("10.0.0.0/31")
		first, last, err := GetIPRange(*ipNet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Equal(net.ParseIP("10.0.0.0"))).To(BeTrue())
		Expect(last.Equal(net.ParseIP("10.0.0.1"))).To(BeTrue())
	})
})
