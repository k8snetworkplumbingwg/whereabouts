//go:build unsafe

// Package extnetip unsafe conversion implementation.
//
// This file is used when built with the 'unsafe' build tag.
// It provides fast conversions using unsafe.Pointer operations but requires
// that the internal layout of netip.Addr remains stable.
//
// WARNING: This implementation depends on internal Go standard library
// implementation details and may break with future Go versions.

package extnetip

import (
	"net/netip"
	"unsafe"
)

// addr is a low-level struct used to interpret netip.Addr internals via unsafe.Pointer.
//
// It contains:
// - ip: the raw 128-bit IP address data as a uint128.
// - z:  a uintptr used internally by netip.Addr to track address kind/discriminator.
//
// This struct layout must match netip.Addr exactly for unsafe conversions to work.
type addr struct {
	ip uint128
	z  uintptr
}

// Internal singleton pointers extracted from zero-value netip.Addr instances.
// These uintptr values correspond to internal discriminators used by netip.Addr
// to distinguish the kind of IP address representation.
//
// z4    - IPv4 address representation
// z6noz - IPv6 address representation without zone
var (
	z4    uintptr
	z6noz uintptr
)

// Compile-time and runtime sanity checks: fail fast if layout changes.
// If sizes differ this line triggers a compile-time error (array length mismatch).
var _ [1]struct{} = [1 - int(unsafe.Sizeof(addr{})-unsafe.Sizeof(netip.Addr{}))]struct{}{}

func init() {
	if unsafe.Alignof(addr{}) != unsafe.Alignof(netip.Addr{}) {
		panic("extnetip: netip.Addr alignment changed; rebuild without -tags=unsafe")
	}

	// Initialize discriminators only after alignment is verified.
	z4 = unwrap(netip.AddrFrom4([4]byte{})).z
	z6noz = unwrap(netip.AddrFrom16([16]byte{})).z
}

// fromUint128 constructs an addr struct from a uint128 IP representation and a flag is4.
//
// If is4 is true, the addr assumes the IPv4 internal discriminator (z4).
// Otherwise, it uses the IPv6 no-zone internal discriminator (z6noz).
func fromUint128(ip uint128, is4 bool) addr {
	if is4 {
		return addr{ip, z4}
	}
	return addr{ip, z6noz}
}

// is4 checks whether the internal address representation corresponds to an IPv4 address.
//
// It compares the addr's z discriminator with the known IPv4 singleton z4.
func (a *addr) is4() bool {
	return a.z == z4
}

// unwrap converts a netip.Addr value into the internal addr representation using unsafe.Pointer.
//
// This is effectively a cast that allows direct access to netip.Addr internals without copying.
// Must only be used if struct layouts are confirmed to match.
//
// Precondition: a is a valid IP address.
func unwrap(a netip.Addr) addr {
	return *(*addr)(unsafe.Pointer(&a))
}

// wrap converts from the internal addr representation back to netip.Addr.
//
// It reconstructs a valid netip.Addr value by pointer-casting the addr.
//
// Use with caution; meaningful only if addr and netip.Addr share memory layout.
func wrap(a addr) netip.Addr {
	return *(*netip.Addr)(unsafe.Pointer(&a))
}
