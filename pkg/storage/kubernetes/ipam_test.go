package kubernetes

import (
	"context"
	"net"
	"testing"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// mockIPPool implements storage.IPPool for testing rollbackCommitted.
type mockIPPool struct {
	allocations []types.IPReservation
	updated     bool
}

func (m *mockIPPool) Allocations() []types.IPReservation {
	return m.allocations
}

func (m *mockIPPool) Update(_ context.Context, reservations []types.IPReservation) error {
	m.allocations = reservations
	m.updated = true
	return nil
}

func TestRollbackCommitted(t *testing.T) {
	ip1 := net.ParseIP("10.0.0.1")
	ip2 := net.ParseIP("10.0.0.2")
	ip3 := net.ParseIP("10.0.0.3")

	pool1 := &mockIPPool{
		allocations: []types.IPReservation{
			{IP: ip1, PodRef: "ns/pod1", IfName: "eth0"},
			{IP: ip2, PodRef: "ns/pod2", IfName: "eth0"},
		},
	}
	pool2 := &mockIPPool{
		allocations: []types.IPReservation{
			{IP: ip3, PodRef: "ns/pod1", IfName: "eth0"},
		},
	}

	committed := []committedAlloc{
		{pool: pool1, ip: ip1},
		{pool: pool2, ip: ip3},
	}

	rollbackCommitted(context.Background(), committed)

	// pool1 should have ip1 removed, ip2 remaining.
	if !pool1.updated {
		t.Fatal("pool1 should have been updated")
	}
	if len(pool1.allocations) != 1 {
		t.Fatalf("expected 1 allocation in pool1, got %d", len(pool1.allocations))
	}
	if !pool1.allocations[0].IP.Equal(ip2) {
		t.Errorf("expected remaining IP %s, got %s", ip2, pool1.allocations[0].IP)
	}

	// pool2 should have ip3 removed, empty.
	if !pool2.updated {
		t.Fatal("pool2 should have been updated")
	}
	if len(pool2.allocations) != 0 {
		t.Fatalf("expected 0 allocations in pool2, got %d", len(pool2.allocations))
	}
}

func TestIPPoolName(t *testing.T) {
	cases := []struct {
		name           string
		poolIdentifier PoolIdentifier
		expectedResult string
	}{
		{
			name: "No node name, unnamed network",
			poolIdentifier: PoolIdentifier{
				NetworkName: UnnamedNetwork,
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "10.0.0.0-8",
		},
		{
			name: "No node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "test",
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "test-10.0.0.0-8",
		},
		{
			name: "Node name, unnamed network",
			poolIdentifier: PoolIdentifier{
				NetworkName: UnnamedNetwork,
				NodeName:    "testnode",
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "testnode-10.0.0.0-8",
		},
		{
			name: "Node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "testnetwork",
				NodeName:    "testnode",
				IPRange:     "10.0.0.0/8",
			},
			expectedResult: "testnetwork-testnode-10.0.0.0-8",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := IPPoolName(tc.poolIdentifier)
			if result != tc.expectedResult {
				t.Errorf("Expected result: %s, got result: %s", tc.expectedResult, result)
			}
		})
	}
}

func TestToIPReservationList(t *testing.T) {
	firstIP := net.ParseIP("10.0.0.0")

	cases := []struct {
		name        string
		allocations map[string]whereaboutsv1alpha1.IPAllocation
		firstIP     net.IP
		expectedIPs []string
	}{
		{
			name:        "empty allocations",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
			firstIP:     firstIP,
			expectedIPs: []string{},
		},
		{
			name: "simple offset",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
				"5": {PodRef: "ns/pod2", ContainerID: "c2", IfName: "eth0"},
			},
			firstIP:     firstIP,
			expectedIPs: []string{"10.0.0.1", "10.0.0.5"},
		},
		{
			name: "invalid offset is skipped",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"1":       {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
				"notanum": {PodRef: "ns/bad", ContainerID: "c3", IfName: "eth0"},
			},
			firstIP:     firstIP,
			expectedIPs: []string{"10.0.0.1"},
		},
		{
			name: "negative offset is skipped",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"-1": {PodRef: "ns/neg", ContainerID: "c4", IfName: "eth0"},
				"2":  {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
			},
			firstIP:     firstIP,
			expectedIPs: []string{"10.0.0.2"},
		},
		{
			name: "large offset beyond uint64 max",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"18446744073709551616": {PodRef: "ns/pod-big", ContainerID: "c5", IfName: "eth0"},
			},
			firstIP:     net.ParseIP("fd00::"),
			expectedIPs: []string{"fd00::1:0:0:0:0"},
		},
		{
			name: "max uint64 offset",
			allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"18446744073709551615": {PodRef: "ns/pod-max", ContainerID: "c6", IfName: "eth0"},
			},
			firstIP:     net.ParseIP("::"),
			expectedIPs: []string{"::ffff:ffff:ffff:ffff"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := toIPReservationList(tc.allocations, tc.firstIP)
			if len(result) != len(tc.expectedIPs) {
				t.Fatalf("expected %d reservations, got %d", len(tc.expectedIPs), len(result))
			}
			for _, expected := range tc.expectedIPs {
				expectedIP := net.ParseIP(expected)
				found := false
				for _, r := range result {
					if r.IP.Equal(expectedIP) {
						found = true
						break
					}
				}
				if !found {
					var got []string
					for _, r := range result {
						got = append(got, r.IP.String())
					}
					t.Errorf("expected IP %s not found in results: %v", expected, got)
				}
			}
		})
	}
}

func TestToAllocationMapRoundTrip(t *testing.T) {
	firstIP := net.ParseIP("10.0.0.0")
	reservations := []types.IPReservation{
		{IP: net.ParseIP("10.0.0.1"), PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
		{IP: net.ParseIP("10.0.0.10"), PodRef: "ns/pod2", ContainerID: "c2", IfName: "net1"},
	}

	allocMap, err := toAllocationMap(reservations, firstIP)
	if err != nil {
		t.Fatalf("toAllocationMap failed: %v", err)
	}

	roundTripped := toIPReservationList(allocMap, firstIP)
	if len(roundTripped) != len(reservations) {
		t.Fatalf("round-trip length mismatch: expected %d, got %d", len(reservations), len(roundTripped))
	}

	originalIPs := make(map[string]types.IPReservation)
	for _, r := range reservations {
		originalIPs[r.IP.String()] = r
	}
	for _, r := range roundTripped {
		orig, ok := originalIPs[r.IP.String()]
		if !ok {
			t.Errorf("unexpected IP %s after round-trip", r.IP)
			continue
		}
		if r.PodRef != orig.PodRef || r.ContainerID != orig.ContainerID || r.IfName != orig.IfName {
			t.Errorf("metadata mismatch for IP %s: got podRef=%s containerID=%s ifName=%s, want podRef=%s containerID=%s ifName=%s",
				r.IP, r.PodRef, r.ContainerID, r.IfName, orig.PodRef, orig.ContainerID, orig.IfName)
		}
	}
}

func TestToAllocationMapRoundTripIPv6LargeOffset(t *testing.T) {
	// Use a /64 prefix — the full uint64 range (and beyond) should be addressable.
	firstIP := net.ParseIP("fd00::")
	// Offset = 2^64, which exceeds uint64 max — only possible with big.Int.
	highIP := net.ParseIP("fd00::1:0:0:0:0")

	reservations := []types.IPReservation{
		{IP: highIP, PodRef: "ns/pod-high", ContainerID: "c1", IfName: "eth0"},
	}

	allocMap, err := toAllocationMap(reservations, firstIP)
	if err != nil {
		t.Fatalf("toAllocationMap failed: %v", err)
	}

	// Verify the offset key is the expected large value.
	expectedKey := "18446744073709551616" // 2^64
	if _, ok := allocMap[expectedKey]; !ok {
		t.Fatalf("expected allocation key %s, got keys: %v", expectedKey, allocMap)
	}

	// Round-trip back.
	roundTripped := toIPReservationList(allocMap, firstIP)
	if len(roundTripped) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(roundTripped))
	}
	if !roundTripped[0].IP.Equal(highIP) {
		t.Errorf("round-trip IP mismatch: expected %s, got %s", highIP, roundTripped[0].IP)
	}
}
