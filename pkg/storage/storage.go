package storage

import (
	"context"
	"fmt"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/clientv3/concurrency"
	"time"
)

const whereaboutLockID = "/whereabouts"

var (
	// DialTimeout defines how long we dial etcd
	DialTimeout = 2 * time.Second
	// RequestTimeout defines how long the context timesout in
	RequestTimeout = 10 * time.Second
)

// TestGetValue is a freakin' test.
func TestGetValue(etcdhost string) error {

	logging.Errorf("ETCD host: %v", etcdhost)
	ctx, _ := context.WithTimeout(context.Background(), RequestTimeout)
	cli, _ := clientv3.New(clientv3.Config{
		DialTimeout: DialTimeout,
		Endpoints:   []string{etcdhost},
	})
	defer cli.Close()
	kv := clientv3.NewKV(cli)

	logging.Errorf("cli type: %T", cli)

	err := ModifiedMutexLock(ctx, cli)
	if err != nil {
		logging.Errorf("ETCD error: %v", err)
		return err
	}

	err = GetSingleValueDemo(ctx, kv)
	if err != nil {
		logging.Errorf("ETCD error: %v", err)
		return err
	}

	// err = GetLockDemo(ctx, kv)
	// if err != nil {
	// 	logging.Errorf("ETCD lock error: %v", err)
	// 	return err
	// }

	return nil

}

// ModifiedMutexLock only needs one for my purposes.
func ModifiedMutexLock(ctx context.Context, cli *clientv3.Client) error {
	// cli, err := clientv3.New(clientv3.Config{
	// 	DialTimeout: DialTimeout,
	// 	Endpoints:   []string{etcdhost},
	// })

	// create two separate sessions for lock competition
	s1, err := concurrency.NewSession(cli)
	if err != nil {
		return err
	}
	defer s1.Close()

	m1 := concurrency.NewMutex(s1, whereaboutLockID)
	s2, err := concurrency.NewSession(cli)
	if err != nil {
		return err
	}
	defer s2.Close()

	m2 := concurrency.NewMutex(s2, whereaboutLockID)
	// acquire lock for s1
	if err := m1.Lock(ctx); err != nil {
		return err
	}

	logging.Errorf("acquired lock for s1")
	m2Locked := make(chan struct{})
	go func() {
		defer close(m2Locked)
		// wait until s1 is locks /my-lock/
		if err := m2.Lock(ctx); err != nil {
			logging.Errorf("err in m2 lock: %v", err)
		}
	}()
	if err := m1.Unlock(ctx); err != nil {
		return err
	}
	logging.Errorf("released lock for s1")
	<-m2Locked
	logging.Errorf("acquired lock for s2")
	// Output:
	// acquired lock for s1
	// released lock for s1
	// acquired lock for s2

	return nil
}

// ExampleMutexLock from: https://chromium.googlesource.com/external/github.com/coreos/etcd/+/refs/heads/master/clientv3/concurrency/example_mutex_test.go
func ExampleMutexLock(etcdhost string) error {
	cli, err := clientv3.New(clientv3.Config{
		DialTimeout: DialTimeout,
		Endpoints:   []string{etcdhost},
	})

	if err != nil {
		return err
	}
	defer cli.Close()
	// create two separate sessions for lock competition
	s1, err := concurrency.NewSession(cli)
	if err != nil {
		return err
	}
	defer s1.Close()
	m1 := concurrency.NewMutex(s1, whereaboutLockID)
	s2, err := concurrency.NewSession(cli)
	if err != nil {
		return err
	}
	defer s2.Close()
	m2 := concurrency.NewMutex(s2, whereaboutLockID)
	// acquire lock for s1
	if err := m1.Lock(context.TODO()); err != nil {
		return err
	}
	logging.Errorf("acquired lock for s1")
	m2Locked := make(chan struct{})
	go func() {
		defer close(m2Locked)
		// wait until s1 is locks /my-lock/
		if err := m2.Lock(context.TODO()); err != nil {
			logging.Errorf("err in m2 lock: %v", err)
		}
	}()
	if err := m1.Unlock(context.TODO()); err != nil {
		return err
	}
	logging.Errorf("released lock for s1")
	<-m2Locked
	logging.Errorf("acquired lock for s2")
	// Output:
	// acquired lock for s1
	// released lock for s1
	// acquired lock for s2

	return nil
}

// GetLockDemo try a lock?
func GetLockDemo(ctx context.Context, kv clientv3.KV) error {
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

// SetRequestTimeout sets the timeout for testing purposes
func SetRequestTimeout(newtime time.Duration) {
	RequestTimeout = newtime
}
