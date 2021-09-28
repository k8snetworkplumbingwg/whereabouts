package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	whereaboutsv1alpha1 "github.com/dougbtv/whereabouts/pkg/api/v1alpha1"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/storage"
	whereaboutstypes "github.com/dougbtv/whereabouts/pkg/types"
	jsonpatch "gomodules.xyz/jsonpatch/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewKubernetesIPAM returns a new KubernetesIPAM Client configured to a kubernetes CRD backend
func NewKubernetesIPAM(containerID string, ipamConf whereaboutstypes.IPAMConfig) (*KubernetesIPAM, error) {
	var namespace string
	if cfg, err := clientcmd.LoadFromFile(ipamConf.Kubernetes.KubeConfigPath); err != nil {
		return nil, err
	} else if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctx != nil {
		namespace = ctx.Namespace
	} else {
		return nil, fmt.Errorf("k8s config: namespace not present in context")
	}

	kubernetesClient, err := NewClient(ipamConf.Kubernetes.KubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed instantiating kubernetes client: %v", err)
	}
	k8sIPAM := newKubernetesIPAM(containerID, ipamConf, namespace, *kubernetesClient)
	return k8sIPAM, nil
}

func newKubernetesIPAM(containerID string, ipamConf whereaboutstypes.IPAMConfig, namespace string, kubernetesClient Client) *KubernetesIPAM {
	return &KubernetesIPAM{
		config:      ipamConf,
		containerID: containerID,
		namespace:   namespace,
		Client:      kubernetesClient,
	}
}

// KubernetesIPAM manages ip blocks in an kubernetes CRD backend
type KubernetesIPAM struct {
	Client
	config      whereaboutstypes.IPAMConfig
	containerID string
	namespace   string
}

func toIPReservationList(allocations map[string]whereaboutsv1alpha1.IPAllocation, firstip net.IP) []whereaboutstypes.IPReservation {
	reservelist := []whereaboutstypes.IPReservation{}
	for offset, a := range allocations {
		numOffset, err := strconv.ParseInt(offset, 10, 64)
		if err != nil {
			// allocations that are invalid int64s should be ignored
			// toAllocationMap should be the only writer of offsets, via `fmt.Sprintf("%d", ...)``
			logging.Errorf("Error decoding ip offset (backend: kubernetes): %v", err)
			continue
		}
		ip := allocate.IPAddOffset(firstip, uint64(numOffset))
		reservelist = append(reservelist, whereaboutstypes.IPReservation{IP: ip, ContainerID: a.ContainerID, PodRef: a.PodRef})
	}
	return reservelist
}

func toAllocationMap(reservelist []whereaboutstypes.IPReservation, firstip net.IP) map[string]whereaboutsv1alpha1.IPAllocation {
	allocations := make(map[string]whereaboutsv1alpha1.IPAllocation)
	for _, r := range reservelist {
		index := allocate.IPGetOffset(r.IP, firstip)
		allocations[fmt.Sprintf("%d", index)] = whereaboutsv1alpha1.IPAllocation{ContainerID: r.ContainerID, PodRef: r.PodRef}
	}
	return allocations
}

// GetIPPool returns a storage.IPPool for the given range
func (i *KubernetesIPAM) GetIPPool(ctx context.Context, ipRange string) (storage.IPPool, error) {
	// v6 filter
	normalized := strings.ReplaceAll(ipRange, ":", "-")
	// replace subnet cidr slash
	normalized = strings.ReplaceAll(normalized, "/", "-")

	pool, err := i.getPool(ctx, normalized, ipRange)
	if err != nil {
		return nil, err
	}

	firstIP, _, err := pool.ParseCIDR()
	if err != nil {
		return nil, err
	}

	return &KubernetesIPPool{i.client, i.containerID, firstIP, pool}, nil
}

func (i *KubernetesIPAM) getPool(ctx context.Context, name string, iprange string) (*whereaboutsv1alpha1.IPPool, error) {
	pool := &whereaboutsv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: i.namespace},
	}
	if err := i.client.Get(ctx, types.NamespacedName{Name: name, Namespace: i.namespace}, pool); errors.IsNotFound(err) {
		// pool does not exist, create it
		pool.ObjectMeta.Name = name
		pool.Spec.Range = iprange
		pool.Spec.Allocations = make(map[string]whereaboutsv1alpha1.IPAllocation)
		if err := i.client.Create(ctx, pool); errors.IsAlreadyExists(err) {
			// the pool was just created -- allow retry
			return nil, &temporaryError{err}
		} else if err != nil {
			return nil, fmt.Errorf("k8s create error: %s", err)
		}
		// if the pool was created for the first time, trigger another retry of the allocation loop
		// so all of the metadata / resourceVersions are populated as necessary by the `client.Get` call
		return nil, &temporaryError{fmt.Errorf("k8s pool initialized")}
	} else if err != nil {
		return nil, fmt.Errorf("k8s get error: %s", err)
	}
	return pool, nil
}

// Status tests connectivity to the kubernetes backend
func (i *KubernetesIPAM) Status(ctx context.Context) error {
	list := &whereaboutsv1alpha1.IPPoolList{}
	err := i.client.List(ctx, list, &client.ListOptions{Namespace: i.namespace})
	return err
}

// Close partially implements the Store interface
func (i *KubernetesIPAM) Close() error {
	return nil
}

// KubernetesOverlappingRangeStore represents a OverlappingRangeStore interface
type KubernetesOverlappingRangeStore struct {
	client      client.Client
	containerID string
	namespace   string
}

// GetOverlappingRangeStore returns a clusterstore interface
func (i *KubernetesIPAM) GetOverlappingRangeStore() (storage.OverlappingRangeStore, error) {
	return &KubernetesOverlappingRangeStore{i.client, i.containerID, i.namespace}, nil
}

// IsAllocatedInOverlappingRange checks for IP addresses to see if they're allocated cluster wide, for overlapping ranges
func (c *KubernetesOverlappingRangeStore) IsAllocatedInOverlappingRange(ctx context.Context, ip net.IP) (bool, error) {

	// IPv6 doesn't make for valid CR names, so normalize it.
	normalizedip := strings.ReplaceAll(fmt.Sprint(ip), ":", "-")

	logging.Debugf("OverlappingRangewide allocation check for IP: %v", normalizedip)

	// clusteripres := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
	// 	ObjectMeta: metav1.ObjectMeta{Name: normalizedip, Namespace: i.namespace},
	// }
	clusteripres := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: normalizedip, Namespace: c.namespace},
	}
	if err := c.client.Get(ctx, types.NamespacedName{Name: normalizedip, Namespace: c.namespace}, clusteripres); errors.IsNotFound(err) {
		// cluster ip reservation does not exist, this appears to be good news.
		// logging.Debugf("IP %v is not reserved cluster wide, allowing.", ip)
		return false, nil
	} else if err != nil {
		logging.Errorf("k8s get OverlappingRangeIPReservation error: %s", err)
		return false, fmt.Errorf("k8s get OverlappingRangeIPReservation error: %s", err)
	}

	logging.Debugf("IP %v is reserved cluster wide.", ip)
	return true, nil
}

// UpdateOverlappingRangeAllocation updates clusterwide allocation for overlapping ranges.
func (c *KubernetesOverlappingRangeStore) UpdateOverlappingRangeAllocation(ctx context.Context, mode int, ip net.IP, containerID string, podRef string) error {
	// Normalize the IP
	normalizedip := strings.ReplaceAll(fmt.Sprint(ip), ":", "-")

	clusteripres := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
		ObjectMeta: metav1.ObjectMeta{Name: normalizedip, Namespace: c.namespace},
	}

	var err error
	var verb string
	switch mode {
	case whereaboutstypes.Allocate:
		// Put together our cluster ip reservation
		verb = "allocate"

		clusteripres.Spec = whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
			ContainerID: containerID,
			PodRef:      podRef,
		}

		err = c.client.Create(ctx, clusteripres)

	case whereaboutstypes.Deallocate:
		verb = "deallocate"
		err = c.client.Delete(ctx, clusteripres)
	}

	if err != nil {
		return err
	}

	logging.Debugf("K8s UpdateOverlappingRangeAllocation success on %v: %+v", verb, clusteripres)
	return nil
}

// KubernetesIPPool represents an IPPool resource and its parsed set of allocations
type KubernetesIPPool struct {
	client      client.Client
	containerID string
	firstIP     net.IP
	pool        *whereaboutsv1alpha1.IPPool
}

// Allocations returns the initially retrieved set of allocations for this pool
func (p *KubernetesIPPool) Allocations() []whereaboutstypes.IPReservation {
	return toIPReservationList(p.pool.Spec.Allocations, p.firstIP)
}

// Update sets the pool allocated IP list to the given IP reservations
func (p *KubernetesIPPool) Update(ctx context.Context, reservations []whereaboutstypes.IPReservation) error {
	// marshal the current pool to serve as the base for the patch creation
	orig := p.pool.DeepCopy()
	origBytes, err := json.Marshal(orig)
	if err != nil {
		return err
	}

	// update the pool before marshalling once again
	p.pool.Spec.Allocations = toAllocationMap(reservations, p.firstIP)
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
		{Operation: "test", Path: "/metadata/resourceVersion", Value: orig.ObjectMeta.ResourceVersion},
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
	err = p.client.Patch(ctx, orig, client.RawPatch(types.JSONPatchType, patchData))
	if err != nil {
		if errors.IsInvalid(err) {
			// expect "invalid" errors if any of the jsonpatch "test" Operations fail
			return &temporaryError{err}
		}
		return err
	}

	return nil
}

// newLeaderElector creates a new leaderelection.LeaderElector and associated
// channels by which to observe elections and depositions.
func newLeaderElector(clientset *kubernetes.Clientset, namespace string, podNamespace string, podID string, leaseDuration int, renewDeadline int, retryPeriod int) (*leaderelection.LeaderElector, chan struct{}, chan struct{}) {
	//log.WithField("context", "leaderelection")
	// leaderOK will block gRPC startup until it's closed.
	leaderOK := make(chan struct{})
	// deposed is closed by the leader election callback when
	// we are deposed as leader so that we can clean up.
	deposed := make(chan struct{})

	var rl = &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "whereabouts",
			Namespace: namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: fmt.Sprintf("%s/%s", podNamespace, podID),
		},
	}

	// Make the leader elector, ready to be used in the Workgroup.
	// !bang
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: time.Duration(leaseDuration) * time.Millisecond,
		RenewDeadline: time.Duration(renewDeadline) * time.Millisecond,
		RetryPeriod:   time.Duration(retryPeriod) * time.Millisecond,
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
		logging.Errorf("Failed to create leader elector: %v", err)
		return nil, leaderOK, deposed
	}
	return le, leaderOK, deposed
}

// IPManagement manages ip allocation and deallocation from a storage perspective
func IPManagement(mode int, ipamConf whereaboutstypes.IPAMConfig, containerID string, podRef string) (net.IPNet, error) {
	var newip net.IPNet

	ipam, err := NewKubernetesIPAM(containerID, ipamConf)
	if err != nil {
		return newip, logging.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
	}
	defer ipam.Close()

	if ipamConf.PodName == "" {
		return newip, fmt.Errorf("IPAM %s client initialization error: no pod name", ipamConf.Datastore)
	}

	// setup leader election
	le, leader, deposed := newLeaderElector(ipam.clientSet, ipam.namespace, ipamConf.PodNamespace, ipamConf.PodName, ipamConf.LeaderLeaseDuration, ipamConf.LeaderRenewDeadline, ipamConf.LeaderRetryPeriod)
	var wg sync.WaitGroup
	wg.Add(2)

	stopM := make(chan struct{})
	result := make(chan error, 2)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopM:
				logging.Debugf("Stopped leader election")
				result <- nil
				return
			case <-leader:
				logging.Debugf("Elected as leader, do processing")
				newip, err = IPManagementKubernetesUpdate(mode, ipam, ipamConf, containerID, podRef)
				stopM <- struct{}{}
				return
			case <-deposed:
				logging.Debugf("Deposed as leader, shutting down")
				result <- nil
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error)

		go func() {
			logging.Debugf("Started leader election")
			le.Run(ctx)
			logging.Debugf("Finished leader election")
			res <- nil
		}()

		// wait for stop
		<-stopM
		// cancel fn(ctx)
		cancel()
		result <- (<-res)
	}()

	wg.Wait()
	close(stopM)
	logging.Debugf("IPManagement: %v, %v", newip, err)
	return newip, err
}

// IPManagementKubernetesUpdate manages k8s updates
func IPManagementKubernetesUpdate(mode int, ipam *KubernetesIPAM, ipamConf whereaboutstypes.IPAMConfig, containerID string, podRef string) (net.IPNet, error) {
	logging.Debugf("IPManagement -- mode: %v / containerID: %v / podRef: %v", mode, containerID, podRef)

	var newip net.IPNet
	// Skip invalid modes
	switch mode {
	case whereaboutstypes.Allocate, whereaboutstypes.Deallocate:
	default:
		return newip, fmt.Errorf("Got an unknown mode passed to IPManagement: %v", mode)
	}

	var overlappingrangestore storage.OverlappingRangeStore
	var pool storage.IPPool
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), storage.RequestTimeout)
	defer cancel()

	// Check our connectivity first
	if err := ipam.Status(ctx); err != nil {
		logging.Errorf("IPAM connectivity error: %v", err)
		return newip, err
	}

	// handle the ip add/del until successful
	var overlappingrangeallocations []whereaboutstypes.IPReservation
	var ipforoverlappingrangeupdate net.IP
RETRYLOOP:
	for j := 0; j < storage.DatastoreRetries; j++ {
		select {
		case <-ctx.Done():
			return newip, err
		default:
			// retry the IPAM loop if the context has not been cancelled
		}

		overlappingrangestore, err = ipam.GetOverlappingRangeStore()
		if err != nil {
			logging.Errorf("IPAM error getting OverlappingRangeStore: %v", err)
			return newip, err
		}

		pool, err = ipam.GetIPPool(ctx, ipamConf.Range)
		if err != nil {
			logging.Errorf("IPAM error reading pool allocations (attempt: %d): %v", j, err)
			if e, ok := err.(storage.Temporary); ok && e.Temporary() {
				continue
			}
			return newip, err
		}

		reservelist := pool.Allocations()
		reservelist = append(reservelist, overlappingrangeallocations...)
		var updatedreservelist []whereaboutstypes.IPReservation
		switch mode {
		case whereaboutstypes.Allocate:
			newip, updatedreservelist, err = allocate.AssignIP(ipamConf, reservelist, containerID, podRef)
			if err != nil {
				logging.Errorf("Error assigning IP: %v", err)
				return newip, err
			}
			// Now check if this is allocated overlappingrange wide
			// When it's allocated overlappingrange wide, we add it to a local reserved list
			// And we try again.
			if ipamConf.OverlappingRanges {
				isallocated, err := overlappingrangestore.IsAllocatedInOverlappingRange(ctx, newip.IP)
				if err != nil {
					logging.Errorf("Error checking overlappingrange allocation: %v", err)
					return newip, err
				}

				if isallocated {
					logging.Debugf("Continuing loop, IP is already allocated (possibly from another range): %v", newip)
					// We create "dummy" records here for evaluation, but, we need to filter those out later.
					overlappingrangeallocations = append(overlappingrangeallocations, whereaboutstypes.IPReservation{IP: newip.IP, IsAllocated: true})
					continue
				}

				ipforoverlappingrangeupdate = newip.IP
			}

		case whereaboutstypes.Deallocate:
			updatedreservelist, ipforoverlappingrangeupdate, err = allocate.DeallocateIP(reservelist, containerID)
			if err != nil {
				logging.Errorf("Error deallocating IP: %v", err)
				return newip, err
			}
		}

		// Clean out any dummy records from the reservelist...
		var usereservelist []whereaboutstypes.IPReservation
		for _, rl := range updatedreservelist {
			if rl.IsAllocated != true {
				usereservelist = append(usereservelist, rl)
			}
		}

		err = pool.Update(ctx, usereservelist)
		if err != nil {
			logging.Errorf("IPAM error updating pool (attempt: %d): %v", j, err)
			if e, ok := err.(storage.Temporary); ok && e.Temporary() {
				continue
			}
			break RETRYLOOP
		}
		break RETRYLOOP
	}

	if ipamConf.OverlappingRanges {
		err = overlappingrangestore.UpdateOverlappingRangeAllocation(ctx, mode, ipforoverlappingrangeupdate, containerID, podRef)
		if err != nil {
			logging.Errorf("Error performing UpdateOverlappingRangeAllocation: %v", err)
			return newip, err
		}
	}

	return newip, err
}
