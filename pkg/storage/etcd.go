package storage

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/coreos/etcd/pkg/transport"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
)

const whereaboutsPrefix = "/whereabouts"

var (
	// DialTimeout defines how long we dial etcd
	DialTimeout = 2 * time.Second
)

// NewETCDIPAM returns a new IPAM Client configured to an etcd backend
func NewETCDIPAM(ipamConf types.IPAMConfig) (*ETCDIPAM, error) {
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

	// acquire our lock
	if err := mutex.Lock(context.Background()); err != nil {
		return nil, err
	}

	return &ETCDIPAM{client, clientv3.NewKV(client), mutex, session}, nil
}

// ETCDIPAM manages ip blocks in an etcd backend
type ETCDIPAM struct {
	client *clientv3.Client
	kv     clientv3.KV

	mutex   *concurrency.Mutex
	session *concurrency.Session
}

// Status tests connectivity to the etcd backend
func (i *ETCDIPAM) Status(ctx context.Context) error {
	_, err := i.kv.Get(ctx, "anykey")
	return err
}

// EtcdOverlappingRangeStore represents a set of cluster wide resources
type EtcdOverlappingRangeStore struct {
	client *clientv3.Client
}

// GetOverlappingRangeStore returns an OverlappingRangeStore interface
func (i *ETCDIPAM) GetOverlappingRangeStore() (OverlappingRangeStore, error) {
	return &EtcdOverlappingRangeStore{i.client}, nil
}

// IsAllocatedInOverlappingRange checks to see if the IP is allocated across the whole cluster (and not just the current range)
func (i *EtcdOverlappingRangeStore) IsAllocatedInOverlappingRange(ctx context.Context, ip net.IP) (bool, error) {
	logging.Debugf("ETCD IsAllocatedInOverlappingRange is NOT IMPLEMENTED!!!! TODO")
	return false, nil
}

// UpdateOverlappingRangeAllocation updates our clusterwide allocation for overlapping ranges.
func (i *EtcdOverlappingRangeStore) UpdateOverlappingRangeAllocation(ctx context.Context, mode int, ip net.IP, containerID string) error {
	logging.Debugf("ETCD UpdateOverlappingRangeWide is NOT IMPLEMENTED!!!! TODO")
	return nil
}

// Close shuts down the clients etcd connections
func (i *ETCDIPAM) Close() error {
	defer i.client.Close()
	defer i.session.Close()
	return i.mutex.Unlock(context.Background())
}

// GetIPPool returns a storage.IPPool for the given range
func (i *ETCDIPAM) GetIPPool(ctx context.Context, ipRange string) (IPPool, error) {
	reservelist, err := i.getRange(ctx, ipRange)
	if err != nil {
		return nil, err
	}
	return &ETCDIPPool{i.kv, ipRange, reservelist}, nil
}

// GetRange gets the reserved list of IPs for a range
func (i *ETCDIPAM) getRange(ctx context.Context, iprange string) ([]types.IPReservation, error) {
	reservelist, err := i.kv.Get(ctx, fmt.Sprintf("%s/%s", whereaboutsPrefix, iprange))
	if err != nil {
		return nil, err
	}
	if len(reservelist.Kvs) == 0 {
		return nil, nil
	}

	reservations := strings.Split(string(reservelist.Kvs[0].Value), "\n")
	list := make([]types.IPReservation, len(reservations))
	for i, raw := range reservations {
		split := strings.SplitN(raw, " ", 2)
		if len(split) != 2 {
			return nil, nil
		}
		list[i] = types.IPReservation{
			IP:          net.ParseIP(split[0]),
			ContainerID: split[1],
		}
	}

	return list, nil
}

// ETCDIPPool represents a range and its parsed set of allocations
type ETCDIPPool struct {
	kv          clientv3.KV
	ipRange     string
	allocations []types.IPReservation
}

// Allocations returns the initially retrieved set of allocations for this pool
func (p *ETCDIPPool) Allocations() []types.IPReservation {
	return p.allocations
}

// Update sets the pool allocated IP list to the given IP reservations
func (p *ETCDIPPool) Update(ctx context.Context, reservations []types.IPReservation) error {
	var raw []string
	for _, r := range reservations {
		raw = append(raw, fmt.Sprintf("%s %s", r.IP.String(), r.ContainerID))
	}
	_, err := p.kv.Put(ctx, fmt.Sprintf("%s/%s", whereaboutsPrefix, p.ipRange), strings.Join(raw, "\n"))
	return err
}
