package storage

import (
	"context"
	"fmt"
	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
	"net"
	"time"
)

const whereaboutsPrefix = "/whereabouts"

var (
	// DialTimeout defines how long we dial etcd
	DialTimeout = 2 * time.Second
	// RequestTimeout defines how long the context timesout in
	RequestTimeout = 10 * time.Second
)

// IPManagement manages ip allocation and deallocation from a storage perspective
func IPManagement(mode int, ipamConf types.IPAMConfig, containerID string) (net.IPNet, error) {

	logging.Debugf("IPManagement -- mode: %v / host: %v / containerID: %v", mode, ipamConf.EtcdHost, containerID)
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()
	cli, _ := clientv3.New(clientv3.Config{
		DialTimeout: DialTimeout,
		Endpoints:   []string{ipamConf.EtcdHost},
	})
	defer cli.Close()
	kv := clientv3.NewKV(cli)

	// Check our connectivity first
	err := CheckConnectivity(ctx, kv)
	if err != nil {
		logging.Errorf("ETCD CheckConnectivity error: %v", err)
		return net.IPNet{}, err
	}

	// Create our session...
	session, err := concurrency.NewSession(cli)
	if err != nil {
		return net.IPNet{}, err
	}
	defer session.Close()

	// Create our mutex
	m1 := concurrency.NewMutex(session, whereaboutsPrefix)

	// ------------------------ acquire our lock
	if err := m1.Lock(ctx); err != nil {
		return net.IPNet{}, err
	}

	// ------------------------ perform everything that's exclusive.
	logging.Debugf("acquired lock for our session...")

	reservelist, err := GetReserveList(ctx, ipamConf.Range, kv)
	if err != nil {
		logging.Errorf("GetReserveList error: %v", err)
		mode = types.SkipOperation
	}

	// logging.Debugf("Updated reservelist: %v", updatedreservelist)
	var newip net.IPNet
	var updatedreservelist string

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
		err = PutReserveList(ctx, ipamConf.Range, updatedreservelist, kv)
		if err != nil {
			logging.Errorf("PutReserveList error: %v", err)
			return net.IPNet{}, err
		}

	}

	// ------------------------ Unlock our session...
	if err := m1.Unlock(ctx); err != nil {
		return net.IPNet{}, err
	}
	logging.Debugf("released lock for our session")

	// Don't throw errors until here!!
	return newip, err

}

// ModifiedMutexLock only needs one for my purposes.
// Example @ https://github.com/etcd-io/etcd/blob/master/clientv3/concurrency/example_mutex_test.go
func ModifiedMutexLock(ctx context.Context, cli *clientv3.Client) error {

	// Create our session...
	s1, err := concurrency.NewSession(cli)
	if err != nil {
		return err
	}
	defer s1.Close()

	// Create our mutex
	m1 := concurrency.NewMutex(s1, whereaboutsPrefix)

	// acquire our lock
	if err := m1.Lock(ctx); err != nil {
		return err
	}

	logging.Debugf("acquired lock for our session...")

	// Unlock our session...
	if err := m1.Unlock(ctx); err != nil {
		return err
	}
	logging.Debugf("released lock for our session")

	return nil
}

// CheckConnectivity is just for a test.
func CheckConnectivity(ctx context.Context, kv clientv3.KV) error {
	_, err := kv.Get(ctx, "anykey")
	if err != nil {
		return fmt.Errorf("ETCD get error: %v", err)
	}
	return nil
}

// PutReserveList writes a new reserved list after assigning an IP
func PutReserveList(ctx context.Context, iprange string, reservelist string, kv clientv3.KV) error {
	_, err := kv.Put(ctx, "/"+iprange, reservelist)
	if err != nil {
		return fmt.Errorf("ETCD put error: %v", err)
	}

	logging.Debugf("Updated reserve list for %v", "/"+iprange)
	return nil
}

// GetReserveList gets a reserved list of IPs for a range.
func GetReserveList(ctx context.Context, iprange string, kv clientv3.KV) (string, error) {
	logging.Debugf("Getting reserve list range: %v", iprange)
	reservelist, err := kv.Get(ctx, "/"+iprange)
	if err != nil {
		return "", fmt.Errorf("GetReserveList get error: %v", err)
	}

	returnstring := ""
	found := false
	for range reservelist.Kvs {
		found = true
		// fmt.Printf("%s : %s\n", ev.Key, ev.Value)
	}

	if found {
		returnstring = string(reservelist.Kvs[0].Value)
	}

	// logging.Debugf("Reserve list value: %+v", reservelist)
	// logging.Debugf("returnstring: %v", returnstring)

	return returnstring, nil
}

// GetSingleValueDemo is just for a test.
func GetSingleValueDemo(ctx context.Context, kv clientv3.KV) error {
	logging.Debugf("*** GetSingleValueDemo()")
	// Delete all keys
	kv.Delete(ctx, "key", clientv3.WithPrefix())

	// Insert a key value
	pr, err := kv.Put(ctx, "key", "444")
	if err != nil {
		return fmt.Errorf("ETCD put error: %v", err)
	}

	rev := pr.Header.Revision

	logging.Debugf("Revision: %v", rev)

	gr, err := kv.Get(ctx, "key")
	if err != nil {
		return fmt.Errorf("ETCD get error: %v", err)
	}

	logging.Debugf("Value: %v / Revision: %v", string(gr.Kvs[0].Value), gr.Header.Revision)

	// Modify the value of an existing key (create new revision)
	kv.Put(ctx, "key", "555")

	gr, _ = kv.Get(ctx, "key")
	logging.Debugf("Value: %v / Revision: %v", string(gr.Kvs[0].Value), gr.Header.Revision)

	// Get the value of the previous revision
	gr, _ = kv.Get(ctx, "key", clientv3.WithRev(rev))
	logging.Debugf("Value: %v / Revision: %v", string(gr.Kvs[0].Value), gr.Header.Revision)

	return nil
}

// SetTimeouts sets the timeout for testing purposes
func SetTimeouts(newtime time.Duration) {
	DialTimeout = newtime
	RequestTimeout = newtime
}
