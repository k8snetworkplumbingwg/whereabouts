// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"strings"
	"testing"
)

func TestValidateCIDRExtended(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{name: "IPv4 /0", cidr: "0.0.0.0/0", wantErr: false},
		{name: "IPv6 /128", cidr: "::1/128", wantErr: false},
		{name: "IPv6 /0", cidr: "::/0", wantErr: false},
		{name: "host address with mask", cidr: "10.0.0.1/24", wantErr: false},
		{name: "prefix too large for IPv4", cidr: "10.0.0.0/33", wantErr: true},
		{name: "prefix too large for IPv6", cidr: "fd00::/129", wantErr: true},
		{name: "negative prefix", cidr: "10.0.0.0/-1", wantErr: true},
		{name: "only slash", cidr: "/24", wantErr: true},
		{name: "double CIDR", cidr: "10.0.0.0/24/8", wantErr: true},
		{name: "whitespace around valid CIDR", cidr: " 10.0.0.0/24 ", wantErr: true},
		{name: "IPv4-mapped IPv6", cidr: "::ffff:10.0.0.0/120", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCIDR(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCIDR(%q) error = %v, wantErr %v", tt.cidr, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePodRefExtended(t *testing.T) {
	tests := []struct {
		name     string
		podRef   string
		required bool
		wantErr  bool
		errMsg   string
	}{
		// Intentional leniency: ValidatePodRef only enforces "namespace/name" shape
		// via SplitN(podRef, "/", 2). It does NOT attempt to mirror Kubernetes
		// DNS-1123 resource name validation. Unicode and long strings are accepted
		// because the CNI plugin stores podRefs as-is from the runtime, and
		// Kubernetes itself enforces naming constraints at the API server level.
		// This means values like Unicode namespaces or spaces may produce
		// orphaned reservations that can never match a real pod. Namespaces with
		// embedded spaces (e.g. "name space") cannot exist in Kubernetes but are
		// still accepted here to keep this validator focused purely on shape, not
		// full Kubernetes name validity.
		{name: "unicode namespace", podRef: "ünïcödé/pod", required: true, wantErr: false},
		{name: "very long podRef", podRef: strings.Repeat("a", 253) + "/" + strings.Repeat("b", 253), required: true, wantErr: false},
		{name: "spaces in namespace", podRef: "name space/pod", required: true, wantErr: false},
		{name: "single-space namespace", podRef: " /pod", required: true, wantErr: true},
		// Note: ValidatePodRef trims namespace/name segments with strings.TrimSpace.
		// A segment containing only spaces (e.g. "ns/ ") becomes empty and is rejected,
		// whereas embedded spaces (e.g. "name space/pod") survive trimming and are
		// accepted because this validator only enforces the "namespace/name" shape.
		{name: "single-space name", podRef: "ns/ ", required: true, wantErr: true},
		{name: "slash only", podRef: "/", required: false, wantErr: true},
		{name: "double slash", podRef: "//", required: false, wantErr: true, errMsg: "namespace/name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePodRef(tt.podRef, tt.required)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePodRef(%q, %v) error = %v, wantErr %v", tt.podRef, tt.required, err, tt.wantErr)
			}
			if tt.errMsg != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidatePodRef(%q, %v) error = %q, want substring %q", tt.podRef, tt.required, err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestValidateSliceSizeExtended(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "negative number", input: "-1", want: 0, wantErr: true},
		{name: "negative with slash", input: "/-1", want: 0, wantErr: true},
		{name: "very large number", input: "9999", want: 0, wantErr: true},
		{name: "float", input: "24.5", want: 0, wantErr: true},
		{name: "hex", input: "0x18", want: 0, wantErr: true},
		// Go's strconv.Atoi parses "024" as decimal 24 (base 10), not octal 20.
		{name: "leading zero", input: "024", want: 24, wantErr: false},
		{name: "whitespace padded", input: " 24 ", want: 0, wantErr: true},
		{name: "slash only", input: "/", want: 0, wantErr: true},
		{name: "double slash", input: "//24", want: 0, wantErr: true},
		{name: "boundary 1", input: "1", want: 1, wantErr: false},
		{name: "boundary 128", input: "128", want: 128, wantErr: false},
		{name: "just above boundary", input: "129", want: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateSliceSize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSliceSize(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ValidateSliceSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
