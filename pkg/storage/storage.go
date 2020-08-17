package storage

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
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

// OverlappingRangeStore is an interface for wrapping overlappingrange storage options
type OverlappingRangeStore interface {
	IsAllocatedInOverlappingRange(ctx context.Context, ip net.IP) (bool, error)
	UpdateOverlappingRangeAllocation(ctx context.Context, mode int, ip net.IP, containerID string) error
}

// IPManagement manages ip allocation and deallocation from a storage perspective
func IPManagement(mode int, ipamConf types.IPAMConfig, containerID string) (net.IPNet, error) {

	logging.Debugf("IPManagement -- mode: %v / host: %v / containerID: %v", mode, ipamConf.EtcdHost, containerID)

	var newip net.IPNet
	// Skip invalid modes
	switch mode {
	case types.Allocate, types.Deallocate:
	default:
		return newip, fmt.Errorf("Got an unknown mode passed to IPManagement: %v", mode)
	}

	var ipam Store
	var overlappingrangestore OverlappingRangeStore
	var pool IPPool
	var err error
	switch ipamConf.Datastore {
	case types.DatastoreETCD:
		ipam, err = NewETCDIPAM(ipamConf)
	case types.DatastoreKubernetes:
		ipam, err = NewKubernetesIPAM(containerID, ipamConf)
	}
	if err != nil {
		logging.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
		return newip, fmt.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
	}
	defer ipam.Close()

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	// Check our connectivity first
	if err := ipam.Status(ctx); err != nil {
		logging.Errorf("IPAM connectivity error: %v", err)
		return newip, err
	}

	// handle the ip add/del until successful
	var overlappingrangeallocations []types.IPReservation
	var ipforoverlappingrangeupdate net.IP
RETRYLOOP:
	for j := 0; j < DatastoreRetries; j++ {
		select {
		case <-ctx.Done():
			return newip, nil
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
			if e, ok := err.(temporary); ok && e.Temporary() {
				continue
			}
			return newip, err
		}

		reservelist := pool.Allocations()
		reservelist = append(reservelist, overlappingrangeallocations...)
		var updatedreservelist []types.IPReservation
		switch mode {
		case types.Allocate:
			newip, updatedreservelist, err = allocate.AssignIP(ipamConf, reservelist, containerID)
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
					overlappingrangeallocations = append(overlappingrangeallocations, types.IPReservation{IP: newip.IP, IsAllocated: true})
					continue
				}

				ipforoverlappingrangeupdate = newip.IP
			}

		case types.Deallocate:
			updatedreservelist, ipforoverlappingrangeupdate, err = allocate.DeallocateIP(ipamConf, ipamConf.Range, reservelist, containerID)
			if err != nil {
				logging.Errorf("Error deallocating IP: %v", err)
				return newip, err
			}
		}

		// Clean out any dummy records from the reservelist...
		var usereservelist []types.IPReservation
		for _, rl := range updatedreservelist {
			if rl.IsAllocated != true {
				usereservelist = append(usereservelist, rl)
			}
		}

		err = pool.Update(ctx, usereservelist)
		if err != nil {
			logging.Errorf("IPAM error updating pool (attempt: %d): %v", j, err)
			if e, ok := err.(temporary); ok && e.Temporary() {
				continue
			}
			break RETRYLOOP
		}
		break RETRYLOOP
	}

	if ipamConf.OverlappingRanges {
		err = overlappingrangestore.UpdateOverlappingRangeAllocation(ctx, mode, ipforoverlappingrangeupdate, containerID)
		if err != nil {
			logging.Errorf("Error performing UpdateOverlappingRangeAllocation: %v", err)
			return newip, err
		}
	}

	return newip, err
}
