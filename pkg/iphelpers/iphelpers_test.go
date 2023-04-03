package iphelpers

import (
	"fmt"
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestIPHelpers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cmd")
}

var _ = Describe("CompareIPs operations", func() {
	It("compares IPv4.To4() addresses", func() {
		left := net.ParseIP("192.168.0.0")
		right := net.ParseIP("192.169.0.0")
		Expect(CompareIPs(left.To4(), right.To4())).To(Equal(-1))
	})

	It("compares IPv4.To16() addresses", func() {
		left := net.ParseIP("192.169.0.0")
		right := net.ParseIP("192.168.0.0")
		Expect(CompareIPs(left.To16(), right.To16())).To(Equal(1))
	})

	It("compares IPv4 mixed addresses", func() {
		left := net.ParseIP("192.168.0.0")
		right := net.ParseIP("192.168.0.0")
		Expect(CompareIPs(left.To16(), right.To4())).To(Equal(0))
	})

	It("compares IPv6 addresses when left is smaller than right", func() {
		left := net.ParseIP("2000::")
		right := net.ParseIP("2000::1")
		Expect(CompareIPs(left, right)).To(Equal(-1))
	})

	It("compares IPv6 addresses when left is larger than right", func() {
		left := net.ParseIP("2000::1")
		right := net.ParseIP("2000::")
		Expect(CompareIPs(left, right)).To(Equal(1))
	})

	It("compares IPv6 addresses when left == right", func() {
		left := net.ParseIP("2000::1")
		right := net.ParseIP("2000::1")
		Expect(CompareIPs(left, right)).To(Equal(0))
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
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())
		})

		It("returns true for end of range for IPv4", func() {
			in := net.ParseIP("192.169.0.0")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())
		})

		It("returns true for start of range for IPv4", func() {
			in := net.ParseIP("192.168.0.0")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())
		})

		It("returns true for IPv6", func() {
			in := net.ParseIP("2000::ffff:ffcc")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())

			in = net.ParseIP("2001:db8:480:603d:304:403::")
			start = net.ParseIP("2001:db8:480:603d::1")
			end = net.ParseIP("2001:db8:480:603e::4")
			isInRange, err = IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())
		})

		It("returns true for end of range for IPv6", func() {
			in := net.ParseIP("2000:1::")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())
		})

		It("returns true for start of range for IPv6", func() {
			in := net.ParseIP("2000::")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeTrue())
		})
	})

	When("the IP is not within the range", func() {
		It("returns false for IPv4", func() {
			in := net.ParseIP("192.169.255.100")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeFalse())
		})

		It("returns false for one beyond end of range for IPv4", func() {
			in := net.ParseIP("192.169.0.1")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeFalse())
		})

		It("returns false for one beyond start of range for IPv4", func() {
			in := net.ParseIP("192.167.255.255")
			start := net.ParseIP("192.168.0.0")
			end := net.ParseIP("192.169.0.0")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeFalse())
		})

		It("returns false for IPv6", func() {
			in := net.ParseIP("2000:1::ffff:ffcc")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeFalse())
		})

		It("returns false for one beyond end of range for IPv6", func() {
			in := net.ParseIP("2000:1::1")
			start := net.ParseIP("2000::")
			end := net.ParseIP("2000:1::")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeFalse())
		})

		It("returns false for one beyond start of range for IPv6", func() {
			in := net.ParseIP("2000::")
			start := net.ParseIP("2000::1")
			end := net.ParseIP("2000:1::")
			isInRange, err := IsIPInRange(in, start, end)
			Expect(err).NotTo(HaveOccurred())
			Expect(isInRange).To(BeFalse())
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
		It("correctly gets the FirstUsableIP for a /32", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			_, err := FirstUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the FirstUsableIP for a /31", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			_, err := FirstUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the FirstUsableIP for a /30", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			ip, err := FirstUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.1").To16()))
		})

		It("correctly gets the FirstUsableIP for a /23", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/23")
			ip, err := FirstUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.1").To16()))
		})
	})

	Context("IPv6", func() {
		It("correctly gets the FirstUsableIP for a /128", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			_, err := FirstUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the FirstUsableIP for a /127", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			_, err := FirstUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the FirstUsableIP for a /126", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			ip, err := FirstUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::1").To16()))
		})

		It("correctly gets the FirstUsableIP for a /64", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/64")
			ip, err := FirstUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::1").To16()))
		})
	})
})

var _ = Describe("LastUsableIP operations", func() {
	Context("IPv4", func() {
		It("correctly gets the LastUsableIP for a /32", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			_, err := LastUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the LastUsableIP for a /31", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			_, err := LastUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the LastUsableIP for a /30", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			ip, err := LastUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.0.2").To16()))
		})

		It("correctly gets the LastUsableIP for a /23", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/23")
			ip, err := LastUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("192.168.1.254").To16()))
		})
	})

	Context("IPv6", func() {
		It("correctly gets the LastUsableIP for a /128", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			_, err := LastUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the LastUsableIP for a /127", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			_, err := LastUsableIP(*ipnet)
			Expect(err.Error()).To(HavePrefix("net mask is too short"))
		})

		It("correctly gets the LastUsableIP for a /126", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			ip, err := LastUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::2").To16()))
		})

		It("correctly gets the LastUsableIP for a /64", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/64")
			ip, err := LastUsableIP(*ipnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.To16()).To(Equal(net.ParseIP("2000::ffff:ffff:ffff:fffe").To16()))
		})
	})
})

var _ = Describe("HasUsableIPs operations", func() {
	Context("small subnets", func() {
		It("IPv4 /32 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/32")
			Expect(HasUsableIPs(*ipnet)).To(BeFalse())
		})

		It("IPv4 /31 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/31")
			Expect(HasUsableIPs(*ipnet)).To(BeFalse())
		})

		It("IPv6 /128 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/128")
			Expect(HasUsableIPs(*ipnet)).To(BeFalse())
		})

		It("IPv6 /127 has no usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/127")
			Expect(HasUsableIPs(*ipnet)).To(BeFalse())
		})
	})

	Context("larger subnets", func() {
		It("IPv4 /30 has usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("192.168.0.0/30")
			Expect(HasUsableIPs(*ipnet)).To(BeTrue())
		})

		It("IPv6 /126 has usable IPs", func() {
			_, ipnet, _ := net.ParseCIDR("2000::/126")
			Expect(HasUsableIPs(*ipnet)).To(BeTrue())
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
			ip3 := net.ParseIP("ff02::1")
			ip4 := IncIP(ip3)
			Expect(ip4).To(Equal(net.ParseIP("ff02::2")))
		})

		It("works with IPv6 with ff", func() {
			ip5 := net.ParseIP("ff02::ff")
			ip6 := IncIP(ip5)
			Expect(ip6).To(Equal(net.ParseIP("ff02::0:100")))
		})
	})

	When("IP addresses are increased with rollover", func() {
		It("can roll over a single octet", func() {
			ip1 := net.ParseIP("192.168.2.255")
			ip2 := IncIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.3.0")))
		})

		It("can roll over 2 octets", func() {
			ip3 := net.ParseIP("192.168.255.255")
			ip4 := IncIP(ip3)
			Expect(ip4).To(Equal(net.ParseIP("192.169.0.0")))
		})

		It("can roll over IPv6", func() {
			ip7 := net.ParseIP("ff02::ffff")
			ip8 := IncIP(ip7)
			Expect(ip8).To(Equal(net.ParseIP("ff02::1:0")))
		})

		It("can roll over 4 IPv6 octets", func() {
			ip9 := net.ParseIP("ff02::ffff:ffff")
			ip10 := IncIP(ip9)
			Expect(ip10).To(Equal(net.ParseIP("ff02::1:0:0")))
		})

		It("IPv4 addresses can overflow", func() {
			ip11 := net.ParseIP("255.255.255.255")
			ip12 := IncIP(ip11)
			Expect(ip12).To(Equal(net.ParseIP("0.0.0.0")))
		})

		It("IPv6 addresses can overflow", func() {
			ip13 := net.ParseIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
			ip14 := IncIP(ip13)
			Expect(ip14).To(Equal(net.ParseIP("::")))
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
			ip3 := net.ParseIP("ff02::2")
			ip4 := DecIP(ip3)
			Expect(ip4).To(Equal(net.ParseIP("ff02::1")))
		})

		It("works with IPv6 with ff", func() {
			ip5 := net.ParseIP("ff02::100")
			ip6 := DecIP(ip5)
			Expect(ip6).To(Equal(net.ParseIP("ff02::0:ff")))
		})
	})

	When("IP addresses are decreased with rollover", func() {
		It("can roll over a single octet", func() {
			ip1 := net.ParseIP("192.168.3.0")
			ip2 := DecIP(ip1)
			Expect(ip2).To(Equal(net.ParseIP("192.168.2.255")))
		})

		It("can roll over 2 octets", func() {
			ip3 := net.ParseIP("192.169.0.0")
			ip4 := DecIP(ip3)
			Expect(ip4).To(Equal(net.ParseIP("192.168.255.255")))
		})

		It("can roll over IPv6", func() {
			ip7 := net.ParseIP("ff02::1:0")
			ip8 := DecIP(ip7)
			Expect(ip8).To(Equal(net.ParseIP("ff02::ffff")))
		})

		It("can roll over 4 IPv6 octets", func() {
			ip9 := net.ParseIP("ff02::1:0:0")
			ip10 := DecIP(ip9)
			Expect(ip10).To(Equal(net.ParseIP("ff02::ffff:ffff")))
		})

		It("IPv4 addresses can overflow", func() {
			ip11 := net.ParseIP("0.0.0.0")
			ip12 := DecIP(ip11)
			Expect(ip12).To(Equal(net.ParseIP("255.255.255.255")))
		})

		It("IPv6 addresses can overflow", func() {
			ip13 := net.ParseIP("::")
			ip14 := DecIP(ip13)
			Expect(ip14).To(Equal(net.ParseIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")))
		})
	})
})

var _ = Describe("GetIPRange operations", func() {
	It("creates an IPv4 range properly for 30 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("192.168.21.100/30")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.21.101"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.21.102"))
	})

	It("creates an IPv4 range properly for 24 bits network address with different range start", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		ip := net.ParseIP("192.168.2.23") // range start
		firstip, lastip, err := GetIPRange(*ipnet, ip, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.23"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 range properly for 27 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/27")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.193"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.222"))
	})

	It("creates an IPv4 range properly for 24 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 range properly for 24 bits network address with endRange", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		endRange := net.ParseIP("192.168.2.100")
		firstip, lastip, err := GetIPRange(*ipnet, nil, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.100"))
	})

	It("creates an IPv4 range properly for 24 bits network address with startRange and endRange", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.2.50")
		endRange := net.ParseIP("192.168.2.100")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.50"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.100"))
	})

	It("creates an IPv4 range properly for 24 bits network address with startRange and endRange outside of ipnet", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.1.150")
		endRange := net.ParseIP("192.168.3.100")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.1"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 range properly for 24 bits network address with startRange and endRange inverted", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.2.100")
		endRange := net.ParseIP("192.168.2.50")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.100"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.254"))
	})

	It("creates an IPv4 single range properly", func() {
		_, ipnet, err := net.ParseCIDR("192.168.2.200/24")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("192.168.2.50")
		endRange := net.ParseIP("192.168.2.50")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("192.168.2.50"))
		Expect(fmt.Sprint(lastip)).To(Equal("192.168.2.50"))
	})

	It("creates an IPv6 range properly for 116 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("2001::0/116")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001::ffe"))
	})

	It("creates an IPv6 range when the first hextet has leading zeroes", func() {
		_, ipnet, err := net.ParseCIDR("fd:db8:abcd:0012::0/96")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("fd:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("fd:db8:abcd:12::ffff:fffe"))
	})

	It("creates an IPv6 range properly for 96 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/96")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::ffff:fffe"))
	})

	It("creates an IPv6 range properly for 64 bits network address", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		firstip, lastip, err := GetIPRange(*ipnet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))
	})

	It("creates an IPv6 range properly for 64 bits network address with endRange", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		endRange := net.ParseIP("2001:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(*ipnet, nil, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::100"))
	})

	It("creates an IPv6 range properly for 64 bits network address with startRange and endRange", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:abcd:0012::50")
		endRange := net.ParseIP("2001:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::50"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::100"))
	})

	It("creates an IPv6 range properly for 64 bits network address with startRange and endRange outside of ipnet", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2000:db8:abcd:0012::50")
		endRange := net.ParseIP("2003:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::1"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))
	})

	It("creates an IPv6 range properly for 64 bits network address with startRange and endRange inverted", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:abcd:0012::100")
		endRange := net.ParseIP("2001:db8:abcd:0012::50")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::100"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12:ffff:ffff:ffff:fffe"))
	})

	It("creates an IPv6 single range properly", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:abcd:0012::0/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:abcd:0012::100")
		endRange := net.ParseIP("2001:db8:abcd:0012::100")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:abcd:12::100"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:abcd:12::100"))
	})

	It("creates a complex IPv6 single range properly", func() {
		_, ipnet, err := net.ParseCIDR("2001:db8:480:603d::/64")
		Expect(err).NotTo(HaveOccurred())
		startRange := net.ParseIP("2001:db8:480:603d:304:403::")
		endRange := net.ParseIP("2001:db8:480:603d:304:403:0:4")
		firstip, lastip, err := GetIPRange(*ipnet, startRange, endRange)
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprint(firstip)).To(Equal("2001:db8:480:603d:304:403::"))
		Expect(fmt.Sprint(lastip)).To(Equal("2001:db8:480:603d:304:403:0:4"))
	})

	It("do not fail when the mask meets minimum required", func() {
		_, validIPNet, err := net.ParseCIDR("192.168.21.100/30")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = GetIPRange(*validIPNet, nil, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails when the mask is too short", func() {
		_, badIPNet, err := net.ParseCIDR("192.168.21.100/31")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = GetIPRange(*badIPNet, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(HavePrefix("net mask is too short"))
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
		offset1, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset1).To(Equal(uint64(254)))

		ip3 := net.ParseIP("ff02::ff")
		ip4 := net.ParseIP("ff02::1")
		offset2, err := IPGetOffset(ip3, ip4)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset2).To(Equal(uint64(254)))
	})

	It("confirms the IPGetOffset carry case", func() {
		ip1 := net.ParseIP("192.168.3.0")
		ip2 := net.ParseIP("192.168.2.1")
		offset1, err := IPGetOffset(ip1, ip2)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset1).To(Equal(uint64(255)))

		ip3 := net.ParseIP("ff02::100")
		ip4 := net.ParseIP("ff02::1")
		offset2, err := IPGetOffset(ip3, ip4)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset2).To(Equal(uint64(255)))

		ip5 := net.ParseIP("ff02::1:0")
		ip6 := net.ParseIP("ff02::1")
		offset3, err := IPGetOffset(ip5, ip6)
		Expect(err).NotTo(HaveOccurred())
		Expect(offset3).To(Equal(uint64(0xffff)))
	})

	It("confirms the IPGetOffset error case", func() {
		// cannot get offset from v4/v6
		ip1 := net.ParseIP("192.168.3.0")
		ip2 := net.ParseIP("ff02::1")
		offset1, err := IPGetOffset(ip1, ip2)
		Expect(err).To(HaveOccurred())
		Expect(offset1).To(Equal(uint64(0)))

		// cannot get offset from v6/v4
		ip3 := net.ParseIP("ff02::1")
		ip4 := net.ParseIP("192.168.3.0")
		offset2, err := IPGetOffset(ip3, ip4)
		Expect(err).To(HaveOccurred())
		Expect(offset2).To(Equal(uint64(0)))
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
