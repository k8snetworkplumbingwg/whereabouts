package storage

import (
	"context"
	"fmt"
	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/clientv3/concurrency"
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

	logging.Errorf("ETCD mode: %v / host: %v / containerID: %v", mode, ipamConf.EtcdHost, containerID)
	ctx, _ := context.WithTimeout(context.Background(), RequestTimeout)
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

	// acquire our lock
	if err := m1.Lock(ctx); err != nil {
		return net.IPNet{}, err
	}

	logging.Errorf("acquired lock for our session...")
	newip, err := allocate.AssignIP(ipamConf.Range)

	err = GetSingleValueDemo(ctx, kv)
	if err != nil {
		logging.Errorf("ETCD error: %v", err)
		return net.IPNet{}, err
	}

	// Unlock our session...
	if err := m1.Unlock(ctx); err != nil {
		return net.IPNet{}, err
	}
	logging.Errorf("released lock for our session")

	return newip, nil

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

	logging.Errorf("acquired lock for our session...")

	// Unlock our session...
	if err := m1.Unlock(ctx); err != nil {
		return err
	}
	logging.Errorf("released lock for our session")

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

// GetSingleValueDemo is just for a test.
func GetSingleValueDemo(ctx context.Context, kv clientv3.KV) error {
	logging.Errorf("*** GetSingleValueDemo()")
	// Delete all keys
	kv.Delete(ctx, "key", clientv3.WithPrefix())

	// Insert a key value
	pr, err := kv.Put(ctx, "key", "444")
	if err != nil {
		return fmt.Errorf("ETCD put error: %v", err)
	}

	rev := pr.Header.Revision

	logging.Errorf("Revision: %v", rev)

	gr, err := kv.Get(ctx, "key")
	if err != nil {
		return fmt.Errorf("ETCD get error: %v", err)
	}

	logging.Errorf("Value: %v / Revision: %v", string(gr.Kvs[0].Value), gr.Header.Revision)

	// Modify the value of an existing key (create new revision)
	kv.Put(ctx, "key", "555")

	gr, _ = kv.Get(ctx, "key")
	logging.Errorf("Value: %v / Revision: %v", string(gr.Kvs[0].Value), gr.Header.Revision)

	// Get the value of the previous revision
	gr, _ = kv.Get(ctx, "key", clientv3.WithRev(rev))
	logging.Errorf("Value: %v / Revision: %v", string(gr.Kvs[0].Value), gr.Header.Revision)

	return nil
}

// SetTimeouts sets the timeout for testing purposes
func SetTimeouts(newtime time.Duration) {
	DialTimeout = newtime
	RequestTimeout = newtime
}
