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
	Status(ctx context.Context) error
	Close() error
}

// IPManagement manages ip allocation and deallocation from a storage perspective
func IPManagement(mode int, ipamConf types.IPAMConfig, containerID string) (net.IPNet, net.IP, error) {

	logging.Debugf("IPManagement -- mode: %v / host: %v / containerID: %v", mode, ipamConf.EtcdHost, containerID)

	var newip net.IPNet
	var gateway net.IP
	// Skip invalid modes
	switch mode {
	case types.Allocate, types.Deallocate:
	default:
		return newip, gateway, fmt.Errorf("Got an unknown mode passed to IPManagement: %v", mode)
	}

	var ipam Store
	var err error
	switch ipamConf.Datastore {
	case types.DatastoreETCD:
		ipam, err = NewETCDIPAM(ipamConf)
	case types.DatastoreKubernetes:
		ipam, err = NewKubernetesIPAM(containerID, ipamConf)
	}
	if err != nil {
		logging.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
		return newip, gateway, fmt.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
	}
	defer ipam.Close()

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	// Check our connectivity first
	if err := ipam.Status(ctx); err != nil {
		logging.Errorf("IPAM connectivity error: %v", err)
		return newip, gateway, err
	}

	for _, ipRange := range ipamConf.Ranges {

		logging.Debugf("try ipRange:%+v", ipRange)
		newip, err = IPManagementRange(ctx, mode, ipRange, ipam, containerID)
		logging.Debugf("result allocate in range %+v IP:%+v err:%v", ipRange, newip, err)
		if err == nil {
			gateway = ipRange.Gateway
			break

		} else {
			switch mode {
			case types.Allocate:
				if _, ok := err.(allocate.AssignmentError); ok {
					logging.Debugf("range not have free IP, try next")
					continue
				}
			case types.Deallocate:
				if _, ok := err.(allocate.DeallocateError); ok {
					logging.Debugf("range not have container, try next")
					continue
				}
			}
			return newip, gateway, err
		}
	}

	return newip, gateway, err
}

func IPManagementRange(ctx context.Context, mode int, ipRange types.IPRange, ipam Store, containerID string) (net.IPNet, error) {
	var newip net.IPNet
	var pool IPPool
	var err error

	// handle the ip add/del until successful
RETRYLOOP:
	for j := 0; j < DatastoreRetries; j++ {
		select {
		case <-ctx.Done():
			return newip, nil
		default:
			// retry the IPAM loop if the context has not been cancelled
		}

		pool, err = ipam.GetIPPool(ctx, ipRange.Range)
		if err != nil {
			logging.Errorf("IPAM error reading pool allocations (attempt: %d): %v", j, err)
			if e, ok := err.(temporary); ok && e.Temporary() {
				continue
			}
			return newip, err
		}

		reservelist := pool.Allocations()
		var updatedreservelist []types.IPReservation
		switch mode {
		case types.Allocate:
			newip, updatedreservelist, err = allocate.AssignIP(ipRange, reservelist, containerID)
			if err != nil {
				logging.Errorf("Error assigning IP: %v", err)
				return newip, err
			}
		case types.Deallocate:
			updatedreservelist, err = allocate.DeallocateIP(reservelist, containerID)
			if err != nil {
				logging.Errorf("Error deallocating IP: %v", err)
				return newip, err
			}
		}

		err = pool.Update(ctx, updatedreservelist)
		if err != nil {
			logging.Errorf("IPAM error updating pool (attempt: %d): %v", j, err)
			if e, ok := err.(temporary); ok && e.Temporary() {
				continue
			}
			break RETRYLOOP
		}
		break RETRYLOOP
	}

	return newip, err
}
