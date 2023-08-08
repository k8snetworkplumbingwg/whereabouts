package storage

import (
	"context"
	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net"
	"time"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

var (
	// RequestTimeout defines how long the context timesout in
	RequestTimeout = 10 * time.Second

	// DatastoreRetries defines how many retries are attempted when updating the Pool
	DatastoreRetries = 100
)

// IPPool is the interface that represents an manageable pool of allocated IPs
type IPPool interface {
	Allocations() []types.IPReservation
	Update(ctx context.Context, reservations []types.IPReservation) error
}

// Store is the interface that wraps the basic IP Allocation methods on the underlying storage backend
type Store interface {
	GetIPPool(ctx context.Context, ipRange string) (IPPool, error)
	GetOverlappingRangeStore() (OverlappingRangeStore, error)
	Status(ctx context.Context) error
	Close() error
}

type OverlappingRangeHandler func() error

// OverlappingRangeStore is an interface for wrapping overlappingrange storage options
type OverlappingRangeStore interface {
	IsAllocatedInOverlappingRange(ctx context.Context, ip net.IP, networkName string) (bool, error)
	RetrievePreviousAllocation(ctx context.Context, ownerRef string, networkName string) (*whereaboutsv1alpha1.OverlappingRangeIPReservation, error)
	UpdateOverlappingRangeAllocation(
		ctx context.Context,
		mode int,
		ip net.IP,
		containerID, podRef, networkName string,
		ownerRef *metav1.OwnerReference,
		existingAllocation *whereaboutsv1alpha1.OverlappingRangeIPReservation,
	) error
}

type Temporary interface {
	Temporary() bool
}
