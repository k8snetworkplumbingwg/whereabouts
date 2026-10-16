package kubernetes

import (
	"net"
	"testing"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
)

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
				IpRange:     "10.0.0.0/8",
			},
			expectedResult: "10.0.0.0-8",
		},
		{
			name: "No node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "test",
				IpRange:     "10.0.0.0/8",
			},
			expectedResult: "test-10.0.0.0-8",
		},
		{
			name: "Node name, unnamed network",
			poolIdentifier: PoolIdentifier{
				NetworkName: UnnamedNetwork,
				NodeName:    "testnode",
				IpRange:     "10.0.0.0/8",
			},
			expectedResult: "testnode-10.0.0.0-8",
		},
		{
			name: "Node name, named network",
			poolIdentifier: PoolIdentifier{
				NetworkName: "testnetwork",
				NodeName:    "testnode",
				IpRange:     "10.0.0.0/8",
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

func TestToIPReservationList_LargeIPv6Offset(t *testing.T) {
	firstIP := net.ParseIP("2a00:8a01:8030:101::")

	cases := []struct {
		name        string
		offset      string
		expectedIP  string
		expectFound bool
	}{
		{
			name:        "small offset within int64 range",
			offset:      "1",
			expectedIP:  "2a00:8a01:8030:101::1",
			expectFound: true,
		},
		{
			name:        "offset exceeding int64 max",
			offset:      "18446744073709490180",
			expectedIP:  "2a00:8a01:8030:101:ffff:ffff:ffff:1004",
			expectFound: true,
		},
		{
			name:        "offset at int64 boundary",
			offset:      "9223372036854775808",
			expectedIP:  "2a00:8a01:8030:101:8000::",
			expectFound: true,
		},
		{
			name:        "invalid offset",
			offset:      "notanumber",
			expectFound: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allocations := map[string]whereaboutsv1alpha1.IPAllocation{
				tc.offset: {
					ContainerID: "test-container",
					PodRef:      "test-ns/test-pod",
					IfName:      "net1",
				},
			}

			result := toIPReservationList(allocations, firstIP)

			if tc.expectFound {
				if len(result) != 1 {
					t.Fatalf("expected 1 reservation, got %d", len(result))
				}
				if result[0].IP.String() != tc.expectedIP {
					t.Errorf("expected IP %s, got %s", tc.expectedIP, result[0].IP.String())
				}
				if result[0].ContainerID != "test-container" {
					t.Errorf("expected container ID test-container, got %s", result[0].ContainerID)
				}
			} else {
				if len(result) != 0 {
					t.Errorf("expected 0 reservations for invalid offset, got %d", len(result))
				}
			}
		})
	}
}

func TestToIPReservationListAndBackRoundTrip(t *testing.T) {
	firstIP := net.ParseIP("2a00:8a01:8030:101::")
	offsets := []string{"0", "1", "9223372036854775807", "9223372036854775808", "18446744073709551615"}

	for _, offset := range offsets {
		t.Run("offset_"+offset, func(t *testing.T) {
			allocations := map[string]whereaboutsv1alpha1.IPAllocation{
				offset: {
					ContainerID: "ctr-1",
					PodRef:      "ns/pod-1",
					IfName:      "net1",
				},
			}

			reservations := toIPReservationList(allocations, firstIP)
			if len(reservations) != 1 {
				t.Fatalf("expected 1 reservation, got %d", len(reservations))
			}

			roundTripped, err := toAllocationMap(reservations, firstIP)
			if err != nil {
				t.Fatalf("toAllocationMap failed: %v", err)
			}

			if _, ok := roundTripped[offset]; !ok {
				keys := make([]string, 0, len(roundTripped))
				for k := range roundTripped {
					keys = append(keys, k)
				}
				t.Errorf("expected offset key %q in round-tripped map, got keys: %v", offset, keys)
			}
		})
	}
}
