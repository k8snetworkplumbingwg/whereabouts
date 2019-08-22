package etcd

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/coreos/etcd/pkg/transport"
	"github.com/dougbtv/whereabouts/pkg/types"
)

const whereaboutsPrefix = "/whereabouts"

var (
	// DialTimeout defines how long we dial etcd
	DialTimeout = 2 * time.Second
)

// New returns a new IPAM Client configured to an etcd backend
func New(ipamConf types.IPAMConfig) (*IPAM, error) {
	cfg := clientv3.Config{
		DialTimeout: DialTimeout,
		Endpoints:   []string{ipamConf.EtcdHost},
		Username:    ipamConf.EtcdUsername,
		Password:    ipamConf.EtcdPassword,
	}
	if cert, key := ipamConf.EtcdCertFile, ipamConf.EtcdKeyFile; cert != "" && key != "" {
		tlsInfo := transport.TLSInfo{
			CertFile:      cert,
			KeyFile:       key,
			TrustedCAFile: ipamConf.EtcdCACertFile,
		}
		tlsConfig, err := tlsInfo.ClientConfig()
		if err != nil {
			return nil, err
		}
		cfg.TLS = tlsConfig
	}
	client, err := clientv3.New(cfg)
	if err != nil {
		return nil, err
	}

	session, err := concurrency.NewSession(client)
	if err != nil {
		return nil, err
	}
	mutex := concurrency.NewMutex(session, fmt.Sprintf("%s/%s", whereaboutsPrefix, ipamConf.Range))
	return &IPAM{client, clientv3.NewKV(client), mutex, session}, nil
}

// IPAM manages ip blocks in an etcd backend
type IPAM struct {
	client *clientv3.Client
	kv     clientv3.KV

	mutex   *concurrency.Mutex
	session *concurrency.Session
}

// GetRange gets the reserved list of IPs for a range
func (i *IPAM) GetRange(ctx context.Context, iprange string) (string, error) {
	reservelist, err := i.kv.Get(ctx, "/"+iprange)
	if err != nil {
		return "", err
	}
	returnstring := ""
	if len(reservelist.Kvs) >= 1 {
		returnstring = string(reservelist.Kvs[0].Value)
	}
	return returnstring, nil
}

// UpdateRange writes a new reserved list after assigning an IP within a range
func (i *IPAM) UpdateRange(ctx context.Context, iprange string, reservelist string) error {
	_, err := i.kv.Put(ctx, "/"+iprange, reservelist)
	return err
}

// Lock locks the IPAM backend
func (i *IPAM) Lock(ctx context.Context) error {
	return i.mutex.Lock(ctx)
}

// Unlock unlocks the IPAM backend
func (i *IPAM) Unlock(ctx context.Context) error {
	return i.mutex.Unlock(ctx)
}

// Status tests connectivity to the etcd backend
func (i *IPAM) Status(ctx context.Context) error {
	_, err := i.kv.Get(ctx, "anykey")
	return err
}

// Close shuts down the clients etcd connections
func (i *IPAM) Close() error {
	defer i.session.Close()
	return i.client.Close()
}