package extnetip

import "math/bits"

// uint128 represents a 128-bit unsigned integer value using two uint64 parts.
//
// This struct models the internal numeric representation of netip.Addr
// in Go 1.18 and newer, where IP addresses are handled as 128-bit values.
type uint128 struct {
	hi uint64
	lo uint64
}

// and returns the bitwise AND of two uint128 values.
func (u uint128) and(v uint128) uint128 {
	return uint128{u.hi & v.hi, u.lo & v.lo}
}

// or returns the bitwise OR of two uint128 values.
func (u uint128) or(v uint128) uint128 {
	return uint128{u.hi | v.hi, u.lo | v.lo}
}

// xor returns the bitwise XOR of two uint128 values.
func (u uint128) xor(v uint128) uint128 {
	return uint128{u.hi ^ v.hi, u.lo ^ v.lo}
}

// not returns the bitwise complement (inversion) of the uint128 value.
func (u uint128) not() uint128 {
	return uint128{^u.hi, ^u.lo}
}

// mask6 creates a network mask with the first n bits set to 1 (from the MSB side),
// and the remaining bits set to 0.
//
// For example:
//   - n=0   -> all bits 0
//   - n=128 -> all bits 1
//
// Since IPv6/IPv4 addresses are represented as 128-bit values,
// this function covers prefix lengths from 0 to 128.
//
// Implementation details:
//   - For n <= 64: sets top n bits of the 'hi' uint64, 'lo' is zero.
//   - For n > 64: 'hi' is fully set, 'lo' has (n-64) high bits set.
func mask6(n int) uint128 {
	return uint128{^(^uint64(0) >> n), ^uint64(0) << (128 - n)}
}

// u64CommonPrefixLen calculates the number of leading bits that u and v have in common.
//
// It computes the number of leading zero bits in the XOR of u and v,
// effectively the length of their matching prefix in 64 bits.
func u64CommonPrefixLen(u, v uint64) int {
	return bits.LeadingZeros64(u ^ v)
}

// commonPrefixLen returns the number of leading bits that two uint128
// values have in common.
//
// If the upper 64 bits have a full 64-bit match, it continues to check
// the lower 64 bits.
func (u uint128) commonPrefixLen(v uint128) (n int) {
	if n = u64CommonPrefixLen(u.hi, v.hi); n == 64 {
		n += u64CommonPrefixLen(u.lo, v.lo)
	}
	return
}

// prefixOK checks if the range from u to v (inclusive) forms an exact IP prefix (CIDR block).
//
// Returns:
//   - lcp: the length of the common prefix bits between u and v.
//   - ok: true if [u, v] corresponds exactly to a CIDR range without gaps.
//
// The check confirms:
//   - That the prefix length matches the differing bits.
//   - That u has zeros in all host bits (the prefix network part).
//   - That v has ones in all host bits (the prefix broadcast/end address).
func (u uint128) prefixOK(v uint128) (lcp int, ok bool) {
	lcp = u.commonPrefixLen(v)
	if lcp == 128 {
		return lcp, true
	}
	mask := mask6(lcp)

	// check if mask applied to first and last results in all zeros and all ones
	allZero := u.xor(u.and(mask)) == uint128{}
	allOnes := v.or(mask) == uint128{^uint64(0), ^uint64(0)}

	return lcp, allZero && allOnes
}

// compare compares u and v and returns:
//
//	 1 if u > v
//	-1 if u < v
//	 0 if u == v
func (u uint128) compare(v uint128) int {
	if u.hi > v.hi {
		return 1
	}
	if u.hi < v.hi {
		return -1
	}

	// u.hi == v.hi
	if u.lo > v.lo {
		return 1
	}
	if u.lo < v.lo {
		return -1
	}

	// u.hi == v.hi && u.lo == v.lo
	return 0
}
