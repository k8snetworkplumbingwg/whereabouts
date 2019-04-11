package types

import (
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"net"
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
	Name       string
	Type       string            `json:"type"`
	Routes     []*cnitypes.Route `json:"routes"`
	Addresses  []Address         `json:"addresses,omitempty"`
	DNS        cnitypes.DNS      `json:"dns"`
	Range      string            `json:"range"`
	GatewayStr string            `json:"gateway"`
	EtcdHost   string            `json:"etcd_host"`
	LogFile    string            `json:"log_file"`
	LogLevel   string            `json:"log_level"`
	Gateway    net.IP
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
)
