package types

import (
	"net"

	cnitypes "github.com/containernetworking/cni/pkg/types"
)

// Net is The top-level network config - IPAM plugins are passed the full configuration
// of the calling plugin, not just the IPAM section.
type Net struct {
	Name       string      `json:"name"`
	CNIVersion string      `json:"cniVersion"`
	IPAM       *IPAMConfig `json:"ipam"`
}

// IPAMConfig describes the expected json configuration for this plugin
type IPAMConfig struct {
	Name           string
	Type           string            `json:"type"`
	Routes         []*cnitypes.Route `json:"routes"`
	Addresses      []Address         `json:"addresses,omitempty"`
	OmitRanges     []string          `json:"exclude,omitempty"`
	DNS            cnitypes.DNS      `json:"dns"`
	Range          string            `json:"range"`
	GatewayStr     string            `json:"gateway"`
	EtcdHost       string            `json:"etcd_host,omitempty"`
	EtcdUsername   string            `json:"etcd_username,omitempty"`
	EtcdPassword   string            `json:"etcd_password,omitempty"`
	EtcdKeyFile    string            `json:"etcd_key_file,omitempty"`
	EtcdCertFile   string            `json:"etcd_cert_file,omitempty"`
	EtcdCACertFile string            `json:"etcd_ca_cert_file,omitempty"`
	LogFile        string            `json:"log_file"`
	LogLevel       string            `json:"log_level"`
	Gateway        net.IP
}

// IPAMEnvArgs are the environment vars we expect
type IPAMEnvArgs struct {
	cnitypes.CommonArgs
	IP      cnitypes.UnmarshallableString `json:"ip,omitempty"`
	GATEWAY cnitypes.UnmarshallableString `json:"gateway,omitempty"`
}

// Address is our standard address.
type Address struct {
	AddressStr string `json:"address"`
	Gateway    net.IP `json:"gateway,omitempty"`
	Address    net.IPNet
	Version    string
}

const (
	// Allocate operation identifier
	Allocate = 0
	// Deallocate operation identifier
	Deallocate = 1
	// SkipOperation is used for a kind of noop when an error is encountered
	SkipOperation = 1000000
)
