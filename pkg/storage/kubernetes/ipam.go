package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"gomodules.xyz/jsonpatch/v2"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/allocate"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/clientset/versioned"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/iphelpers"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	whereaboutstypes "github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const UnnamedNetwork string = ""

const (
	// retryInitialBackoff is the starting backoff duration between retries.
	retryInitialBackoff = 5 * time.Millisecond
	// retryMaxBackoff caps the exponential backoff to avoid excessive waits.
	retryMaxBackoff = 1 * time.Second
)

// retryBackoff sleeps for a jittered duration, respecting context cancellation.
func retryBackoff(ctx context.Context, d time.Duration) {
	// Apply ±50% jitter: sleep for d/2 + rand(d/2)
	half := int64(d / 2)
	if half <= 0 {
		half = 1
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(half))
	jittered := time.Duration(half + n.Int64())
	select {
	case <-time.After(jittered):
	case <-ctx.Done():
	}
}

// KubernetesIPAM manages IP address blocks using Kubernetes CRDs as the
// storage backend. It embeds Client for API access and carries the per-request
// context needed to perform allocations and deallocations.
type KubernetesIPAM struct {
	// Client provides access to the Kubernetes and Whereabouts API clients.
	Client
	// Config is the parsed IPAM configuration for this allocation request.
	Config whereaboutstypes.IPAMConfig
	// Namespace is the Kubernetes namespace where IPPool CRDs are stored.
	Namespace string
	// ContainerID is the CNI container ID for the current request.
	ContainerID string
	// IfName is the network interface name inside the container.
	IfName string
}

func newKubernetesIPAM(containerID, ifName string, ipamConf whereaboutstypes.IPAMConfig, namespace string, kubernetesClient Client) *KubernetesIPAM {
	return &KubernetesIPAM{
		Config:      ipamConf,
		ContainerID: containerID,
		IfName:      ifName,
		Namespace:   namespace,
		Client:      kubernetesClient,
	}
}

// NewKubernetesIPAM returns a new KubernetesIPAM Client configured to a kubernetes CRD backend.
func NewKubernetesIPAM(containerID, ifName string, ipamConf whereaboutstypes.IPAMConfig) (*KubernetesIPAM, error) {
	var namespace string
	if cfg, err := clientcmd.LoadFromFile(ipamConf.Kubernetes.KubeConfigPath); err != nil {
		return nil, err
	} else if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctx != nil {
		namespace = wbNamespaceFromCtx(ctx)
	} else {
		return nil, fmt.Errorf("k8s config: namespace not present in context")
	}

	kubernetesClient, err := NewClientViaKubeconfig(ipamConf.Kubernetes.KubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed instantiating kubernetes client: %w", err)
	}
	k8sIPAM := newKubernetesIPAM(containerID, ifName, ipamConf, namespace, *kubernetesClient)
	return k8sIPAM, nil
}

// NewKubernetesIPAMWithNamespace returns a new KubernetesIPAM Client configured to a kubernetes CRD backend.
func NewKubernetesIPAMWithNamespace(containerID, ifName string, ipamConf whereaboutstypes.IPAMConfig, namespace string) (*KubernetesIPAM, error) {
	k8sIPAM, err := NewKubernetesIPAM(containerID, ifName, ipamConf)
	if err != nil {
		return nil, err
	}
	k8sIPAM.Namespace = namespace
	return k8sIPAM, nil
}

type PoolIdentifier struct {
	IPRange     string
	NetworkName string
	NodeName    string
}

// GetIPPool returns a storage.IPPool for the given range.
func (i *KubernetesIPAM) GetIPPool(ctx context.Context, poolIdentifier PoolIdentifier) (storage.IPPool, error) {
	name := IPPoolName(poolIdentifier)

	pool, err := i.getPool(ctx, name, poolIdentifier.IPRange)
	if err != nil {
		return nil, err
	}

	firstIP, _, err := pool.ParseCIDR()
	if err != nil {
		return nil, err
	}

	return &KubernetesIPPool{i.client, firstIP, pool}, nil
}

func IPPoolName(poolIdentifier PoolIdentifier) string {
	if poolIdentifier.NodeName != "" {
		// fast node range naming convention
		if poolIdentifier.NetworkName == UnnamedNetwork {
			return fmt.Sprintf("%v-%v", poolIdentifier.NodeName, normalizeRange(poolIdentifier.IPRange))
		}
		return fmt.Sprintf("%v-%v-%v", poolIdentifier.NetworkName, poolIdentifier.NodeName, normalizeRange(poolIdentifier.IPRange))
	}

	// default naming convention
	if poolIdentifier.NetworkName == UnnamedNetwork {
		return normalizeRange(poolIdentifier.IPRange)
	}
	return fmt.Sprintf("%s-%s", poolIdentifier.NetworkName, normalizeRange(poolIdentifier.IPRange))
}

// normalizeRange converts an IP range CIDR into a string suitable for use as a
// Kubernetes resource name. Colons (IPv6) and slashes (CIDR notation) are
// replaced with dashes because metadata.name must match RFC 1123 DNS subdomain.
func normalizeRange(ipRange string) string {
	if ipRange == "" {
		return ""
	}
	// Trailing colon in abbreviated IPv6 (e.g. "2001::") needs a zero appended
	// so the replacement below produces a valid name segment.
	if ipRange[len(ipRange)-1] == ':' {
		ipRange += "0"
	}
	normalized := strings.ReplaceAll(ipRange, ":", "-")
	normalized = strings.ReplaceAll(normalized, "/", "-")
	return normalized
}

func (i *KubernetesIPAM) getPool(ctx context.Context, name string, iprange string) (*whereaboutsv1alpha1.IPPool, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, storage.RequestTimeout)
	defer cancel()

	pool, err := i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Get(ctxWithTimeout, name, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		// pool does not exist, create it
		newPool := &whereaboutsv1alpha1.IPPool{}
		newPool.Name = name
		newPool.Spec.Range = iprange
		newPool.Spec.Allocations = make(map[string]whereaboutsv1alpha1.IPAllocation)
		_, err = i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).Create(ctxWithTimeout, newPool, metav1.CreateOptions{})
		if err != nil && errors.IsAlreadyExists(err) {
			// the pool was just created -- allow retry
			return nil, &temporaryError{err}
		} else if err != nil {
			return nil, fmt.Errorf("k8s create error: %w", err)
		}
		// if the pool was created for the first time, trigger another retry of the allocation loop
		// so all of the metadata / resourceVersions are populated as necessary by the `client.Get` call
		return nil, &temporaryError{ErrPoolInitialized}
	} else if err != nil {
		return nil, fmt.Errorf("k8s get error: %w", err)
	}
	return pool, nil
}

// Status tests connectivity to the kubernetes backend.
func (i *KubernetesIPAM) Status(ctx context.Context) error {
	_, err := i.client.WhereaboutsV1alpha1().IPPools(i.Namespace).List(ctx, metav1.ListOptions{})
	return err
}

// Close partially implements the Store interface.
func (i *KubernetesIPAM) Close() error {
	return nil
}

// KubernetesIPPool represents an IPPool resource and its parsed set of allocations.
type KubernetesIPPool struct {
	client  wbclient.Interface
	firstIP net.IP
	pool    *whereaboutsv1alpha1.IPPool
}

// Allocations returns the initially retrieved set of allocations for this pool.
func (p *KubernetesIPPool) Allocations() []whereaboutstypes.IPReservation {
	return toIPReservationList(p.pool.Spec.Allocations, p.firstIP)
}

// Update sets the pool allocated IP list to the given IP reservations.
func (p *KubernetesIPPool) Update(ctx context.Context, reservations []whereaboutstypes.IPReservation) error {
	// marshal the current pool to serve as the base for the patch creation
	orig := p.pool.DeepCopy()
	origBytes, err := json.Marshal(orig)
	if err != nil {
		return err
	}

	// update the pool before marshaling once again
	allocations, err := toAllocationMap(reservations, p.firstIP)
	if err != nil {
		return err
	}
	p.pool.Spec.Allocations = allocations
	modBytes, err := json.Marshal(p.pool)
	if err != nil {
		return err
	}

	// create the patch
	patch, err := jsonpatch.CreatePatch(origBytes, modBytes)
	if err != nil {
		return err
	}

	// add additional tests to the patch
	ops := []jsonpatch.Operation{
		// ensure patch is applied to appropriate resource version only
		{Operation: "test", Path: "/metadata/resourceVersion", Value: orig.ResourceVersion},
	}
	for _, o := range patch {
		// safeguard add ops -- "add" will update existing paths, this "test" ensures the path is empty
		if o.Operation == "add" {
			var m map[string]interface{}
			ops = append(ops, jsonpatch.Operation{Operation: "test", Path: o.Path, Value: m})
		}
	}
	ops = append(ops, patch...)
	patchData, err := json.Marshal(ops)
	if err != nil {
		return err
	}

	// apply the patch
	_, err = p.client.WhereaboutsV1alpha1().IPPools(orig.GetNamespace()).Patch(ctx, orig.GetName(), types.JSONPatchType, patchData, metav1.PatchOptions{})
	if err != nil {
		if errors.IsInvalid(err) {
			// expect "invalid" errors if any of the jsonpatch "test" Operations fail
			return &temporaryError{err}
		}
		return err
	}

	return nil
}

func toIPReservationList(allocations map[string]whereaboutsv1alpha1.IPAllocation, firstip net.IP) []whereaboutstypes.IPReservation {
	reservelist := []whereaboutstypes.IPReservation{}
	for offset, a := range allocations {
		numOffset, ok := new(big.Int).SetString(offset, 10)
		if !ok || numOffset.Sign() < 0 {
			// allocations that are not valid non-negative integers should be ignored
			logging.Errorf("Error decoding ip offset (backend: kubernetes): invalid offset %q", offset)
			continue
		}
		ip := iphelpers.IPAddOffset(firstip, numOffset)
		reservelist = append(reservelist, whereaboutstypes.IPReservation{IP: ip, ContainerID: a.ContainerID, PodRef: a.PodRef, IfName: a.IfName})
	}
	return reservelist
}

func toAllocationMap(reservelist []whereaboutstypes.IPReservation, firstip net.IP) (map[string]whereaboutsv1alpha1.IPAllocation, error) {
	allocations := make(map[string]whereaboutsv1alpha1.IPAllocation)
	for _, r := range reservelist {
		index, err := iphelpers.IPGetOffset(r.IP, firstip)
		if err != nil {
			return nil, err
		}
		allocations[index.String()] = whereaboutsv1alpha1.IPAllocation{ContainerID: r.ContainerID, PodRef: r.PodRef, IfName: r.IfName}
	}
	return allocations, nil
}

// KubernetesOverlappingRangeStore represents a OverlappingRangeStore interface.
type KubernetesOverlappingRangeStore struct {
	client    wbclient.Interface
	namespace string
}

// GetOverlappingRangeStore returns a clusterstore interface.
func (i *KubernetesIPAM) GetOverlappingRangeStore() (storage.OverlappingRangeStore, error) {
	return &KubernetesOverlappingRangeStore{i.client, i.Namespace}, nil
}

// IsAllocatedInOverlappingRange checks for IP addresses to see if they're allocated cluster wide, for overlapping
// ranges. First return value is true if the IP is allocated, second return value is true if the IP is allocated to the
// current podRef.
func (c *KubernetesOverlappingRangeStore) GetOverlappingRangeIPReservation(ctx context.Context, ip net.IP,
	_ /* podRef */, networkName string) (*whereaboutsv1alpha1.OverlappingRangeIPReservation, error) {
	normalizedIP := NormalizeIP(ip, networkName)

	logging.Debugf("Get overlappingRangewide allocation; normalized IP: %q, IP: %q, networkName: %q",
		normalizedIP, ip, networkName)

	r, err := c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Get(ctx, normalizedIP, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		// cluster ip reservation does not exist, this appears to be good news.
		return nil, nil
	} else if err != nil {
		logging.Errorf("k8s get OverlappingRangeIPReservation error: %w", err)
		return nil, fmt.Errorf("k8s get OverlappingRangeIPReservation error: %w", err)
	}

	logging.Debugf("Normalized IP is reserved; normalized IP: %q, IP: %q, networkName: %q",
		normalizedIP, ip, networkName)
	return r, nil
}

// UpdateOverlappingRangeAllocation updates clusterwide allocation for overlapping ranges.
func (c *KubernetesOverlappingRangeStore) UpdateOverlappingRangeAllocation(ctx context.Context, mode int, ip net.IP,
	podRef, ifName, networkName string) error {
	normalizedIP := NormalizeIP(ip, networkName)

	clusteripres := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: normalizedIP, Namespace: c.namespace},
	}

	var err error
	var verb string
	switch mode {
	case whereaboutstypes.Allocate:
		// Put together our cluster ip reservation
		verb = "allocate"

		clusteripres.Spec = whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			PodRef: podRef,
			IfName: ifName,
		}

		_, err = c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Create(
			ctx, clusteripres, metav1.CreateOptions{})

	case whereaboutstypes.Deallocate:
		verb = "deallocate"
		err = c.client.WhereaboutsV1alpha1().OverlappingRangeIPReservations(c.namespace).Delete(ctx, clusteripres.GetName(), metav1.DeleteOptions{})
	}

	if err != nil {
		return err
	}

	logging.Debugf("K8s UpdateOverlappingRangeAllocation success on %v: %+v", verb, clusteripres)
	return nil
}

// NormalizeIP normalizes the IP. This is important for IPv6 which doesn't make for valid CR names. It also allows us
// to add the network-name when it's different from the unnamed network.
func NormalizeIP(ip net.IP, networkName string) string {
	ipStr := ip.String()
	if ipStr[len(ipStr)-1] == ':' {
		ipStr += "0"
		logging.Debugf("modified: %s", ipStr)
	}
	normalizedIP := strings.ReplaceAll(ipStr, ":", "-")
	if networkName != UnnamedNetwork {
		normalizedIP = fmt.Sprintf("%s-%s", networkName, normalizedIP)
	}
	return normalizedIP
}

// getNodeName prefers an OS env var of NODENAME, or, uses a file named ./nodename in the whereabouts configuration path.
func getNodeName(ipam *KubernetesIPAM) (string, error) {
	envName := os.Getenv("NODENAME")
	if envName != "" {
		return strings.TrimSpace(envName), nil
	}

	nodeNamePath := fmt.Sprintf("%s/%s", ipam.Config.ConfigurationPath, "nodename")
	file, err := os.Open(nodeNamePath)
	if err != nil {
		file, err = os.Open("/etc/hostname")
		if err != nil {
			logging.Errorf("Could not determine nodename and could not open /etc/hostname: %w", err)
			return "", err
		}
	}
	defer file.Close()

	// Read the contents of the file
	data := make([]byte, 1024) // Adjust the buffer size as needed
	n, err := file.Read(data)
	if err != nil {
		logging.Errorf("Error reading file /etc/hostname: %w", err)
	}

	// Convert bytes to string
	hostname := string(data[:n])
	hostname = strings.TrimSpace(hostname)
	logging.Debugf("discovered current hostname as: %s", hostname)
	return hostname, nil
}

// newLeaderElector creates a new leaderelection.LeaderElector and associated
// channels by which to observe elections and depositions.
func newLeaderElector(ctx context.Context, clientset kubernetes.Interface, namespace string, ipamConf *KubernetesIPAM) (elector *leaderelection.LeaderElector, leaderOK, deposed chan struct{}) {
	// leaderOK will block gRPC startup until it's closed.
	leaderOK = make(chan struct{})
	// deposed is closed by the leader election callback when
	// we are deposed as leader so that we can clean up.
	deposed = make(chan struct{})

	leaseName := "whereabouts"
	if ipamConf.Config.NodeSliceSize != "" {
		// we lock per IP Pool so just use the pool name for the lease name
		hostname, err := getNodeName(ipamConf)
		if err != nil {
			logging.Errorf("Failed to create leader elector: %w", err)
			return nil, leaderOK, deposed
		}
		nodeSliceRange, err := GetNodeSlicePoolRange(ctx, ipamConf, hostname)
		if err != nil {
			logging.Errorf("Failed to create leader elector: %w", err)
			return nil, leaderOK, deposed
		}
		leaseName = IPPoolName(PoolIdentifier{IPRange: nodeSliceRange, NodeName: hostname, NetworkName: ipamConf.Config.NetworkName})
	}
	logging.Debugf("using lease with name: %v", leaseName)

	var rl = &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: fmt.Sprintf("%s/%s", ipamConf.Config.PodNamespace, ipamConf.Config.PodName),
		},
	}

	// Make the leader elector, ready to be used in the Workgroup.
	// !bang
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            rl,
		LeaseDuration:   time.Duration(ipamConf.Config.LeaderLeaseDuration) * time.Millisecond,
		RenewDeadline:   time.Duration(ipamConf.Config.LeaderRenewDeadline) * time.Millisecond,
		RetryPeriod:     time.Duration(ipamConf.Config.LeaderRetryPeriod) * time.Millisecond,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				logging.Debugf("OnStartedLeading() called")
				close(leaderOK)
			},
			OnStoppedLeading: func() {
				logging.Debugf("OnStoppedLeading() called")
				// The context being canceled will trigger a handler that will
				// deal with being deposed.
				close(deposed)
			},
		},
	})
	if err != nil {
		logging.Errorf("Failed to create leader elector: %w", err)
		return nil, leaderOK, deposed
	}
	return le, leaderOK, deposed
}

// IPManagement orchestrates IP allocation or deallocation using leader election.
// It acquires a Kubernetes lease lock, then delegates to IPManagementKubernetesUpdate
// to perform the actual pool update. The mode parameter must be types.Allocate or
// types.Deallocate. Returns the list of assigned IP networks (for Allocate) or nil
// (for Deallocate). The function blocks until the operation completes, the context
// is canceled, or leader election fails.
func IPManagement(ctx context.Context, mode int, ipamConf whereaboutstypes.IPAMConfig, client *KubernetesIPAM) ([]net.IPNet, error) {
	var newips []net.IPNet

	if ipamConf.PodName == "" {
		return newips, fmt.Errorf("IPAM client initialization error: no pod name")
	}

	// setup leader election
	le, leader, deposed := newLeaderElector(ctx, client.clientSet, client.Namespace, client)
	if le == nil {
		return newips, fmt.Errorf("failed to create leader elector")
	}
	var wg sync.WaitGroup
	wg.Add(2)

	stopM := make(chan struct{}, 1)
	result := make(chan error, 2)

	var err error
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				err = fmt.Errorf("time limit exceeded while waiting to become leader")
				stopM <- struct{}{}
				return
			case <-leader:
				logging.Debugf("Elected as leader, do processing")
				newips, err = IPManagementKubernetesUpdate(ctx, mode, client, ipamConf)
				stopM <- struct{}{}
				return
			case <-deposed:
				logging.Debugf("Deposed as leader, shutting down")
				err = fmt.Errorf("deposed as leader, cannot complete IP management")
				stopM <- struct{}{}
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		res := make(chan error)
		leCtx, leCancel := context.WithCancel(ctx)

		go func() {
			logging.Debugf("Started leader election")
			le.Run(leCtx)
			logging.Debugf("Finished leader election")
			res <- nil
		}()

		// wait for stop which tells us when IP allocation occurred or context deadline exceeded
		<-stopM
		// leCancel fn(leCtx)
		leCancel()
		result <- (<-res)
	}()
	wg.Wait()
	close(stopM)
	logging.Debugf("IPManagement: %v, %v", newips, err)
	return newips, err
}

func GetNodeSlicePoolRange(ctx context.Context, ipam *KubernetesIPAM, nodeName string) (string, error) {
	logging.Debugf("ipam namespace is %v", ipam.Namespace)
	nodeSlice, err := ipam.client.WhereaboutsV1alpha1().NodeSlicePools(ipam.Namespace).Get(ctx, getNodeSliceName(ipam), metav1.GetOptions{})
	if err != nil {
		logging.Errorf("error getting node slice %s/%s %w", ipam.Namespace, getNodeSliceName(ipam), err)
		return "", err
	}
	for _, allocation := range nodeSlice.Status.Allocations {
		if allocation.NodeName == nodeName {
			logging.Debugf("found matching node slice allocation for hostname %v: %v", nodeName, allocation)
			return allocation.SliceRange, nil
		}
	}
	logging.Errorf("error finding node within node slice allocations")
	return "", fmt.Errorf("no allocated node slice for node")
}

func getNodeSliceName(ipam *KubernetesIPAM) string {
	if ipam.Config.NetworkName == UnnamedNetwork {
		return ipam.Config.Name
	}
	return ipam.Config.NetworkName
}

// committedAlloc tracks a successfully committed pool allocation for rollback.
type committedAlloc struct {
	pool storage.IPPool
	ip   net.IP
}

// IPManagementKubernetesUpdate manages k8s updates.
func IPManagementKubernetesUpdate(ctx context.Context, mode int, ipam *KubernetesIPAM, ipamConf whereaboutstypes.IPAMConfig) (newips []net.IPNet, retErr error) {
	logging.Debugf("IPManagement -- mode: %d / containerID: %q / podRef: %q / ifName: %q ", mode, ipam.ContainerID, ipamConf.GetPodRef(), ipam.IfName)

	var newip net.IPNet
	// Skip invalid modes
	switch mode {
	case whereaboutstypes.Allocate, whereaboutstypes.Deallocate:
	default:
		return newips, fmt.Errorf("got an unknown mode passed to IPManagement: %v", mode)
	}

	var overlappingrangestore storage.OverlappingRangeStore
	var pool storage.IPPool

	statusCtx, statusCancel := context.WithTimeout(ctx, storage.RequestTimeout)
	defer statusCancel()

	// Check our connectivity first
	if err := ipam.Status(statusCtx); err != nil {
		logging.Errorf("IPAM connectivity error: %w", err)
		return newips, err
	}

	// handle the ip add/del until successful
	// For multi-range (e.g. dual-stack), if allocation succeeds for range N
	// but fails for range N+1, we perform a best-effort rollback of the
	// earlier allocations so IPs are not left orphaned.
	var committed []committedAlloc

	// Deferred rollback: if the function returns an error during Allocate mode
	// and we have previously committed allocations, undo them.
	defer func() {
		if retErr != nil && mode == whereaboutstypes.Allocate && len(committed) > 0 {
			rollbackCommitted(context.Background(), committed)
		}
	}()

	var overlappingrangeallocations []whereaboutstypes.IPReservation
	var ipforoverlappingrangeupdate net.IP

	// Sticky IP: read the pod's preferred-ip annotation to attempt assigning
	// a specific IP address. This enables pods to retain the same IP across
	// restarts (e.g. StatefulSets). See upstream #621.
	var preferredIP net.IP
	if mode == whereaboutstypes.Allocate && ipamConf.PodName != "" && ipamConf.PodNamespace != "" {
		pod, podErr := ipam.clientSet.CoreV1().Pods(ipamConf.PodNamespace).Get(ctx, ipamConf.PodName, metav1.GetOptions{})
		if podErr == nil {
			if preferred, ok := pod.Annotations["whereabouts.cni.cncf.io/preferred-ip"]; ok {
				preferredIP = net.ParseIP(preferred)
				if preferredIP != nil {
					logging.Debugf("Pod %s has preferred IP annotation: %s", ipamConf.GetPodRef(), preferredIP)
				} else {
					logging.Debugf("Pod %s has invalid preferred-ip annotation: %q", ipamConf.GetPodRef(), preferred)
				}
			}
		}
	}

	for _, ipRange := range ipamConf.IPRanges {
		configuredRange := ipRange.Range // capture before potential node-slice reassignment
		var err error
		var attempts int
		skipOverlappingRangeUpdate := false
		backoff := retryInitialBackoff
	RETRYLOOP:
		for j := range storage.DatastoreRetries {
			attempts = j + 1
			requestCtx, requestCancel := context.WithTimeout(ctx, storage.RequestTimeout)
			select {
			case <-ctx.Done():
				requestCancel()
				break RETRYLOOP
			default:
				// retry the IPAM loop if the context has not been canceled
			}
			overlappingrangestore, err = ipam.GetOverlappingRangeStore()
			if err != nil {
				logging.Errorf("IPAM error getting OverlappingRangeStore: %w", err)
				requestCancel()
				return newips, err
			}
			poolIdentifier := PoolIdentifier{IPRange: ipRange.Range, NetworkName: ipamConf.NetworkName}
			if ipamConf.NodeSliceSize != "" {
				hostname, err := getNodeName(ipam)
				if err != nil {
					logging.Errorf("Failed to get node hostname: %w", err)
					requestCancel()
					return newips, err
				}
				poolIdentifier.NodeName = hostname
				nodeSliceRange, err := GetNodeSlicePoolRange(requestCtx, ipam, hostname)
				if err != nil {
					requestCancel()
					return newips, err
				}
				_, ipNet, err := net.ParseCIDR(nodeSliceRange)
				if err != nil {
					logging.Errorf("Error parsing node slice cidr to net.IPNet: %w", err)
					requestCancel()
					return newips, err
				}
				poolIdentifier.IPRange = nodeSliceRange
				rangeStart, err := iphelpers.FirstUsableIP(*ipNet)
				if err != nil {
					logging.Errorf("Error parsing node slice cidr to range start: %w", err)
					requestCancel()
					return newips, err
				}
				rangeEnd, err := iphelpers.LastUsableIP(*ipNet)
				if err != nil {
					logging.Errorf("Error parsing node slice cidr to range end: %w", err)
					requestCancel()
					return newips, err
				}
				ipRange = whereaboutstypes.RangeConfiguration{
					OmitRanges: ipRange.OmitRanges,
					Range:      ipRange.Range,
					RangeStart: rangeStart,
					RangeEnd:   rangeEnd,
				}
			}
			logging.Debugf("using pool identifier: %v", poolIdentifier)
			pool, err = ipam.GetIPPool(requestCtx, poolIdentifier)
			if err != nil {
				logging.Errorf("IPAM error reading pool allocations (attempt: %d): %w", j, err)
				if e, ok := err.(storage.Temporary); ok && e.Temporary() {
					requestCancel()
					retryBackoff(ctx, backoff)
					backoff = min(backoff*2, retryMaxBackoff)
					continue
				}
				requestCancel()
				return newips, err
			}

			reservelist := pool.Allocations()
			reservelist = append(reservelist, overlappingrangeallocations...)
			var updatedreservelist []whereaboutstypes.IPReservation
			switch mode {
			case whereaboutstypes.Allocate:
				// Set preferred IP from pod annotation for sticky assignment.
				if preferredIP != nil {
					ipRange.PreferredIP = preferredIP
				}
				newip, updatedreservelist, err = allocate.AssignIP(ipRange, reservelist, ipam.ContainerID, ipamConf.GetPodRef(), ipam.IfName)
				if err != nil {
					logging.Errorf("Error assigning IP: %w", err)
					requestCancel()
					return newips, err
				}
				// Now check if this is allocated overlappingrange wide
				// When it's allocated overlappingrange wide, we add it to a local reserved list
				// And we try again.
				if ipamConf.OverlappingRanges {
					overlappingRangeIPReservation, err := overlappingrangestore.GetOverlappingRangeIPReservation(requestCtx, newip.IP,
						ipamConf.GetPodRef(), ipamConf.NetworkName)
					if err != nil {
						logging.Errorf("Error getting cluster wide IP allocation: %w", err)
						requestCancel()
						return newips, err
					}

					if overlappingRangeIPReservation != nil {
						if overlappingRangeIPReservation.Spec.PodRef != ipamConf.GetPodRef() {
							logging.Debugf("Continuing loop, IP is already allocated (possibly from another range): %v", newip)
							// We create "dummy" records here for evaluation, but, we need to filter those out later.
							overlappingrangeallocations = append(overlappingrangeallocations, whereaboutstypes.IPReservation{IP: newip.IP, IsAllocated: true})
							requestCancel()
							continue
						}

						skipOverlappingRangeUpdate = true
					}

					ipforoverlappingrangeupdate = newip.IP
				}

			case whereaboutstypes.Deallocate:
				updatedreservelist, ipforoverlappingrangeupdate = allocate.DeallocateIP(reservelist, ipam.ContainerID, ipam.IfName)
				if ipforoverlappingrangeupdate == nil {
					// Allocation not found in this range — continue to remaining
					// ranges so that IPs in other ranges are still released.
					logging.Debugf("No allocation found for container ID %q in range %s, continuing to next range", ipam.ContainerID, ipRange.Range)
					skipOverlappingRangeUpdate = true
					requestCancel()
					break RETRYLOOP
				}
			}

			// Clean out any dummy records from the reservelist...
			var usereservelist []whereaboutstypes.IPReservation
			for _, rl := range updatedreservelist {
				if !rl.IsAllocated {
					usereservelist = append(usereservelist, rl)
				}
			}

			// Manual race condition testing (capped to prevent DoS)
			if ipamConf.SleepForRace > 0 {
				sleepSec := ipamConf.SleepForRace
				if sleepSec > whereaboutstypes.MaxSleepForRace {
					logging.Debugf("Capping sleep_for_race from %d to %d seconds", sleepSec, whereaboutstypes.MaxSleepForRace)
					sleepSec = whereaboutstypes.MaxSleepForRace
				}
				time.Sleep(time.Duration(sleepSec) * time.Second)
			}

			err = pool.Update(requestCtx, usereservelist)
			if err != nil {
				logging.Errorf("IPAM error updating pool (attempt: %d): %w", j, err)
				if e, ok := err.(storage.Temporary); ok && e.Temporary() {
					requestCancel()
					retryBackoff(ctx, backoff)
					backoff = min(backoff*2, retryMaxBackoff)
					continue
				}
				requestCancel()
				break RETRYLOOP
			}
			requestCancel()
			break RETRYLOOP
		}

		if err != nil {
			return newips, logging.Errorf("IP allocation failed for range %s after %d attempts: %w", configuredRange, attempts, err)
		}

		if ipamConf.OverlappingRanges {
			if !skipOverlappingRangeUpdate {
				overlappingCtx, overlappingCancel := context.WithTimeout(ctx, storage.RequestTimeout)
				err = overlappingrangestore.UpdateOverlappingRangeAllocation(overlappingCtx, mode, ipforoverlappingrangeupdate,
					ipamConf.GetPodRef(), ipam.IfName, ipamConf.NetworkName)
				overlappingCancel()
				if err != nil {
					logging.Errorf("Error performing UpdateOverlappingRangeAllocation: %w", err)
					// Best-effort rollback: if we just allocated an IP in the pool
					// but failed to create the ORIP, attempt to remove the allocation
					// so the IP isn't reserved without overlap protection.
					if mode == whereaboutstypes.Allocate && pool != nil {
						rollbackCommitted(context.Background(), []committedAlloc{{pool: pool, ip: newip.IP}})
					}
					return newips, err
				}
			}
		}

		// Track this allocation so we can roll it back if a later range fails.
		// Only append to newips in Allocate mode — during Deallocate, newip is
		// never assigned and would be a zero-value net.IPNet{}.
		if mode == whereaboutstypes.Allocate {
			committed = append(committed, committedAlloc{pool: pool, ip: newip.IP})
			newips = append(newips, newip)
		}
	}
	return newips, nil
}

// rollbackCommitted performs a best-effort rollback of previously committed
// multi-range allocations. Each allocation is removed from its pool so the IP
// is not left orphaned when a later range fails.
func rollbackCommitted(ctx context.Context, committed []committedAlloc) {
	for _, c := range committed {
		allocs := c.pool.Allocations()
		var cleaned []whereaboutstypes.IPReservation
		for _, r := range allocs {
			if !r.IP.Equal(c.ip) {
				cleaned = append(cleaned, r)
			}
		}
		rbCtx, rbCancel := context.WithTimeout(ctx, storage.RequestTimeout)
		if err := c.pool.Update(rbCtx, cleaned); err != nil {
			logging.Errorf("Multi-range rollback failed for IP %s: %w", c.ip, err)
		} else {
			logging.Debugf("Rolled back allocation for IP %s", c.ip)
		}
		rbCancel()
	}
}

func wbNamespaceFromCtx(ctx *clientcmdapi.Context) string {
	namespace := ctx.Namespace
	if namespace == "" {
		return metav1.NamespaceSystem
	}
	return namespace
}
