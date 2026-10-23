package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	fakewbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/clientset/versioned/fake"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	whereaboutstypes "github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// TestIPPoolName verifies IPPool CR naming for various network name and node name combinations.
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

const (
	testNamespace   = "kube-system"
	testIPRange     = "10.10.0.0/16"
	testContainerID = "test-container-id"
	testIfName      = "eth0"
	testPodName     = "test-pod"
	testPodNS       = "default"
	testNetworkName = "testnet"
)

// newTestIPAM creates a KubernetesIPAM backed by a fake whereabouts client.
func newTestIPAM(wbClient *fakewbclient.Clientset) *KubernetesIPAM {
	return &KubernetesIPAM{
		Client:      *NewKubernetesClient(wbClient, nil),
		Config:      whereaboutstypes.IPAMConfig{},
		Namespace:   testNamespace,
		ContainerID: testContainerID,
		IfName:      testIfName,
	}
}

// newTestIPAMConfig returns an IPAMConfig with overlapping ranges enabled.
func newTestIPAMConfig() whereaboutstypes.IPAMConfig {
	return whereaboutstypes.IPAMConfig{
		IPRanges:          []whereaboutstypes.RangeConfiguration{{Range: testIPRange}},
		OverlappingRanges: true,
		PodName:           testPodName,
		PodNamespace:      testPodNS,
		NetworkName:       testNetworkName,
	}
}

// seedIPPool creates an empty IPPool in the fake client for the given pool identifier.
func seedIPPool(t *testing.T, wbClient *fakewbclient.Clientset, poolID PoolIdentifier) {
	t.Helper()
	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            IPPoolName(poolID),
			Namespace:       testNamespace,
			ResourceVersion: "1",
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       poolID.IpRange,
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}
	_, err := wbClient.WhereaboutsV1alpha1().IPPools(testNamespace).Create(
		context.Background(), pool, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to seed IPPool: %v", err)
	}
}

// testPoolID returns the default PoolIdentifier used across tests.
func testPoolID() PoolIdentifier {
	return PoolIdentifier{IpRange: testIPRange, NetworkName: testNetworkName}
}

// TestIPManagementKubernetesUpdate_PoolUpdateFailurePreventsORIPUpdate verifies that a permanent
// pool.Update failure returns an error and does not create an OverlappingRangeIPReservation.
func TestIPManagementKubernetesUpdate_PoolUpdateFailurePreventsORIPUpdate(t *testing.T) {
	origRetries := storage.DatastoreRetries
	storage.DatastoreRetries = 3
	t.Cleanup(func() { storage.DatastoreRetries = origRetries })

	wbClient := fakewbclient.NewSimpleClientset()
	seedIPPool(t, wbClient, testPoolID())

	patchErr := fmt.Errorf("permanent patch failure")
	wbClient.PrependReactor("patch", "ippools", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, patchErr
	})

	var oripCalls atomic.Int32
	wbClient.PrependReactor("create", "overlappingrangeipreservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		oripCalls.Add(1)
		return false, nil, nil
	})

	ipam := newTestIPAM(wbClient)
	ipamConf := newTestIPAMConfig()

	_, err := IPManagementKubernetesUpdate(context.Background(), whereaboutstypes.Allocate, ipam, ipamConf)
	if err == nil {
		t.Fatal("expected error from pool.Update failure, got nil")
	}
	if err.Error() != patchErr.Error() {
		t.Errorf("expected error %q, got %q", patchErr, err)
	}
	if oripCalls.Load() != 0 {
		t.Errorf("expected 0 ORIP create calls after pool.Update failure, got %d", oripCalls.Load())
	}
}

// TestIPManagementKubernetesUpdate_RetriesExhaustedPreventsORIPUpdate verifies that exhausting all
// temporary-error retries returns an error and does not create an OverlappingRangeIPReservation.
func TestIPManagementKubernetesUpdate_RetriesExhaustedPreventsORIPUpdate(t *testing.T) {
	origRetries := storage.DatastoreRetries
	storage.DatastoreRetries = 3
	t.Cleanup(func() { storage.DatastoreRetries = origRetries })

	wbClient := fakewbclient.NewSimpleClientset()
	seedIPPool(t, wbClient, testPoolID())

	var patchCalls atomic.Int32
	wbClient.PrependReactor("patch", "ippools", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls.Add(1)
		return true, nil, &temporaryError{fmt.Errorf("conflict")}
	})

	var oripCalls atomic.Int32
	wbClient.PrependReactor("create", "overlappingrangeipreservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		oripCalls.Add(1)
		return false, nil, nil
	})

	ipam := newTestIPAM(wbClient)
	ipamConf := newTestIPAMConfig()

	_, err := IPManagementKubernetesUpdate(context.Background(), whereaboutstypes.Allocate, ipam, ipamConf)
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	if patchCalls.Load() != int32(storage.DatastoreRetries) {
		t.Errorf("expected %d patch attempts, got %d", storage.DatastoreRetries, patchCalls.Load())
	}
	if oripCalls.Load() != 0 {
		t.Errorf("expected 0 ORIP create calls after retries exhausted, got %d", oripCalls.Load())
	}
}

// TestIPManagementKubernetesUpdate_OverlappingRetriesExhaustedPreventsORIPUpdate verifies that
// overlap conflicts on every retry return an error without updating the pool or ORIP.
func TestIPManagementKubernetesUpdate_OverlappingRetriesExhaustedPreventsORIPUpdate(t *testing.T) {
	origRetries := storage.DatastoreRetries
	storage.DatastoreRetries = 3
	t.Cleanup(func() { storage.DatastoreRetries = origRetries })

	wbClient := fakewbclient.NewSimpleClientset()
	seedIPPool(t, wbClient, testPoolID())

	for _, ip := range []string{"10.10.0.1", "10.10.0.2", "10.10.0.3"} {
		reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      NormalizeIP(net.ParseIP(ip), testNetworkName),
				Namespace: testNamespace,
			},
			Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
				PodRef: "default/another-pod",
			},
		}
		_, err := wbClient.WhereaboutsV1alpha1().OverlappingRangeIPReservations(testNamespace).Create(
			context.Background(), reservation, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to seed overlapping range reservation for %s: %v", ip, err)
		}
	}

	var overlapGetCalls, patchCalls, oripCreateCalls atomic.Int32
	wbClient.PrependReactor("get", "overlappingrangeipreservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		overlapGetCalls.Add(1)
		return false, nil, nil
	})
	wbClient.PrependReactor("patch", "ippools", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls.Add(1)
		return false, nil, nil
	})
	wbClient.PrependReactor("create", "overlappingrangeipreservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		oripCreateCalls.Add(1)
		return false, nil, nil
	})

	ipam := newTestIPAM(wbClient)
	ipamConf := newTestIPAMConfig()

	_, err := IPManagementKubernetesUpdate(context.Background(), whereaboutstypes.Allocate, ipam, ipamConf)
	if err == nil {
		t.Fatal("expected error after overlap conflicts exhaust retries, got nil")
	}
	if overlapGetCalls.Load() != int32(storage.DatastoreRetries) {
		t.Errorf("expected %d overlap reservation lookups, got %d", storage.DatastoreRetries, overlapGetCalls.Load())
	}
	if patchCalls.Load() != 0 {
		t.Errorf("expected 0 IPPool patch calls after overlap conflicts, got %d", patchCalls.Load())
	}
	if oripCreateCalls.Load() != 0 {
		t.Errorf("expected 0 ORIP create calls after overlap conflicts, got %d", oripCreateCalls.Load())
	}
}

// TestIPManagementKubernetesUpdate_ContextCanceledBeforePoolUpdatePreventsMutation verifies that
// cancellation after the status check is caught before the retry loop mutates the pool or ORIP.
func TestIPManagementKubernetesUpdate_ContextCanceledBeforePoolUpdatePreventsMutation(t *testing.T) {
	wbClient := fakewbclient.NewSimpleClientset()
	seedIPPool(t, wbClient, testPoolID())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var statusListCalls, patchCalls, oripCreateCalls atomic.Int32
	wbClient.PrependReactor("list", "ippools", func(action k8stesting.Action) (bool, runtime.Object, error) {
		statusListCalls.Add(1)
		cancel()
		return true, &whereaboutsv1alpha1.IPPoolList{}, nil
	})
	wbClient.PrependReactor("patch", "ippools", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls.Add(1)
		return false, nil, nil
	})
	wbClient.PrependReactor("create", "overlappingrangeipreservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		oripCreateCalls.Add(1)
		return false, nil, nil
	})

	ipam := newTestIPAM(wbClient)
	ipamConf := newTestIPAMConfig()

	_, err := IPManagementKubernetesUpdate(ctx, whereaboutstypes.Allocate, ipam, ipamConf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if statusListCalls.Load() != 1 {
		t.Errorf("expected 1 IPPool status list call, got %d", statusListCalls.Load())
	}
	if patchCalls.Load() != 0 {
		t.Errorf("expected 0 IPPool patch calls after cancellation, got %d", patchCalls.Load())
	}
	if oripCreateCalls.Load() != 0 {
		t.Errorf("expected 0 ORIP create calls after cancellation, got %d", oripCreateCalls.Load())
	}
}

// TestIPManagementKubernetesUpdate_ContextCanceledPreventsORIPUpdate verifies that canceling the
// context during a successful pool.Update prevents ORIP creation via the post-loop ctx.Err() guard.
func TestIPManagementKubernetesUpdate_ContextCanceledPreventsORIPUpdate(t *testing.T) {
	wbClient := fakewbclient.NewSimpleClientset()
	seedIPPool(t, wbClient, testPoolID())

	ctx, cancel := context.WithCancel(context.Background())

	var patchCalls atomic.Int32
	wbClient.PrependReactor("patch", "ippools", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls.Add(1)
		cancel()
		return false, nil, nil
	})

	var oripCalls atomic.Int32
	wbClient.PrependReactor("create", "overlappingrangeipreservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		oripCalls.Add(1)
		return false, nil, nil
	})

	ipam := newTestIPAM(wbClient)
	ipamConf := newTestIPAMConfig()

	_, err := IPManagementKubernetesUpdate(ctx, whereaboutstypes.Allocate, ipam, ipamConf)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if patchCalls.Load() != 1 {
		t.Errorf("expected 1 IPPool patch call, got %d", patchCalls.Load())
	}
	if oripCalls.Load() != 0 {
		t.Errorf("expected 0 ORIP create calls after context cancellation, got %d", oripCalls.Load())
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
