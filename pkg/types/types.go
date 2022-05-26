package types

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	cnitypes "github.com/containernetworking/cni/pkg/types"
)

// Datastore types
const (
	DatastoreETCD              = "etcd"
	DatastoreKubernetes        = "kubernetes"
	DefaultLeaderLeaseDuration = 1500
	DefaultLeaderRenewDeadline = 1000
	DefaultLeaderRetryPeriod   = 500
	AddTimeLimit               = 2 * time.Minute
	DelTimeLimit               = 1 * time.Minute
)

// Net is The top-level network config - IPAM plugins are passed the full configuration
// of the calling plugin, not just the IPAM section.
type Net struct {
	Name       string      `json:"name"`
	CNIVersion string      `json:"cniVersion"`
	IPAM       *IPAMConfig `json:"ipam"`
}

// NetConfList describes an ordered list of networks.
type NetConfList struct {
	CNIVersion string `json:"cniVersion,omitempty"`

	Name         string `json:"name,omitempty"`
	DisableCheck bool   `json:"disableCheck,omitempty"`
	Plugins      []*Net `json:"plugins,omitempty"`
}

// IPAMConfig describes the expected json configuration for this plugin
type IPAMConfig struct {
	Name                string
	Type                string            `json:"type"`
	Routes              []*cnitypes.Route `json:"routes"`
	Datastore           string            `json:"datastore"`
	Addresses           []Address         `json:"addresses,omitempty"`
	OmitRanges          []string          `json:"exclude,omitempty"`
	DNS                 cnitypes.DNS      `json:"dns"`
	Range               string            `json:"range"`
	RangeStart          net.IP            `json:"range_start,omitempty"`
	RangeEnd            net.IP            `json:"range_end,omitempty"`
	GatewayStr          string            `json:"gateway"`
	EtcdHost            string            `json:"etcd_host,omitempty"`
	EtcdUsername        string            `json:"etcd_username,omitempty"`
	EtcdPassword        string            `json:"etcd_password,omitempty"`
	EtcdKeyFile         string            `json:"etcd_key_file,omitempty"`
	EtcdCertFile        string            `json:"etcd_cert_file,omitempty"`
	EtcdCACertFile      string            `json:"etcd_ca_cert_file,omitempty"`
	LeaderLeaseDuration int               `json:"leader_lease_duration,omitempty"`
	LeaderRenewDeadline int               `json:"leader_renew_deadline,omitempty"`
	LeaderRetryPeriod   int               `json:"leader_retry_period,omitempty"`
	LogFile             string            `json:"log_file"`
	LogLevel            string            `json:"log_level"`
	OverlappingRanges   bool              `json:"enable_overlapping_ranges,omitempty"`
	SleepForRace        int               `json:"sleep_for_race,omitempty"`
	Gateway             net.IP
	Kubernetes          KubernetesConfig `json:"kubernetes,omitempty"`
	ConfigurationPath   string           `json:"configuration_path"`
	PodName             string
	PodNamespace        string
}

func (ic *IPAMConfig) UnmarshalJSON(data []byte) error {
	type IPAMConfigAlias struct {
		Name                string
		Type                string            `json:"type"`
		Routes              []*cnitypes.Route `json:"routes"`
		Datastore           string            `json:"datastore"`
		Addresses           []Address         `json:"addresses,omitempty"`
		OmitRanges          []string          `json:"exclude,omitempty"`
		DNS                 cnitypes.DNS      `json:"dns"`
		Range               string            `json:"range"`
		RangeStart          string            `json:"range_start,omitempty"`
		RangeEnd            string            `json:"range_end,omitempty"`
		GatewayStr          string            `json:"gateway"`
		EtcdHost            string            `json:"etcd_host,omitempty"`
		EtcdUsername        string            `json:"etcd_username,omitempty"`
		EtcdPassword        string            `json:"etcd_password,omitempty"`
		EtcdKeyFile         string            `json:"etcd_key_file,omitempty"`
		EtcdCertFile        string            `json:"etcd_cert_file,omitempty"`
		EtcdCACertFile      string            `json:"etcd_ca_cert_file,omitempty"`
		LeaderLeaseDuration int               `json:"leader_lease_duration,omitempty"`
		LeaderRenewDeadline int               `json:"leader_renew_deadline,omitempty"`
		LeaderRetryPeriod   int               `json:"leader_retry_period,omitempty"`
		LogFile             string            `json:"log_file"`
		LogLevel            string            `json:"log_level"`
		OverlappingRanges   bool              `json:"enable_overlapping_ranges,omitempty"`
		SleepForRace        int               `json:"sleep_for_race,omitempty"`
		Gateway             string
		Kubernetes          KubernetesConfig `json:"kubernetes,omitempty"`
		ConfigurationPath   string           `json:"configuration_path"`
		PodName             string
		PodNamespace        string
	}

	var ipamConfigAlias IPAMConfigAlias
	if err := json.Unmarshal(data, &ipamConfigAlias); err != nil {
		return err
	}

	var rangeStart, rangeEnd net.IP
	if rs, err := sanitizeIP(ipamConfigAlias.RangeStart); err == nil {
		rangeStart = rs
	}
	if re, err := sanitizeIP(ipamConfigAlias.RangeEnd); err == nil {
		rangeEnd = re
	}

	var gateway net.IP
	if gw, err := sanitizeIP(ipamConfigAlias.Gateway); err == nil {
		gateway = gw
	}

	*ic = IPAMConfig{
		Name:                ipamConfigAlias.Name,
		Type:                ipamConfigAlias.Type,
		Routes:              ipamConfigAlias.Routes,
		Datastore:           ipamConfigAlias.Datastore,
		Addresses:           ipamConfigAlias.Addresses,
		OmitRanges:          ipamConfigAlias.OmitRanges,
		DNS:                 ipamConfigAlias.DNS,
		Range:               ipamConfigAlias.Range,
		RangeStart:          rangeStart,
		RangeEnd:            rangeEnd,
		GatewayStr:          ipamConfigAlias.GatewayStr,
		EtcdHost:            ipamConfigAlias.EtcdHost,
		EtcdUsername:        ipamConfigAlias.EtcdUsername,
		EtcdPassword:        ipamConfigAlias.EtcdPassword,
		EtcdKeyFile:         ipamConfigAlias.EtcdKeyFile,
		EtcdCertFile:        ipamConfigAlias.EtcdCertFile,
		EtcdCACertFile:      ipamConfigAlias.EtcdCACertFile,
		LeaderLeaseDuration: ipamConfigAlias.LeaderLeaseDuration,
		LeaderRenewDeadline: ipamConfigAlias.LeaderRenewDeadline,
		LeaderRetryPeriod:   ipamConfigAlias.LeaderRetryPeriod,
		LogFile:             ipamConfigAlias.LogFile,
		LogLevel:            ipamConfigAlias.LogLevel,
		OverlappingRanges:   ipamConfigAlias.OverlappingRanges,
		SleepForRace:        ipamConfigAlias.SleepForRace,
		Gateway:             gateway,
		Kubernetes:          ipamConfigAlias.Kubernetes,
		ConfigurationPath:   ipamConfigAlias.ConfigurationPath,
		PodName:             ipamConfigAlias.PodName,
		PodNamespace:        ipamConfigAlias.PodNamespace,
	}
	return nil
}

// IPAMEnvArgs are the environment vars we expect
type IPAMEnvArgs struct {
	cnitypes.CommonArgs
	IP                         cnitypes.UnmarshallableString `json:"ip,omitempty"`
	GATEWAY                    cnitypes.UnmarshallableString `json:"gateway,omitempty"`
	K8S_POD_NAME               cnitypes.UnmarshallableString //revive:disable-line
	K8S_POD_NAMESPACE          cnitypes.UnmarshallableString //revive:disable-line
	K8S_POD_INFRA_CONTAINER_ID cnitypes.UnmarshallableString //revive:disable-line
}

// KubernetesConfig describes the kubernetes-specific configuration details
type KubernetesConfig struct {
	KubeConfigPath string `json:"kubeconfig,omitempty"`
	K8sAPIRoot     string `json:"k8s_api_root,omitempty"`
}

// Address is our standard address.
type Address struct {
	AddressStr string `json:"address"`
	Gateway    net.IP `json:"gateway,omitempty"`
	Address    net.IPNet
	Version    string
}

// IPReservation is an address that has been reserved by this plugin
type IPReservation struct {
	IP          net.IP `json:"ip"`
	ContainerID string `json:"id"`
	PodRef      string `json:"podref,omitempty"`
	IsAllocated bool
}

func (ir IPReservation) String() string {
	return fmt.Sprintf("IP: %s is reserved for pod: %s", ir.IP.String(), ir.PodRef)
}

const (
	// Allocate operation identifier
	Allocate = 0
	// Deallocate operation identifier
	Deallocate = 1
)
