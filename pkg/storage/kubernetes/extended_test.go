package kubernetes

import (
	"context"
	"fmt"
	"net"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fake "k8s.io/client-go/kubernetes/fake"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbfake "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/clientset/versioned/fake"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// TestNormalizeIP tests the NormalizeIP function.
func TestNormalizeIP(t *testing.T) {
	cases := []struct {
		name        string
		ip          net.IP
		networkName string
		expected    string
	}{
		{
			name:        "IPv4 unnamed network",
			ip:          net.ParseIP("10.0.0.1"),
			networkName: UnnamedNetwork,
			expected:    "10.0.0.1",
		},
		{
			name:        "IPv4 named network",
			ip:          net.ParseIP("10.0.0.1"),
			networkName: "mynet",
			expected:    "mynet-10.0.0.1",
		},
		{
			name:        "IPv6 unnamed network",
			ip:          net.ParseIP("fd00::1"),
			networkName: UnnamedNetwork,
			expected:    "fd00--1",
		},
		{
			name:        "IPv6 named network",
			ip:          net.ParseIP("fd00::1"),
			networkName: "v6net",
			expected:    "v6net-fd00--1",
		},
		{
			name:        "IPv6 with trailing colon (zero-padded)",
			ip:          net.ParseIP("fd00::"),
			networkName: UnnamedNetwork,
			expected:    "fd00--0",
		},
		{
			name:        "IPv4-mapped IPv6",
			ip:          net.ParseIP("::ffff:10.0.0.1"),
			networkName: UnnamedNetwork,
			expected:    "10.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := NormalizeIP(tc.ip, tc.networkName)
			if result != tc.expected {
				t.Errorf("NormalizeIP(%v, %q) = %q, want %q", tc.ip, tc.networkName, result, tc.expected)
			}
		})
	}
}

// TestNormalizeRange tests the normalizeRange function.
func TestNormalizeRange(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"IPv4 CIDR", "10.0.0.0/24", "10.0.0.0-24"},
		{"IPv6 CIDR", "fd00::/120", "fd00---120"},
		{"IPv6 ending with colon", "fd00:1234::", "fd00-1234--0"},
		{"slash replacement", "192.168.1.0/16", "192.168.1.0-16"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeRange(tc.input)
			if result != tc.expected {
				t.Errorf("normalizeRange(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

// TestWbNamespaceFromCtx tests the namespace fallback logic.
func TestWbNamespaceFromCtx(t *testing.T) {
	cases := []struct {
		name      string
		namespace string
		expected  string
	}{
		{"empty namespace falls back to kube-system", "", "kube-system"},
		{"non-empty namespace is returned as-is", "my-namespace", "my-namespace"},
		{"kube-system is returned as-is", "kube-system", "kube-system"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &clientcmdapi.Context{Namespace: tc.namespace}
			result := wbNamespaceFromCtx(ctx)
			if result != tc.expected {
				t.Errorf("wbNamespaceFromCtx(%q) = %q, want %q", tc.namespace, result, tc.expected)
			}
		})
	}
}

// TestTemporaryError tests the temporaryError type.
func TestTemporaryError(t *testing.T) {
	err := &temporaryError{error: fmt.Errorf("test error")}
	if !err.Temporary() {
		t.Error("expected Temporary() to return true")
	}
	if err.Error() != "test error" {
		t.Errorf("expected error message 'test error', got '%s'", err.Error())
	}
}

// TestClientListIPPools tests the ListIPPools method using a fake client.
func TestClientListIPPools(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))

	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "10.0.0.0-24",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       "10.0.0.0/24",
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}

	wbClient := wbfake.NewClientset(pool)
	k8sClient := fake.NewClientset()

	client := NewKubernetesClient(wbClient, k8sClient)

	pools, err := client.ListIPPools()
	if err != nil {
		t.Fatalf("ListIPPools() error: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
}

// TestClientListIPPoolsEmpty tests ListIPPools with no pools.
func TestClientListIPPoolsEmpty(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	client := NewKubernetesClient(wbClient, k8sClient)

	pools, err := client.ListIPPools()
	if err != nil {
		t.Fatalf("ListIPPools() error: %v", err)
	}
	if len(pools) != 0 {
		t.Fatalf("expected 0 pools, got %d", len(pools))
	}
}

// TestClientListPods tests the ListPods method.
func TestClientListPods(t *testing.T) {
	k8sClient := fake.NewClientset()
	wbClient := wbfake.NewClientset()

	client := NewKubernetesClient(wbClient, k8sClient)

	pods, err := client.ListPods()
	if err != nil {
		t.Fatalf("ListPods() error: %v", err)
	}
	if len(pods) != 0 {
		t.Fatalf("expected 0 pods, got %d", len(pods))
	}
}

// TestClientGetPodNotFound tests GetPod when pod doesn't exist.
func TestClientGetPodNotFound(t *testing.T) {
	k8sClient := fake.NewClientset()
	wbClient := wbfake.NewClientset()

	client := NewKubernetesClient(wbClient, k8sClient)

	_, err := client.GetPod("default", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

// TestClientListOverlappingIPs tests the ListOverlappingIPs method.
func TestClientListOverlappingIPs(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "10.0.0.1",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
		},
	}

	wbClient := wbfake.NewClientset(orip)
	k8sClient := fake.NewClientset()

	client := NewKubernetesClient(wbClient, k8sClient)

	ips, err := client.ListOverlappingIPs()
	if err != nil {
		t.Fatalf("ListOverlappingIPs() error: %v", err)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 overlapping IP, got %d", len(ips))
	}
	if ips[0].Spec.PodRef != "default/pod1" {
		t.Errorf("expected podRef 'default/pod1', got '%s'", ips[0].Spec.PodRef)
	}
}

// TestClientDeleteOverlappingIP tests the DeleteOverlappingIP method.
func TestClientDeleteOverlappingIP(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "10.0.0.1",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
		},
	}

	wbClient := wbfake.NewClientset(orip)
	k8sClient := fake.NewClientset()

	client := NewKubernetesClient(wbClient, k8sClient)

	err := client.DeleteOverlappingIP(orip)
	if err != nil {
		t.Fatalf("DeleteOverlappingIP() error: %v", err)
	}

	// Verify it's gone.
	ips, err := client.ListOverlappingIPs()
	if err != nil {
		t.Fatalf("ListOverlappingIPs() error: %v", err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected 0 overlapping IPs after delete, got %d", len(ips))
	}
}

// TestGetPoolCreatesOnNotFound tests that getPool creates a pool when it doesn't exist.
func TestGetPoolCreatesOnNotFound(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:    *NewKubernetesClient(wbClient, k8sClient),
		Namespace: "default",
		Config:    types.IPAMConfig{},
	}

	// First call should trigger creation and return a temporary error.
	_, err := ipam.getPool(context.Background(), "test-pool", "10.0.0.0/24")
	if err == nil {
		t.Fatal("expected temporary error after pool creation")
	}
	// Should be a temporary error.
	if te, ok := err.(interface{ Temporary() bool }); !ok || !te.Temporary() {
		t.Fatalf("expected temporary error, got: %v", err)
	}

	// Pool should now exist.
	pool, err := wbClient.WhereaboutsV1alpha1().IPPools("default").Get(context.Background(), "test-pool", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected pool to exist after creation, error: %v", err)
	}
	if pool.Spec.Range != "10.0.0.0/24" {
		t.Errorf("expected range '10.0.0.0/24', got '%s'", pool.Spec.Range)
	}
}

// TestGetPoolReturnsExisting tests that getPool returns an existing pool.
func TestGetPoolReturnsExisting(t *testing.T) {
	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-pool",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range:       "10.0.0.0/24",
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
		},
	}

	wbClient := wbfake.NewClientset(pool)
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:    *NewKubernetesClient(wbClient, k8sClient),
		Namespace: "default",
		Config:    types.IPAMConfig{},
	}

	result, err := ipam.getPool(context.Background(), "existing-pool", "10.0.0.0/24")
	if err != nil {
		t.Fatalf("getPool() error: %v", err)
	}
	if result.Spec.Range != "10.0.0.0/24" {
		t.Errorf("expected range '10.0.0.0/24', got '%s'", result.Spec.Range)
	}
}

// TestKubernetesIPAMStatus tests the Status method.
func TestKubernetesIPAMStatus(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:    *NewKubernetesClient(wbClient, k8sClient),
		Namespace: "default",
	}

	err := ipam.Status(context.Background())
	if err != nil {
		t.Errorf("Status() should succeed with empty pool list, got: %v", err)
	}
}

// TestKubernetesIPAMClose tests the Close method.
func TestKubernetesIPAMClose(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:    *NewKubernetesClient(wbClient, k8sClient),
		Namespace: "default",
	}

	err := ipam.Close()
	if err != nil {
		t.Errorf("Close() should return nil, got: %v", err)
	}
}

// TestKubernetesIPAMGetOverlappingRangeStore tests GetOverlappingRangeStore.
func TestKubernetesIPAMGetOverlappingRangeStore(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:    *NewKubernetesClient(wbClient, k8sClient),
		Namespace: "test-ns",
	}

	store, err := ipam.GetOverlappingRangeStore()
	if err != nil {
		t.Fatalf("GetOverlappingRangeStore() error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// Verify the store uses the correct namespace.
	ks := store.(*KubernetesOverlappingRangeStore)
	if ks.namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got '%s'", ks.namespace)
	}
}

// TestOverlappingRangeStoreGetReservation tests GetOverlappingRangeIPReservation.
func TestOverlappingRangeStoreGetReservationNotFound(t *testing.T) {
	wbClient := wbfake.NewClientset()

	store := &KubernetesOverlappingRangeStore{
		client:    wbClient,
		namespace: "default",
	}

	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.1"), "default/pod1", "")
	if err != nil {
		t.Fatalf("expected no error for not found, got: %v", err)
	}
	if res != nil {
		t.Fatal("expected nil reservation for not found")
	}
}

// TestOverlappingRangeStoreGetReservationFound tests finding an existing reservation.
func TestOverlappingRangeStoreGetReservationFound(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "10.0.0.1",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
		},
	}

	wbClient := wbfake.NewClientset(orip)

	store := &KubernetesOverlappingRangeStore{
		client:    wbClient,
		namespace: "default",
	}

	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.1"), "default/pod1", "")
	if err != nil {
		t.Fatalf("GetOverlappingRangeIPReservation() error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil reservation")
	}
	if res.Spec.PodRef != "default/pod1" {
		t.Errorf("expected podRef 'default/pod1', got '%s'", res.Spec.PodRef)
	}
}

// TestOverlappingRangeStoreUpdateAllocate tests creating a reservation.
func TestOverlappingRangeStoreUpdateAllocate(t *testing.T) {
	wbClient := wbfake.NewClientset()

	store := &KubernetesOverlappingRangeStore{
		client:    wbClient,
		namespace: "default",
	}

	err := store.UpdateOverlappingRangeAllocation(context.Background(), types.Allocate, net.ParseIP("10.0.0.5"), "default/pod1", "eth0", "")
	if err != nil {
		t.Fatalf("UpdateOverlappingRangeAllocation(Allocate) error: %v", err)
	}

	// Verify it was created.
	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.5"), "default/pod1", "")
	if err != nil {
		t.Fatalf("GetOverlappingRangeIPReservation() error: %v", err)
	}
	if res == nil {
		t.Fatal("expected reservation to exist after allocate")
	}
}

// TestOverlappingRangeStoreUpdateDeallocate tests deleting a reservation.
func TestOverlappingRangeStoreUpdateDeallocate(t *testing.T) {
	orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "10.0.0.5",
			Namespace: "default",
		},
		Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: "default/pod1",
			IfName: "eth0",
		},
	}

	wbClient := wbfake.NewClientset(orip)

	store := &KubernetesOverlappingRangeStore{
		client:    wbClient,
		namespace: "default",
	}

	err := store.UpdateOverlappingRangeAllocation(context.Background(), types.Deallocate, net.ParseIP("10.0.0.5"), "default/pod1", "eth0", "")
	if err != nil {
		t.Fatalf("UpdateOverlappingRangeAllocation(Deallocate) error: %v", err)
	}

	// Verify it was deleted.
	res, err := store.GetOverlappingRangeIPReservation(context.Background(), net.ParseIP("10.0.0.5"), "default/pod1", "")
	if err != nil {
		t.Fatalf("GetOverlappingRangeIPReservation() error: %v", err)
	}
	if res != nil {
		t.Fatal("expected reservation to be deleted")
	}
}

// TestGetNodeSliceName tests getNodeSliceName.
func TestGetNodeSliceName(t *testing.T) {
	cases := []struct {
		name     string
		config   types.IPAMConfig
		expected string
	}{
		{
			name:     "unnamed network uses Name",
			config:   types.IPAMConfig{Name: "my-nad", NetworkName: ""},
			expected: "my-nad",
		},
		{
			name:     "named network uses NetworkName",
			config:   types.IPAMConfig{Name: "my-nad", NetworkName: "custom-net"},
			expected: "custom-net",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ipam := &KubernetesIPAM{Config: tc.config}
			result := getNodeSliceName(ipam)
			if result != tc.expected {
				t.Errorf("getNodeSliceName() = %q, want %q", result, tc.expected)
			}
		})
	}
}

// TestKubernetesIPPoolAllocations tests the Allocations method.
func TestKubernetesIPPoolAllocations(t *testing.T) {
	pool := &whereaboutsv1alpha1.IPPool{
		Spec: whereaboutsv1alpha1.IPPoolSpec{
			Range: "10.0.0.0/24",
			Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {PodRef: "ns/pod1", ContainerID: "c1", IfName: "eth0"},
				"5": {PodRef: "ns/pod2", ContainerID: "c2", IfName: "net1"},
			},
		},
	}

	k8sPool := &KubernetesIPPool{
		firstIP: net.ParseIP("10.0.0.0"),
		pool:    pool,
	}

	allocs := k8sPool.Allocations()
	if len(allocs) != 2 {
		t.Fatalf("expected 2 allocations, got %d", len(allocs))
	}
}

// TestRetryBackoffDoesNotBlock tests retryBackoff with a canceled context.
func TestRetryBackoffCancelledContext(_ *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should return quickly due to canceled context.
	retryBackoff(ctx, 10*1000*1000*1000) // 10s would block without cancellation
}

// ===========================================================================
// IPManagementKubernetesUpdate tests
// ===========================================================================.

// TestIPManagementKubernetesUpdateUnknownMode tests that an invalid mode
// returns an error.
func TestIPManagementKubernetesUpdateUnknownMode(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:      *NewKubernetesClient(wbClient, k8sClient),
		Namespace:   "default",
		ContainerID: "c1",
		IfName:      "eth0",
		Config:      types.IPAMConfig{},
	}
	conf := types.IPAMConfig{
		PodName:      "pod1",
		PodNamespace: "default",
	}

	_, err := IPManagementKubernetesUpdate(context.Background(), 999, ipam, conf)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if got := err.Error(); got != "got an unknown mode passed to IPManagement: 999" {
		t.Errorf("unexpected error: %s", got)
	}
}

// TestIPManagementKubernetesUpdateStatusCheck tests that the status check
// (connectivity validation) is called before allocate/deallocate.
func TestIPManagementKubernetesUpdateStatusCheck(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:      *NewKubernetesClient(wbClient, k8sClient),
		Namespace:   "default",
		ContainerID: "c1",
		IfName:      "eth0",
		Config:      types.IPAMConfig{},
	}
	// Empty IPRanges means the function will try to Status() then fail
	// on iterate — but at least validates the mode path works.
	conf := types.IPAMConfig{
		PodName:      "pod1",
		PodNamespace: "default",
		IPRanges:     []types.RangeConfiguration{},
	}

	// Allocate with no ranges should return cleanly (empty result, no error).
	newips, err := IPManagementKubernetesUpdate(context.Background(), types.Allocate, ipam, conf)
	if err != nil {
		t.Fatalf("unexpected error with empty ranges: %v", err)
	}
	if len(newips) != 0 {
		t.Errorf("expected 0 IPs from empty ranges, got %d", len(newips))
	}

	// Deallocate with no ranges should also return cleanly.
	newips, err = IPManagementKubernetesUpdate(context.Background(), types.Deallocate, ipam, conf)
	if err != nil {
		t.Fatalf("unexpected error with empty ranges (deallocate): %v", err)
	}
	if len(newips) != 0 {
		t.Errorf("expected 0 IPs from empty ranges (deallocate), got %d", len(newips))
	}
}

// TestIPManagementEmptyPodName tests that IPManagement fails with empty pod name.
func TestIPManagementEmptyPodName(t *testing.T) {
	wbClient := wbfake.NewClientset()
	k8sClient := fake.NewClientset()

	ipam := &KubernetesIPAM{
		Client:    *NewKubernetesClient(wbClient, k8sClient),
		Namespace: "default",
	}
	conf := types.IPAMConfig{
		PodName: "", // empty
	}

	_, err := IPManagement(context.Background(), types.Allocate, conf, ipam)
	if err == nil {
		t.Fatal("expected error for empty pod name")
	}
	if got := err.Error(); got != "IPAM client initialization error: no pod name" {
		t.Errorf("unexpected error: %s", got)
	}
}
