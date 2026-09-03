package kubernetes

import (
	"context"
	"errors"
	"fmt"
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
