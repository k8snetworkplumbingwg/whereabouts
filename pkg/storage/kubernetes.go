package storage

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	whereaboutsv1alpha1 "github.com/dougbtv/whereabouts/pkg/api/v1alpha1"
	"github.com/dougbtv/whereabouts/pkg/logging"
	whereaboutstypes "github.com/dougbtv/whereabouts/pkg/types"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// NewKubernetesIPAM returns a new KubernetesIPAM Client configured to a kubernetes CRD backend
func NewKubernetesIPAM(ctx context.Context, containerID string, ipamConf whereaboutstypes.IPAMConfig) (*KubernetesIPAM, error) {
	scheme := runtime.NewScheme()
	_ = whereaboutsv1alpha1.AddToScheme(scheme)

	overrides := &clientcmd.ConfigOverrides{}
	if apiURL := ipamConf.Kubernetes.K8sAPIRoot; apiURL != "" {
		overrides.ClusterInfo = clientcmdapi.Cluster{
			Server: apiURL,
		}
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: ipamConf.Kubernetes.KubeConfigPath},
		overrides).ClientConfig()
	if err != nil {
		return nil, err
	}

	var namespace string
	if cfg, err := clientcmd.LoadFromFile(ipamConf.Kubernetes.KubeConfigPath); err != nil {
		return nil, err
	} else if apiCtx, ok := cfg.Contexts[cfg.CurrentContext]; ok && apiCtx != nil {
		namespace = apiCtx.Namespace
	} else {
		return nil, fmt.Errorf("k8s config: namespace not present in context")
	}
	mapper, err := apiutil.NewDiscoveryRESTMapper(config)
	if err != nil {
		return nil, err
	}
	c, err := client.New(config, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, err
	}
	lockName := getNormalizedIpRangeStr(ipamConf.Range)
	poolLock := &whereaboutsv1alpha1.IPPoolLock{
		ObjectMeta: metav1.ObjectMeta{Name: lockName, Namespace: namespace},
	}
	// try creating ip pool lock, retry (if already exists) until it succeeds
	// this makes serial access to ip pool across cluster wide and this might decrease
	// the load considerably on k8 api server because of too many update calls, this can
	// happen due to retries upon status conflict errors
	for {
		err = c.Create(ctx, poolLock)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				interval, _ := rand.Int(rand.Reader, big.NewInt(1000))
				time.Sleep(time.Duration(interval.Int64()) * time.Millisecond)
				logging.Errorf("ip pool lock %s is held by someone, retry to acquire it", lockName)
				continue
			}
			logging.Errorf("ip pool lock acquire failed reason: %v ", errors.ReasonForError(err))
			return nil, err
		} else {
			logging.Errorf("acquire ip pool lock %s done", lockName)
			break
		}
	}

	return &KubernetesIPAM{c, ipamConf, containerID, namespace, DatastoreRetries}, nil
}

// KubernetesIPAM manages ip blocks in an kubernetes CRD backend
type KubernetesIPAM struct {
	client      client.Client
	config      whereaboutstypes.IPAMConfig
	containerID string
	namespace   string
	retries     int
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

func getNormalizedIpRangeStr(ipRange string) string {
	// v6 filter
	normalized := strings.ReplaceAll(ipRange, ":", "-")
	// replace subnet cidr slash
	normalized = strings.ReplaceAll(normalized, "/", "-")

	return normalized
}

// GetIPPool returns a storage.IPPool for the given range
func (i *KubernetesIPAM) GetIPPool(ctx context.Context, ipRange string) (IPPool, error) {
	pool, err := i.getPool(ctx, getNormalizedIpRangeStr(ipRange), ipRange)
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

// Close remove ip pool lock to allow other cni execution to access the pool
func (i *KubernetesIPAM) Close(ctx context.Context) error {
	lockName := getNormalizedIpRangeStr(i.config.Range)
	poolLock := &whereaboutsv1alpha1.IPPoolLock{
		ObjectMeta: metav1.ObjectMeta{Name: lockName, Namespace: i.namespace},
	}
	logging.Errorf("releasing ip pool lock %s", lockName)
	err := i.client.Delete(ctx, poolLock)
	if err != nil {
		logging.Errorf("error releasing ip pool lock %s: %v", lockName, err)
	} else {
		logging.Errorf("release ip pool lock %s done", lockName)
	}
	return err
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
	// update the pool with new ip allocations
	p.pool.Spec.Allocations = toAllocationMap(reservations, p.firstIP)
	err := p.client.Update(ctx, p.pool)
	if err != nil {
		if errors.IsConflict(err) {
			return &temporaryError{err}
		}
		return err
	}

	return nil
}

type temporaryError struct {
	error
}

func (t *temporaryError) Temporary() bool {
	return true
}

type temporary interface {
	Temporary() bool
}
