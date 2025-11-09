package iphelpers

import (
	"fmt"
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const (
	leftIPIsSmaller        = -1
	leftAndRightIPAreEqual = 0
	leftIPIsLarger         = 1
)

func TestIPHelpers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("CompareIPs operations", func() {
	It("compares IPv4.To4() addresses", func() {
		left := net.ParseIP("192.168.0.0")
		right := net.ParseIP("192.169.0.0")
		Expect(CompareIPs(left.To4(), right.To4())).To(Equal(leftIPIsSmaller))
	})

	It("compares IPv4.To16() addresses", func() {
		left := net.ParseIP("192.169.0.0")
		right := net.ParseIP("192.168.0.0")
		Expect(CompareIPs(left.To16(), right.To16())).To(Equal(leftIPIsLarger))
	})

	It("compares IPv4 mixed addresses", func() {
		left := net.ParseIP("192.168.0.0")
		right := net.ParseIP("192.168.0.0")
		Expect(CompareIPs(left.To16(), right.To4())).To(Equal(leftAndRightIPAreEqual))
	})

	It("compares IPv6 addresses when left is smaller than right", func() {
		left := net.ParseIP("2000::")
		right := net.ParseIP("2000::1")
		Expect(CompareIPs(left, right)).To(Equal(leftIPIsSmaller))
	})

	It("compares IPv6 addresses when left is larger than right", func() {
		left := net.ParseIP("2000::1")
		right := net.ParseIP("2000::")
		Expect(CompareIPs(left, right)).To(Equal(leftIPIsLarger))
	})

	It("compares IPv6 addresses when left == right", func() {
		left := net.ParseIP("2000::1")
		right := net.ParseIP("2000::1")
		Expect(CompareIPs(left, right)).To(Equal(leftAndRightIPAreEqual))
	})
})

var _ = Describe("IsIPInRange operations", func() {
	When("one of the IPs is nil", func() {
		It("returns an error for IPv4", func() {
			in := net.ParseIP("INVALID")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			_, err := IsIPInRange(in, start, end)
			Expect(err).To(HaveOccurred())
		})

		It("returns an error for IPv6", func() {
			in := net.ParseIP("INVALID")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			_, err := IsIPInRange(in, start, end)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the IP is within the range", func() {
		It("returns true for IPv4", func() {
			in := net.ParseIP("192.168.255.100")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())
		})

		It("returns true for end of range for IPv4", func() {
			in := net.ParseIP("192.169.0.0")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())
		})

		It("returns true for start of range for IPv4", func() {
			in := net.ParseIP("192.168.0.0")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())
		})

		It("returns true for IPv6", func() {
			in := net.ParseIP("2000::ffff:ffcc")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())

			in = net.ParseIP("2001:db8:480:603d:304:403::")
			start = net.ParseIP("2001:db8:480:603d::1")
			end = net.ParseIP("2001:db8:480:603e::4")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())
		})

		It("returns true for end of range for IPv6", func() {
			in := net.ParseIP("2000:1::")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())
		})

		It("returns true for start of range for IPv6", func() {
			in := net.ParseIP("2000::")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			Expect(IsIPInRange(in, start, end)).To(BeTrue())
		})
	})

	When("the IP is not within the range", func() {
		It("returns false for IPv4", func() {
			in := net.ParseIP("192.169.255.100")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			Expect(IsIPInRange(in, start, end)).To(BeFalse())
		})

		It("returns false for one beyond end of range for IPv4", func() {
			in := net.ParseIP("192.169.0.1")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			Expect(IsIPInRange(in, start, end)).To(BeFalse())
		})

		It("returns false for one beyond start of range for IPv4", func() {
			in := net.ParseIP("192.167.255.255")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			Expect(IsIPInRange(in, start, end)).To(BeFalse())
		})

		It("returns false for IPv6", func() {
			in := net.ParseIP("2000:1::ffff:ffcc")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			Expect(IsIPInRange(in, start, end)).To(BeFalse())
		})

		It("returns false for one beyond end of range for IPv6", func() {
			in := net.ParseIP("2000:1::1")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			Expect(IsIPInRange(in, start, end)).To(BeFalse())
		})

		It("returns false for one beyond start of range for IPv6", func() {
			in := net.ParseIP("2000::")
			start := net.ParseIP("2000::1")
			end := net.ParseIP("2000:1::")
			Expect(IsIPInRange(in, start, end)).To(BeFalse())
		})
	})
})

var _ = Describe("NetworkIP operations", func() {
	Context("IPv4", func() {
		It("correctly gets the NetworkIP for a /32", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.0").To16()))
		})

		It("correctly gets the NetworkIP for a /31", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.0").To16()))
		})

		It("correctly gets the NetworkIP for a /30", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.0").To16()))
		})

		It("correctly gets the NetworkIP for a /23", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/23")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.0").To16()))
		})
	})

	Context("IPv6", func() {
		It("correctly gets the NetworkIP for a /128", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::").To16()))
		})

		It("correctly gets the NetworkIP for a /127", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::").To16()))
		})

		It("correctly gets the NetworkIP for a /126", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::").To16()))
		})

		It("correctly gets the NetworkIP for a /64", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/64")
			ip := NetworkIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::").To16()))
		})
	})
})

var _ = Describe("SubnetBroadcastIP operations", func() {
	Context("IPv4", func() {
		It("correctly gets the SubnetBroadcastIP for a /32", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.0").To16()))
		})

		It("correctly gets the SubnetBroadcastIP for a /31", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.1").To16()))
		})

		It("correctly gets the SubnetBroadcastIP for a /30", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.3").To16()))
		})

		It("correctly gets the SubnetBroadcastIP for a /23", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/23")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.1.255").To16()))
		})
	})

	Context("IPv6", func() {
		It("correctly gets the SubnetBroadcastIP for a /128", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::0").To16()))
		})

		It("correctly gets the SubnetBroadcastIP for a /127", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::1").To16()))
		})

		It("correctly gets the SubnetBroadcastIP for a /126", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::3").To16()))
		})

		It("correctly gets the SubnetBroadcastIP for a /64", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/64")
			ip := SubnetBroadcastIP(*ipnet)
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::ffff:ffff:ffff:ffff").To16()))
		})
	})
})

var _ = Describe("FirstUsableIP operations", func() {
	Context("IPv4", func() {
		It("throws an error when running FirstUsableIP for a /32", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			_, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("throws an error when running FirstUsableIP for a /31", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			_, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("correctly gets the FirstUsableIP for a /30", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			ip, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.1").To16()))
		})

		It("correctly gets the FirstUsableIP for a /23", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/23")
			ip, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.1").To16()))
		})
	})

	Context("IPv6", func() {
		It("throws an error when running FirstUsableIP for a /128", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			_, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("throws an error when running FirstUsableIP for a /127", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			_, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("correctly gets the FirstUsableIP for a /126", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			ip, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::1").To16()))
		})

		It("correctly gets the FirstUsableIP for a /64", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/64")
			ip, err := FirstUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::1").To16()))
		})
	})
})

var _ = Describe("LastUsableIP operations", func() {
	Context("IPv4", func() {
		It("throws an error when running LastUsableIP for a /32", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			_, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("throws an error when running LastUsableIP for a /31", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			_, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("correctly gets the LastUsableIP for a /30", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			ip, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.2").To16()))
		})

		It("correctly gets the LastUsableIP for a /23", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/23")
			ip, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.1.254").To16()))
		})
	})

	Context("IPv6", func() {
		It("throws an error when running LastUsableIP for a /128", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			_, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("throws an error when running LastUsableIP for a /127", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			_, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).To(MatchError(HavePrefix("net mask is too short")))
		})

		It("correctly gets the LastUsableIP for a /126", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			ip, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::2").To16()))
		})

		It("correctly gets the LastUsableIP for a /64", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/64")
			ip, err := LastUsableIP(types.Pool{
				IPNet: *ipnet,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::ffff:ffff:ffff:fffe").To16()))
		})
	})
})

var _ = Describe("HasUsableIPs operations", func() {
	Context("small subnets", func() {
		It("IPv4 /32 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			Expect(HasUsableIPs(types.Pool{
				IPNet: *ipnet,
			})).To(BeFalse())
		})

		It("IPv4 /31 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			Expect(HasUsableIPs(types.Pool{
				IPNet: *ipnet,
			})).To(BeFalse())
		})

		It("IPv6 /128 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			Expect(HasUsableIPs(types.Pool{
				IPNet: *ipnet,
			})).To(BeFalse())
		})

		It("IPv6 /127 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			Expect(HasUsableIPs(types.Pool{
				IPNet: *ipnet,
			})).To(BeFalse())
		})
	})

	Context("larger subnets", func() {
		It("IPv4 /30 has usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			Expect(HasUsableIPs(types.Pool{
				IPNet: *ipnet,
			})).To(BeTrue())
		})

		It("IPv6 /126 has usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			Expect(HasUsableIPs(types.Pool{
				IPNet: *ipnet,
			})).To(BeTrue())
		})
	})
})

var _ = Describe("IncIPAddress operations", func() {
	When("IP addresses are increased without rolling over", func() {
		It("works with IPv4", func() {
			ip1 := net.ParseIP("192.168.2.23")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.2.24")))
		})

		It("works with IPv6", func() {
			ip1 := net.ParseIP("ff02::1")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::2")))
		})

		It("works with IPv6 with ff", func() {
			ip1 := net.ParseIP("ff02::ff")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::0:100")))
		})
	})

	When("IP addresses are increased with rollover", func() {
		It("can roll over a single octet", func() {
			ip1 := net.ParseIP("192.168.2.255")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.3.0")))
		})

		It("can roll over 2 octets", func() {
			ip1 := net.ParseIP("192.168.255.255")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.169.0.0")))
		})

		It("can roll over IPv6", func() {
			ip1 := net.ParseIP("ff02::ffff")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::1:0")))
		})

		It("can roll over 4 IPv6 octets", func() {
			ip1 := net.ParseIP("ff02::ffff:ffff")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::1:0:0")))
		})

		It("IPv4 addresses can overflow", func() {
			ip1 := net.ParseIP("255.255.255.255")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("0.0.0.0")))
		})

		It("IPv6 addresses can overflow", func() {
			ip1 := net.ParseIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("::")))
		})
	})
})

var _ = Describe("DecIPAddress operations", func() {
	When("IP addresses are decreased without rolling over", func() {
		It("works with IPv4", func() {
			ip1 := net.ParseIP("192.168.2.23")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.2.22")))
		})

		It("works with IPv6", func() {
			ip1 := net.ParseIP("ff02::2")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::1")))
		})

		It("works with IPv6 with ff", func() {
			ip1 := net.ParseIP("ff02::100")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::0:ff")))
		})
	})

	When("IP addresses are decreased with rollover", func() {
		It("can roll over a single octet", func() {
			ip1 := net.ParseIP("192.168.3.0")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.2.255")))
		})

		It("can roll over 2 octets", func() {
			ip1 := net.ParseIP("192.169.0.0")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.255.255")))
		})

		It("can roll over IPv6", func() {
			ip1 := net.ParseIP("ff02::1:0")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::ffff")))
		})

		It("can roll over 4 IPv6 octets", func() {
			ip1 := net.ParseIP("ff02::1:0:0")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ff02::ffff:ffff")))
		})

		It("IPv4 addresses can overflow", func() {
			ip1 := net.ParseIP("0.0.0.0")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("255.255.255.255")))
		})

		It("IPv6 addresses can overflow", func() {
			ip1 := net.ParseIP("::")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")))
		})
	})
})

var _ = Describe("GetIPRange operations", func() {
	It("creates an IPv4 range properly for 30 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("192.168.21.100/30")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.21.101"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.21.102"))
	})

	It("creates an IPv4 range properly for 24 bits network address with different range start", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		ip := net.ParseIP("192.168.2.23") // range start
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: ip,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.23"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 range properly for 27 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/27")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.193"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.222"))
	})

	It("creates an IPv4 range properly for 24 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 range properly for 24 bits network address with endRange", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		endRange := net.ParseIP("192.168.2.100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:    *ipnet,
			RangeEnd: endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.100"))
	})

	It("creates an IPv4 range properly for 24 bits network address with startRange and endRange", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.2.50")
		endRange := net.ParseIP("192.168.2.100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.50"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.100"))
	})

	It("creates an IPv4 range properly for 24 bits network address with startRange and endRange outside of ipnet", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.1.150")
		endRange := net.ParseIP("192.168.3.100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 range properly for 24 bits network address with startRange and endRange inverted", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.2.100")
		endRange := net.ParseIP("192.168.2.50")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.100"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 single range properly", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.2.50")
		endRange := net.ParseIP("192.168.2.50")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.50"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.50"))
	})

	It("creates an IPv6 range properly for 116 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("2001::0/116")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001::ffe"))
	})

	It("creates an IPv6 range when the first hextet has leading zeroes", func() {
		_, ipnet, err := net.ParseCIDR("fd:db8:abcd:0012::0/96")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("fd:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("fd:db8:abcd:12::ffff:fffe"))
	})

	It("creates an IPv6 range properly for 96 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/96")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::ffff:fffe"))
	})

	It("creates an IPv6 range properly for 64 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet: *ipnet,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))
	})

	It("creates an IPv6 range properly for 64 bits network address with endRange", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		endRange := net.ParseIP("2001:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:    *ipnet,
			RangeEnd: endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::100"))
	})

	It("creates an IPv6 range properly for 64 bits network address with startRange and endRange", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:abcd:0012::50")
		endRange := net.ParseIP("2001:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::50"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::100"))
	})

	It("creates an IPv6 range properly for 64 bits network address with startRange and endRange outside of ipnet", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2000:db8:abcd:0012::50")
		endRange := net.ParseIP("2003:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))
	})

	It("creates an IPv6 range properly for 64 bits network address with startRange and endRange inverted", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:abcd:0012::100")
		endRange := net.ParseIP("2001:db8:abcd:0012::50")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::100"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))
	})

	It("creates an IPv6 single range properly", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:abcd:0012::100")
		endRange := net.ParseIP("2001:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::100"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::100"))
	})

	It("creates a complex IPv6 single range properly", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:480:603d::/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:480:603d:304:403::")
		endRange := net.ParseIP("2001:db8:480:603d:304:403:0:4")
		firstip, lastip, err := GetIPRange(types.Pool{
			IPNet:      *ipnet,
			RangeStart: startRange,
			RangeEnd:   endRange,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:480:603d:304:403::"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:480:603d:304:403:0:4"))
	})

	It("do not fail when the mask meets minimum required", func() {
		_, validIPNet, err := net.ParseCIDR("192.168.21.100/30")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = GetIPRange(types.Pool{
			IPNet: *validIPNet,
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails when the mask is too short", func() {
		_, badIPNet, err := net.ParseCIDR("192.168.21.100/31")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = GetIPRange(types.Pool{
			IPNet: *badIPNet,
		})
		Expect(err).To(MatchError(HavePrefix("net mask is too short")))
	})
})

var _ = Describe("IPGetOffset operations", func() {
	It("correctly calculates the offset between two IPv4 IPs", func() {
		ip1 := net.ParseIP("192.168.1.1")
		ip2 := net.ParseIP("192.168.1.0")
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(1)))
	})

	It("correctly calculates the offset between two IPv4 IPs in different notations when the first value is in To4", func() {
		ip1 := net.ParseIP("192.168.1.1").To4()
		ip2 := net.ParseIP("192.168.1.0").To16()
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(1)))

		ip1 = net.ParseIP("192.168.4.0").To4()
		ip2 = net.ParseIP("192.168.3.0").To16()
		offset, err = IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(256)))
	})

	It("correctly calculates the offset between two IPv4 IPs in different notations when the second value in in To4", func() {
		ip1 := net.ParseIP("192.168.1.1").To16()
		ip2 := net.ParseIP("192.168.1.0").To4()
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(1)))
	})

	It("correctly calculates the offset between two IPv4 IPs inverted", func() {
		ip1 := net.ParseIP("192.168.1.0").To16()
		ip2 := net.ParseIP("192.168.1.1").To4()
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(1)))

		ip1 = net.ParseIP("192.168.1.0").To16()
		ip2 = net.ParseIP("192.168.2.255").To4()
		offset, err = IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(511)))
	})

	It("confirms the IPGetOffset normal case", func() {
		ip1 := net.ParseIP("192.168.2.255")
		ip2 := net.ParseIP("192.168.2.1")
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(254)))

		ip1 = net.ParseIP("ff02::ff")
		ip2 = net.ParseIP("ff02::1")
		offset, err = IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(254)))
	})

	It("confirms the IPGetOffset carry case", func() {
		ip1 := net.ParseIP("192.168.3.0")
		ip2 := net.ParseIP("192.168.2.1")
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(255)))

		ip1 = net.ParseIP("ff02::100")
		ip2 = net.ParseIP("ff02::1")
		offset, err = IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(255)))

		ip1 = net.ParseIP("ff02::1:0")
		ip2 = net.ParseIP("ff02::1")
		offset, err = IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(uint64(0xffff)))
	})

	It("confirms the IPGetOffset error case", func() {
		// cannot get offset from v4/v6
		ip1 := net.ParseIP("192.168.3.0")
		ip2 := net.ParseIP("ff02::1")
		offset, err := IPGetOffset(ip1, ip2)
		Expect(err).To(MatchError("cannot calculate offset between IPv4 (192.168.3.0) and IPv6 address (ff02::1)"))
		Expect(offset).To(Equal(uint64(0)))

		// cannot get offset from v6/v4
		ip1 = net.ParseIP("ff02::1")
		ip2 = net.ParseIP("192.168.3.0")
		offset, err = IPGetOffset(ip1, ip2)
		Expect(err).To(MatchError("cannot calculate offset between IPv6 (ff02::1) and IPv4 address (192.168.3.0)"))
		Expect(offset).To(Equal(uint64(0)))
	})
})

var _ = Describe("IP helper utility functions", func() {
	/*
		func byteSliceAdd(ar1, ar2 []byte) ([]byte, error) {
		func byteSliceSub(ar1, ar2 []byte) ([]byte, error) {
		func ipAddrToUint64(ip net.IP) uint64 {
		func ipAddrFromUint64(num uint64) net.IP {
		func IPAddOffset(ip net.IP, offset uint64) net.IP {
		func IPGetOffset(ip1, ip2 net.IP) uint64 {
	*/
	It("tests byteSliceAdd normal case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 10}
		bSum, err := byteSliceAdd(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11, 11}))
	})

	It("tests byteSliceAdd carry case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		bSum, err := byteSliceAdd(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0}))
	})

	It("tests byteSliceSub normal case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 10}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1}
		bSum, err := byteSliceSub(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9, 9}))
	})

	It("tests byteSliceSub carry case", func() {
		b1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0}
		b2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		bSum, err := byteSliceSub(b1, b2)
		Expect(err).NotTo(HaveOccurred())
		Expect(bSum).To(Equal([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255}))
	})

	It("can convert ipAddrToUint64", func() {
		b := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 255, 255}
		bNum := ipAddrToUint64(net.IP(b))
		Expect(bNum).To(Equal(uint64(0x1ffff)))
	})

	It("can convert ipAddrFromUint64", func() {
		uintNum := uint64(0x1ffff)
		ip := net.IP([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 255, 255})
		bNum := ipAddrFromUint64(uintNum)
		Expect(bNum).To(Equal(ip))
	})
})

var _ = Describe("IPAddOffset operations", func() {
	It("correctly calculates the offset between two IPv4 IPs", func() {
		ip := net.ParseIP("192.168.1.1")
		newIP := IPAddOffset(ip, 256)
		Expect(fmt.Sprint(newIP)).To(Equal("192.168.2.1"))
	})

	It("correctly calculates the offset between two IPv6 IPs", func() {
		ip := net.ParseIP("2000::1")
		newIP := IPAddOffset(ip, 65535)
		Expect(fmt.Sprint(newIP)).To(Equal("2000::1:0"))
	})
})

func TestDivideRangeBySize(t *testing.T) {
	cases := []struct {
		name           string
		netRange       string
		sliceSize      string
		expectedResult []string
		expectError    bool
	}{
		{
			name:           "Network divided by same size slice",
			netRange:       "10.0.0.0/8",
			sliceSize:      "/8",
			expectedResult: []string{"10.0.0.0/8"},
		},
		{
			name:           "Network divided /8 by /10",
			netRange:       "10.0.0.0/8",
			sliceSize:      "/10",
			expectedResult: []string{"10.0.0.0/10", "10.64.0.0/10", "10.128.0.0/10", "10.192.0.0/10"},
		},
		{
			name:        "Network divided /10 by /8",
			netRange:    "10.0.0.0/10",
			sliceSize:   "/8",
			expectError: true,
		},
		{
			name:           "Network divided /8 by /11",
			netRange:       "10.0.0.0/8",
			sliceSize:      "/11",
			expectedResult: []string{"10.0.0.0/11", "10.32.0.0/11", "10.64.0.0/11", "10.96.0.0/11", "10.128.0.0/11", "10.160.0.0/11", "10.192.0.0/11", "10.224.0.0/11"},
		},
		{
			name:           "Network divided /10 by /12",
			netRange:       "10.0.0.0/10",
			sliceSize:      "/12",
			expectedResult: []string{"10.0.0.0/12", "10.16.0.0/12", "10.32.0.0/12", "10.48.0.0/12"},
		},
		{
			name:           "Network divided /8 by /10 without / in slice",
			netRange:       "10.0.0.0/8",
			sliceSize:      "10",
			expectedResult: []string{"10.0.0.0/10", "10.64.0.0/10", "10.128.0.0/10", "10.192.0.0/10"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := DivideRangeBySize(tc.netRange, tc.sliceSize)
			if err != nil && !tc.expectError {
				t.Errorf("unexpected error: %v", err)
			}
			if err == nil && tc.expectError {
				t.Fatalf("expected error but did not get it")
			}
			if len(result) != len(tc.expectedResult) {
				t.Fatalf("Expected result: %s, got result: %s", tc.expectedResult, result)
			}
			for i := range result {
				if result[i] != tc.expectedResult[i] {
					t.Fatalf("Expected result: %s, got result: %s", tc.expectedResult, result)
				}
			}
		})
	}
}
