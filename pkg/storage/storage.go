package storage

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
)

var (

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
	Close(ctx context.Context) error
}

// IPManagement manages ip allocation and deallocation from a storage perspective
func IPManagement(mode int, ipamConf types.IPAMConfig, containerID string, podRef string) (net.IPNet, error) {

	logging.Debugf("IPManagement -- mode: %v / host: %v / containerID: %v / podRef: %v", mode, ipamConf.EtcdHost, containerID, podRef)

	var newip net.IPNet
	// Skip invalid modes
	switch mode {
	case types.Allocate, types.Deallocate:
	default:
		return newip, fmt.Errorf("Got an unknown mode passed to IPManagement: %v", mode)
	}

	var ctx context.Context
	var acquireCancel context.CancelFunc
	switch mode {
	case types.Allocate:
		ctx, acquireCancel = context.WithTimeout(context.Background(), time.Duration(ipamConf.AllocateLockRequestTimeout)*time.Second)
		defer acquireCancel()
	case types.Deallocate:
		ctx, acquireCancel = context.WithTimeout(context.Background(), time.Duration(ipamConf.DeAllocateLockRequestTimeout)*time.Second)
		defer acquireCancel()
	}

	var ipam Store
	var pool IPPool
	var err error
	switch ipamConf.Datastore {
	case types.DatastoreETCD:
		ipam, err = NewETCDIPAM(ctx, ipamConf)
	case types.DatastoreKubernetes:
		ipam, err = NewKubernetesIPAM(ctx, containerID, ipamConf)
	}
	if err != nil {
		logging.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
		return newip, fmt.Errorf("IPAM %s client initialization error: %v", ipamConf.Datastore, err)
	}
	defer func() {
		var ctx context.Context
		var releaseCancel context.CancelFunc
		switch mode {
		case types.Allocate:
			ctx, releaseCancel = context.WithTimeout(context.Background(), time.Duration(ipamConf.AllocateLockRequestTimeout)*time.Second)
		case types.Deallocate:
			ctx, releaseCancel = context.WithTimeout(context.Background(), time.Duration(ipamConf.DeAllocateLockRequestTimeout)*time.Second)
		}
		err = ipam.Close(ctx)
		if err != nil {
			logging.Errorf("error in closing ipam pool %v", err)
		}
		releaseCancel()
	}()

	var ipPoolOpCancel context.CancelFunc
	switch mode {
	case types.Allocate:
		ctx, ipPoolOpCancel = context.WithTimeout(context.Background(), time.Duration(ipamConf.AllocateRequestTimeout)*time.Second)
		defer ipPoolOpCancel()
	case types.Deallocate:
		ctx, ipPoolOpCancel = context.WithTimeout(context.Background(), time.Duration(ipamConf.DeAllocateRequestTimeout)*time.Second)
		defer ipPoolOpCancel()
	}

	// Check our connectivity first
	if err := ipam.Status(ctx); err != nil {
		logging.Errorf("IPAM connectivity error: %v", err)
		return newip, err
	}

	var step int
	// handle the ip add/del until successful
RETRYLOOP:
	for j := 1; j < DatastoreRetries+1; j++ {
		select {
		case <-ctx.Done():
			// return last available newip and context.DeadlineExceeded error
			return newip, context.DeadlineExceeded
		default:
			// retry the IPAM loop if the context has not been cancelled
		}

		pool, err = ipam.GetIPPool(ctx, ipamConf.Range)
		if err != nil {
			logging.Errorf("IPAM error reading pool allocations (attempt: %d): %v", j, err)
			if e, ok := err.(temporary); ok && e.Temporary() {
				// this block might never get executed due to ip pool lock.
				interval, _ := rand.Int(rand.Reader, big.NewInt(1000))
				if strings.EqualFold(ipamConf.BackOffRetryScheme, "exponential") {
					time.Sleep(time.Duration(int(interval.Int64())*(2^j)) * time.Millisecond)
				} else {
					time.Sleep(time.Duration(int(interval.Int64())+step) * time.Millisecond)
					step += ipamConf.BackoffLinearStep
				}
				continue
			}
			return newip, err
		}

		reservelist := pool.Allocations()
		var updatedreservelist []types.IPReservation
		switch mode {
		case types.Allocate:
			newip, updatedreservelist, err = allocate.AssignIP(ipamConf, reservelist, containerID, podRef)
			if err != nil {
				logging.Errorf("Error assigning IP: %v", err)
				return newip, err
			}
		case types.Deallocate:
			updatedreservelist, err = allocate.DeallocateIP(ipamConf.Range, reservelist, containerID)
			if err != nil {
				logging.Errorf("Error deallocating IP: %v", err)
				return newip, err
			}
		}

		err = pool.Update(ctx, updatedreservelist)
		if err != nil {
			logging.Errorf("IPAM error updating pool %s (attempt: %d): %v", ipamConf.Range, j, err)
			if e, ok := err.(temporary); ok && e.Temporary() {
				logging.Errorf("IPAM error is temporary for pool %s: %v, retrying", ipamConf.Range, err)
				// this block might never get executed due to ip pool lock.
				interval, _ := rand.Int(rand.Reader, big.NewInt(1000))
				if strings.EqualFold(ipamConf.BackOffRetryScheme, "exponential") {
					time.Sleep(time.Duration(int(interval.Int64())*(2^j)) * time.Millisecond)
				} else {
					time.Sleep(time.Duration(int(interval.Int64())+step) * time.Millisecond)
					step += ipamConf.BackoffLinearStep
				}
				continue
			}
			break RETRYLOOP
		}
		break RETRYLOOP
	}

	return newip, err
}
