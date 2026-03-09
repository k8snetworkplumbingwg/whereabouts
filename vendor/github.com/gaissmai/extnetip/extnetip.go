// Package extnetip is an extension to net/netip providing auxiliary functions
// for converting IP prefixes to IP ranges and vice versa.
//
// The calculations are done efficiently in uint128 space, avoiding conversions
// to/from byte slices. These extensions allow easy implementation of third-party
// libraries for IP range management on top of net/netip.
//
// This package supports both safe and unsafe modes of operation. When built with
// the 'unsafe' build tag, conversions use unsafe.Pointer for better performance.
// Without the tag, safe byte-slice based conversions are used.
package extnetip

import (
	"iter"
	"net/netip"
)

// Range returns the inclusive IP address range [first, last]
// covered by the given prefix p.
//
// The prefix p does not have to be canonical.
//
// If p is invalid, Range returns zero values.
//
// The range calculation is performed by masking the uint128
// representation according to the prefix bits.
func Range(p netip.Prefix) (first, last netip.Addr) {
	if !p.IsValid() {
		return
	}

	// Extract internal representation of the prefix address as addr struct
	pa := unwrap(p.Addr())

	bits := p.Bits()
	if pa.is4() {
		// IPv4 addresses are embedded in IPv6 space with a 96-bit prefix
		bits += 96
	}
	mask := mask6(bits) // get the network mask as uint128

	// Calculate first IP in range: ip & mask
	first128 := pa.ip.and(mask)

	// Calculate last IP in range: first | ^mask
	last128 := first128.or(mask.not())

	// wrap back to netip.Addr, preserving IPv4 or IPv6 form
	first = wrap(fromUint128(first128, pa.is4()))
	last = wrap(fromUint128(last128, pa.is4()))

	return
}

// Prefix tries to determine if the inclusive range [first, last]
// can be exactly represented as a single netip.Prefix.
// It returns the prefix and ok=true if so.
//
// Returns ok=false for ranges that don't align exactly to a prefix,
// invalid IPs, mismatched versions or first > last.
//
// The calculation is done by analyzing the uint128 values
// and checking prefix match conditions.
func Prefix(first, last netip.Addr) (prefix netip.Prefix, ok bool) {
	// invalid IP
	if !first.IsValid() || !last.IsValid() {
		return
	}

	a := unwrap(first) // low-level uint128 view of first
	b := unwrap(last)  // low-level uint128 view of last

	// Check address family consistency.
	if a.is4() != b.is4() {
		return
	}

	// Ensure ordering: first <= last
	if a.ip.compare(b.ip) == 1 {
		return
	}

	// Determine prefix length and validity for exact CIDR match
	bits, ok := a.ip.prefixOK(b.ip)
	if !ok {
		return
	}

	if a.is4() {
		// For IPv4 mapped in IPv6 space, adjust prefix length
		bits -= 96
	}

	// Construct prefix from first IP and prefix length bits.
	return netip.PrefixFrom(first, bits), ok
}

// All returns an iterator over all netip.Prefix values that
// cover the entire inclusive IP range [first, last].
//
// If either IP is invalid, the order is wrong, or versions differ,
// the iterator yields no results.
//
// This uses a recursive subdivision approach to partition
// the range into a minimal set of CIDRs.
func All(first, last netip.Addr) iter.Seq[netip.Prefix] {
	return func(yield func(netip.Prefix) bool) {
		// invalid IP
		if !first.IsValid() || !last.IsValid() {
			return
		}

		a := unwrap(first) // low-level uint128 view of first
		b := unwrap(last)  // low-level uint128 view of last

		// Check address family consistency.
		if a.is4() != b.is4() {
			return
		}

		// Ensure ordering: first <= last
		if a.ip.compare(b.ip) == 1 {
			return
		}

		// Start recursive subdivision and yield prefixes
		allRec(a, b, yield)
	}
}

// allRec recursively yields prefixes for the IP range [a, b].
//
// If the range [a, b] exactly matches a prefix, yields that prefix.
//
// Otherwise recursively splits the range into halves and processes both.
//
// All bit arithmetic and masking is done in uint128 space.
func allRec(a, b addr, yield func(netip.Prefix) bool) bool {
	// Check if [a, b] is exactly a prefix range
	lcp, ok := a.ip.prefixOK(b.ip)
	if ok {
		// Found exact CIDR match - yield it and stop recursion
		if a.is4() {
			lcp -= 96 // Adjust for IPv4-in-IPv6 embedding
		}
		return yield(netip.PrefixFrom(wrap(a), lcp))
	}

	// Range doesn't match a single CIDR - split it in half
	mask := mask6(lcp + 1)                                 // Mask for one bit longer prefix
	leftUpper := fromUint128(a.ip.or(mask.not()), a.is4()) // Left half upper bound
	rightLower := fromUint128(b.ip.and(mask), a.is4())     // Right half lower bound

	// Recursively process both halves
	return allRec(a, leftUpper, yield) && allRec(rightLower, b, yield)
}

// Deprecated: Prefixes is deprecated. Use the iterator version [All] instead.
func Prefixes(first, last netip.Addr) []netip.Prefix {
	return PrefixesAppend(nil, first, last)
}

// Deprecated: PrefixesAppend is deprecated. Use the iterator version [All] instead.
func PrefixesAppend(dst []netip.Prefix, first, last netip.Addr) []netip.Prefix {
	for pfx := range All(first, last) {
		dst = append(dst, pfx)
	}
	return dst
}

// CommonPrefix returns the longest prefix shared by pfx1 and pfx2.
// It returns the zero value if a prefix is invalid or if the IP
// versions do not match. Otherwise it compares both addresses
// and returns the prefix covering their common range.
func CommonPrefix(pfx1, pfx2 netip.Prefix) (pfx netip.Prefix) {
	if !pfx1.IsValid() || !pfx2.IsValid() {
		return
	}

	addr1 := pfx1.Masked().Addr()
	addr2 := pfx2.Masked().Addr()

	is4 := addr1.Is4()
	if addr2.Is4() != is4 {
		return
	}

	ext1 := unwrap(addr1)
	ext2 := unwrap(addr2)

	// count matching bits in the full 128-bit space
	commonBits := ext1.ip.commonPrefixLen(ext2.ip)
	if is4 {
		commonBits -= 96 // adjust offset for IPv4
	}

	// final length must not exceed shorter input prefix
	minBits := min(commonBits, pfx1.Bits(), pfx2.Bits())

	// return normalized prefix; addr1 or addr2 works equally
	return netip.PrefixFrom(addr1, minBits).Masked()
}
