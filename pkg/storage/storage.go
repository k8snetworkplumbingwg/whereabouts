package storage

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/storage/etcd"
	"github.com/dougbtv/whereabouts/pkg/types"
)

var (
	// RequestTimeout defines how long the context timesout in
	RequestTimeout = 10 * time.Second
)

// IPManagement manages ip allocation and deallocation from a storage perspective
func IPManagement(mode int, ipamConf types.IPAMConfig, containerID string) (net.IPNet, error) {

	logging.Debugf("IPManagement -- mode: %v / host: %v / containerID: %v", mode, ipamConf.EtcdHost, containerID)

	ipam, err := etcd.New(ipamConf)
	if err != nil {
		logging.Errorf("IPAM client initialization error: %v", err)
		return net.IPNet{}, err
	}
	defer ipam.Close()

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	// Check our connectivity first
	err = ipam.Status(ctx)
	if err != nil {
		logging.Errorf("IPAM backend error: %v", err)
		return net.IPNet{}, err
	}

	// ------------------------ acquire our lock
	if err := ipam.Lock(ctx); err != nil {
		return net.IPNet{}, err
	}
	reservelist, err := ipam.GetRange(ctx, ipamConf.Range)
	if err != nil {
		logging.Errorf("GetReserveList error: %v", err)
		mode = types.SkipOperation
	}

	var newip net.IPNet
	var updatedreservelist []types.IPReservation

	switch mode {
	case types.Allocate:
		// Get an IP assigned
		newip, updatedreservelist, err = allocate.AssignIP(ipamConf, reservelist, containerID)
		if err != nil {
			logging.Errorf("Error assigning IP: %v", err)
		}
	case types.Deallocate:
		updatedreservelist, err = allocate.DeallocateIP(ipamConf.Range, reservelist, containerID)
		if err != nil {
			logging.Errorf("Error deallocating IP: %v", err)
		}
	case types.SkipOperation:
		// No operation.
	default:
		err = fmt.Errorf("Got an unknown mode passed to IPManagement: %v", mode)
		logging.Errorf("IPManagement mode error: %v", err)
		mode = types.SkipOperation
	}

	// Always update the reserve list unless we hit an error previously.
	if mode != types.SkipOperation {

		// Write the updated reserve list
		err = ipam.UpdateRange(ctx, ipamConf.Range, updatedreservelist)
		if err != nil {
			logging.Errorf("PutReserveList error: %v", err)
			return net.IPNet{}, err
		}

	}

	// ------------------------ Unlock our session...
	if err := ipam.Unlock(ctx); err != nil {
		return net.IPNet{}, err
	}
	logging.Debugf("released lock for our session")

	// Don't throw errors until here!!
	return newip, err

}
