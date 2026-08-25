// Package iphelpers provides IP address arithmetic utilities for whereabouts,
// including offset calculation, range iteration, CIDR splitting, and IPv4/IPv6
// detection.
package iphelpers

import (
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gaissmai/extnetip"
	netutils "k8s.io/utils/net"
)

// toAddr converts a net.IP to netip.Addr.
func toAddr(ip net.IP) (netip.Addr, bool) {
	if ip == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// toPrefix converts a net.IPNet to netip.Prefix.
func toPrefix(ipnet net.IPNet) (netip.Prefix, bool) {
	addr, ok := toAddr(ipnet.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := ipnet.Mask.Size()
	return netip.PrefixFrom(addr, ones), true
}

// CompareIPs reports whether out of 2 given IPs, ipX and ipY, ipY is smaller (-1), the same (0) or larger (1).
func CompareIPs(ipX net.IP, ipY net.IP) int {
	ax, okX := toAddr(ipX)
	ay, okY := toAddr(ipY)
	if !okX || !okY {
		return 0
	}
	return ax.Compare(ay)
}

// DivideRangeBySize takes an ipRange (e.g. "11.0.0.0/8") and a sliceSize (e.g. "/24")
// and returns a list of CIDRs that divide the input range into the given prefix lengths.
// Works with both IPv4 and IPv6.
func DivideRangeBySize(inputNetwork string, sliceSizeString string) ([]string, error) {
	sliceSizeString = strings.TrimPrefix(sliceSizeString, "/")
	sliceSize, err := strconv.Atoi(sliceSizeString)
	if err != nil {
		return nil, fmt.Errorf("invalid slice size %q: %w", sliceSizeString, err)
	}

	prefix, err := netip.ParsePrefix(inputNetwork)
	if err != nil {
		return nil, fmt.Errorf("error parsing CIDR %s: %w", inputNetwork, err)
	}
	if prefix.Addr() != prefix.Masked().Addr() {
		return nil, errors.New("netCIDR is not a valid network address")
	}

	netBits := prefix.Bits()
	if netBits > sliceSize {
		return nil, errors.New("subnetMaskSize must be greater or equal than netMaskSize")
	}

	addrLen := 32
	if prefix.Addr().Is6() {
		addrLen = 128
	}
	if sliceSize > addrLen {
		return nil, fmt.Errorf("slice size /%d exceeds address length /%d", sliceSize, addrLen)
	}

	numSubnets := new(big.Int).Lsh(big.NewInt(1), uint(sliceSize-netBits))
	subnetSize := new(big.Int).Lsh(big.NewInt(1), uint(addrLen-sliceSize))

	baseInt := netutils.BigForIP(prefix.Addr().AsSlice())
	var result []string

	for i := big.NewInt(0); i.Cmp(numSubnets) < 0; i.Add(i, big.NewInt(1)) {
		offset := new(big.Int).Mul(i, subnetSize)
		subnetBig := new(big.Int).Add(baseInt, offset)
		subnetIP := bigIntToIP(subnetBig, prefix.Addr().Is6())
		addr, _ := netip.AddrFromSlice(subnetIP)
		result = append(result, fmt.Sprintf("%s/%d", addr.Unmap(), sliceSize))
	}
	return result, nil
}

// IsIPInRange returns true if a given IP is within the continuous range of start and end IP (inclusively).
func IsIPInRange(in net.IP, start net.IP, end net.IP) (bool, error) {
	if in == nil || start == nil || end == nil {
		return false, fmt.Errorf("cannot determine if IP is in range, either of the values is '<nil>', "+
			"in: %v, start: %v, end: %v", in, start, end)
	}
	return CompareIPs(in, start) >= 0 && CompareIPs(in, end) <= 0, nil
}

// NetworkIP returns the network address of the subnet.
func NetworkIP(ipnet net.IPNet) net.IP {
	pfx, ok := toPrefix(ipnet)
	if !ok {
		return nil
	}
	return addrToNetIP(pfx.Masked().Addr(), ipnet.IP)
}

// SubnetBroadcastIP returns the broadcast IP (last address) for a given net.IPNet.
func SubnetBroadcastIP(ipnet net.IPNet) net.IP {
	pfx, ok := toPrefix(ipnet)
	if !ok {
		return nil
	}
	_, last := extnetip.Range(pfx)
	return addrToNetIP(last, ipnet.IP)
}

// FirstUsableIP returns the first usable IP in a given net.IPNet.
// For /32 (/128 IPv6): returns the single IP in the subnet.
// For /31 (/127 IPv6): returns the first IP (RFC 3021 point-to-point, both usable).
// For /30 and wider: returns the first IP after the network address.
func FirstUsableIP(ipnet net.IPNet) (net.IP, error) {
	if !HasUsableIPs(ipnet) {
		return nil, fmt.Errorf("net mask is too short, subnet %s has no usable IP addresses, it is too small", ipnet)
	}
	ones, totalBits := ipnet.Mask.Size()
	hostBits := totalBits - ones
	switch hostBits {
	case 0: // /32 or /128 — single address
		return ipnet.IP.Mask(ipnet.Mask), nil
	case 1: // /31 or /127 — RFC 3021, both IPs usable
		return NetworkIP(ipnet), nil
	default:
		return IncIP(NetworkIP(ipnet)), nil
	}
}

// LastUsableIP returns the last usable IP in a given net.IPNet.
// For /32 (/128 IPv6): returns the single IP in the subnet.
// For /31 (/127 IPv6): returns the second IP (RFC 3021, both usable).
// For /30 and wider: returns the last IP before the broadcast address.
func LastUsableIP(ipnet net.IPNet) (net.IP, error) {
	if !HasUsableIPs(ipnet) {
		return nil, fmt.Errorf("net mask is too short, subnet %s has no usable IP addresses, it is too small", ipnet)
	}
	ones, totalBits := ipnet.Mask.Size()
	hostBits := totalBits - ones
	switch hostBits {
	case 0: // /32 or /128 — single address
		return ipnet.IP.Mask(ipnet.Mask), nil
	case 1: // /31 or /127 — RFC 3021, both IPs usable
		return SubnetBroadcastIP(ipnet), nil
	default:
		return DecIP(SubnetBroadcastIP(ipnet)), nil
	}
}

// HasUsableIPs returns true if this subnet contains at least one usable IP.
// For /32 (/128): 1 usable IP (the address itself).
// For /31 (/127): 2 usable IPs (RFC 3021 point-to-point link).
// For all other valid subnets: usable IPs exist between network and broadcast.
func HasUsableIPs(ipnet net.IPNet) bool {
	_, totalBits := ipnet.Mask.Size()
	return totalBits > 0
}

// CountUsableIPs returns the number of usable IPs in a CIDR range.
// For /32 (/128): returns 1 (the single address).
// For /31 (/127): returns 2 (RFC 3021 point-to-point, both usable).
// For wider subnets: excludes the network and broadcast addresses.
// The result is clamped to math.MaxInt32 for safe conversion to int32.
func CountUsableIPs(cidr string) (int32, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}
	if !HasUsableIPs(*ipNet) {
		return 0, nil
	}

	// Handle /32 (/128) and /31 (/127) specially.
	ones, totalBits := ipNet.Mask.Size()
	hostBits := totalBits - ones
	if hostBits == 0 {
		return 1, nil // /32 or /128
	}
	if hostBits == 1 {
		return 2, nil // /31 or /127
	}

	first, err := FirstUsableIP(*ipNet)
	if err != nil {
		return 0, err
	}
	last, err := LastUsableIP(*ipNet)
	if err != nil {
		return 0, err
	}
	offset, err := IPGetOffset(first, last)
	if err != nil {
		return 0, err
	}
	// Total usable = offset + 1 (inclusive range).
	total := new(big.Int).Add(offset, big.NewInt(1))

	maxInt32 := big.NewInt(1<<31 - 1)
	if total.Cmp(maxInt32) > 0 {
		return 1<<31 - 1, nil
	}
	return int32(total.Int64()), nil
}

// IncIP increases the given IP address by one.
// If the address is already the maximum (e.g. 255.255.255.255), it is returned unchanged.
func IncIP(ip net.IP) net.IP {
	addr, ok := toAddr(ip)
	if !ok {
		return ip
	}
	next := addr.Next()
	if !next.IsValid() {
		return ip
	}
	return addrToNetIP(next, ip)
}

// DecIP decreases the given IP address by one.
// If the address is already the minimum (0.0.0.0 or ::), it is returned unchanged.
func DecIP(ip net.IP) net.IP {
	addr, ok := toAddr(ip)
	if !ok {
		return ip
	}
	prev := addr.Prev()
	if !prev.IsValid() {
		return ip
	}
	return addrToNetIP(prev, ip)
}

// IPGetOffset returns the absolute offset between ip1 and ip2 as a *big.Int.
// The result is always non-negative. Uses k8s.io/utils/net for IP arithmetic.
func IPGetOffset(ip1, ip2 net.IP) (*big.Int, error) {
	addr1, ok1 := toAddr(ip1)
	addr2, ok2 := toAddr(ip2)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("invalid IP address(es): ip1=%v, ip2=%v", ip1, ip2)
	}
	if addr1.Is4() && !addr2.Is4() {
		return nil, fmt.Errorf("cannot calculate offset between IPv4 (%s) and IPv6 address (%s)", ip1, ip2)
	}
	if !addr1.Is4() && addr2.Is4() {
		return nil, fmt.Errorf("cannot calculate offset between IPv6 (%s) and IPv4 address (%s)", ip1, ip2)
	}

	a := netutils.BigForIP(ip1)
	b := netutils.BigForIP(ip2)
	diff := new(big.Int).Sub(a, b)
	diff.Abs(diff)
	return diff, nil
}

// IPAddOffset returns ip + offset. Uses k8s.io/utils/net for IP arithmetic.
// The offset must be non-negative.
func IPAddOffset(ip net.IP, offset *big.Int) net.IP {
	if ip == nil {
		return nil
	}

	base := netutils.BigForIP(ip)
	resultInt := new(big.Int).Add(base, offset)
	return bigIntToIP(resultInt, len(ip) != net.IPv4len)
}

// IsIPv4 checks if an IP is v4.
func IsIPv4(checkip net.IP) bool {
	return checkip.To4() != nil
}

// GetIPRange returns the first and last IP in a range.
// If either rangeStart or rangeEnd are inside the range of first usable IP to last usable IP, then use them.
// Otherwise, they will be silently ignored and the first usable IP and/or last usable IP will be used.
// A valid rangeEnd cannot be smaller than a valid rangeStart.
func GetIPRange(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP) (first, last net.IP, err error) {
	firstUsableIP, err := FirstUsableIP(ipnet)
	if err != nil {
		return nil, nil, err
	}
	lastUsableIP, err := LastUsableIP(ipnet)
	if err != nil {
		return nil, nil, err
	}
	if rangeStart != nil {
		rangeStartInRange, err := IsIPInRange(rangeStart, firstUsableIP, lastUsableIP)
		if err != nil {
			return nil, nil, err
		}
		if rangeStartInRange {
			firstUsableIP = rangeStart
		}
	}
	if rangeEnd != nil {
		rangeEndInRange, err := IsIPInRange(rangeEnd, firstUsableIP, lastUsableIP)
		if err != nil {
			return nil, nil, err
		}
		if rangeEndInRange {
			lastUsableIP = rangeEnd
		}
	}
	return firstUsableIP, lastUsableIP, nil
}

// bigIntToIP converts a *big.Int to a net.IP of the appropriate length.
// Uses netip.Addr for canonical representation.
func bigIntToIP(i *big.Int, is6 bool) net.IP {
	b := i.Bytes()
	if is6 {
		var arr [net.IPv6len]byte
		if len(b) > net.IPv6len {
			b = b[len(b)-net.IPv6len:]
		}
		copy(arr[net.IPv6len-len(b):], b)
		addr := netip.AddrFrom16(arr)
		return addr.AsSlice()
	}
	var arr [net.IPv4len]byte
	if len(b) > net.IPv4len {
		b = b[len(b)-net.IPv4len:]
	}
	copy(arr[net.IPv4len-len(b):], b)
	addr := netip.AddrFrom4(arr)
	return addr.AsSlice()
}

// addrToNetIP converts a netip.Addr back to net.IP, preserving the slice length of origIP.
// This ensures IPv4 addresses maintain their 4-byte or 16-byte (IPv4-in-IPv6 mapped) representation.
func addrToNetIP(addr netip.Addr, origIP net.IP) net.IP {
	if addr.Is4() && len(origIP) == net.IPv6len {
		b := addr.As16()
		return net.IP(b[:])
	}
	return addr.AsSlice()
}
