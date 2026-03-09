//go:build !unsafe

// Package extnetip safe conversion implementation.
//
// This file is used when the 'unsafe' build tag is NOT specified.
// It provides safe conversions between netip.Addr and internal uint128
// representation using standard library byte manipulation functions.
//
// While slower than the unsafe version, this approach avoids potential
// issues with unsafe pointer operations and is suitable for environments
// where unsafe operations are prohibited.

package extnetip

import (
	"encoding/binary"
	"net/netip"
)

// addr holds the IP address as uint128 data and a flag if it is IPv4.
//
// This struct is used for arithmetic or comparison operations
// on netip.Addr data in a safe manner.
type addr struct {
	ip uint128
	v4 bool
}

// fromUint128 creates an addr struct from a uint128 IP representation and a flag is4.
//
// The addr struct stores the raw 128-bit IP as two uint64 fields and an indicator
// whether the IP is IPv4 or IPv6.
//
// This version relies on safe, explicit encoding/decoding and does not
// use unsafe pointers.
func fromUint128(ip uint128, is4 bool) addr {
	return addr{ip, is4}
}

// is4 returns true if the address represents an IPv4 value.
func (a *addr) is4() bool {
	return a.v4
}

// unwrap extracts the raw uint128 representation from a netip.Addr safely.
//
// It converts the netip.Addr into a []byte slice representing the IP.
//
//   - If the length is 4 (IPv4), it sets the field v4 to true and decodes the 4 bytes
//     as a uint32 into the low 64 bits.
//
//   - Otherwise it decodes the first 8 bytes as the high 64 bits and the
//     next 8 bytes as the low 64 bits of the IP.
//
// This function avoids unsafe.Pointer usage by working explicitly with
// byte slices and binary decoding.
//
// Precondition: a is a valid IP address.
func unwrap(a netip.Addr) (b addr) {
	ip := a.AsSlice() // nil if a isn't valid!

	if len(ip) == 4 {
		b.v4 = true
		b.ip.lo = uint64(binary.BigEndian.Uint32(ip))
		return b
	}

	b.ip.hi = binary.BigEndian.Uint64(ip[:8])
	b.ip.lo = binary.BigEndian.Uint64(ip[8:])

	return b
}

// wrap converts an addr back to a netip.Addr using safe conversions via byte arrays.
//
// It reconstructs a 16-byte array in big endian encoding from the uint128 value.
//
//   - If the addr represents IPv4, it extracts 4 bytes from the last 4 bytes of the array,
//     then converts with netip.AddrFrom4().
//
// - Otherwise, it returns an IPv6 netip.Addr from the full 16 bytes.
//
// This approach is fully safe and compatible with Go standard library interfaces.
func wrap(a addr) netip.Addr {
	var a16 [16]byte
	binary.BigEndian.PutUint64(a16[8:], a.ip.lo)

	if a.v4 {
		return netip.AddrFrom4([4]byte(a16[12:]))
	}

	binary.BigEndian.PutUint64(a16[:8], a.ip.hi)
	return netip.AddrFrom16(a16)
}
