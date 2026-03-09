// Package types defines the core data structures for whereabouts IPAM
// configuration, IP reservations, and operation modes.
package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	cnitypes "github.com/containernetworking/cni/pkg/types"
)

// Datastore types.
const (
	DefaultLeaderLeaseDuration    = 1500
	DefaultLeaderRenewDeadline    = 1000
	DefaultLeaderRetryPeriod      = 500
	AddTimeLimit                  = 2 * time.Minute
	DelTimeLimit                  = 1 * time.Minute
	DefaultOverlappingIPsFeatures = true
	DefaultSleepForRace           = 0
	// MaxSleepForRace caps the sleep_for_race debug parameter to prevent
	// unbounded sleeps that could be used as a denial-of-service vector.
	MaxSleepForRace = 10
)

// Net is The top-level network config - IPAM plugins are passed the full configuration
// of the calling plugin, not just the IPAM section.
type Net struct {
	Name          string                 `json:"name"`
	CNIVersion    string                 `json:"cniVersion"`
	IPAM          *IPAMConfig            `json:"ipam"`
	RawPrevResult map[string]interface{} `json:"prevResult,omitempty"`
}

// NetConfList describes an ordered list of networks.
type NetConfList struct {
	CNIVersion string `json:"cniVersion,omitempty"`

	Name         string `json:"name,omitempty"`
	DisableCheck bool   `json:"disableCheck,omitempty"`
	Plugins      []*Net `json:"plugins,omitempty"`
}

// RangeConfiguration defines a single IP range from which addresses are
// allocated, with optional start/end bounds and exclude lists. Used in the
// ipRanges array for multi-range and dual-stack configurations.
type RangeConfiguration struct {
	// OmitRanges lists CIDRs to exclude from allocation within this range.
	OmitRanges []string `json:"exclude,omitempty"`
	// Range is the CIDR notation for this IP range (e.g., "192.168.1.0/24").
	Range string `json:"range"`
	// RangeStart optionally restricts allocation to start at this IP.
	RangeStart net.IP `json:"range_start,omitempty"`
	// RangeEnd optionally restricts allocation to end at this IP.
	RangeEnd net.IP `json:"range_end,omitempty"`
	// PreferredIP is an optional preferred IP address to assign. When set and
	// available, this IP is assigned instead of the lowest free IP. Used for
	// sticky IP assignment across pod restarts. See upstream #621.
	PreferredIP net.IP `json:"-"`
	// L3 enables L3/routed mode for this range. In L3 mode, all IPs in the
	// subnet are usable — there is no network or broadcast address exclusion.
	// This is appropriate for pure L3 (BGP, routed) environments where every
	// IP is individually routable and there is no broadcast domain.
	L3 bool `json:"enable_l3,omitempty"`
}

// IPAMConfig describes the expected json configuration for this plugin.
// JSON tags use snake_case to match the CNI configuration format.
type IPAMConfig struct {
	// Name is the CNI network name, copied from the top-level Net struct.
	Name string
	// Type is the IPAM plugin type (must be "whereabouts").
	Type string `json:"type"`
	// Routes defines static routes to be added by the CNI plugin.
	Routes []*cnitypes.Route `json:"routes"`
	// Addresses holds static IP addresses for the interface (used by configureStatic).
	Addresses []Address `json:"addresses,omitempty"`
	// IPRanges defines multiple IP ranges for allocation (e.g. dual-stack).
	IPRanges []RangeConfiguration `json:"ipRanges"`
	// OmitRanges lists CIDRs to exclude from allocation.
	OmitRanges []string `json:"exclude,omitempty"`
	// DNS configures DNS for the interface.
	DNS cnitypes.DNS `json:"dns"`
	// Range is the primary CIDR range for IP allocation.
	Range string `json:"range"`
	// NodeSliceSize sets the prefix length (e.g. "/28" or "28") for per-node
	// IP slices. Enables the experimental Fast IPAM feature when non-empty.
	NodeSliceSize string `json:"node_slice_size"`
	// RangeStart optionally restricts allocation to start at this IP within the Range.
	RangeStart net.IP `json:"range_start,omitempty"`
	// RangeEnd optionally restricts allocation to end at this IP within the Range.
	RangeEnd net.IP `json:"range_end,omitempty"`
	// GatewayStr is the gateway IP as a string, parsed from the "gateway" JSON key.
	GatewayStr string `json:"gateway"`
	// LeaderLeaseDuration is the leader election lease duration in milliseconds.
	LeaderLeaseDuration int `json:"leader_lease_duration,omitempty"`
	// LeaderRenewDeadline is the leader election renew deadline in milliseconds.
	LeaderRenewDeadline int `json:"leader_renew_deadline,omitempty"`
	// LeaderRetryPeriod is the leader election retry period in milliseconds.
	LeaderRetryPeriod int `json:"leader_retry_period,omitempty"`
	// LogFile is the path to the whereabouts log file.
	LogFile string `json:"log_file"`
	// LogLevel is the logging verbosity: "debug", "verbose", "error", or "panic".
	LogLevel string `json:"log_level"`
	// Deprecated: ReconcilerCronExpression was used by the legacy CronJob-based
	// reconciler. The operator now uses --reconcile-interval instead. This field
	// is retained for backward compatibility with existing configurations but
	// has no effect.
	ReconcilerCronExpression string `json:"reconciler_cron_expression,omitempty"`
	// OverlappingRanges enables cluster-wide IP uniqueness checks via
	// OverlappingRangeIPReservation CRDs. Defaults to true.
	OverlappingRanges bool `json:"enable_overlapping_ranges,omitempty"`
	// ExcludeGateway, when true, automatically adds the gateway IP to the
	// exclude list of every IP range, preventing the gateway from being
	// allocated to a pod. Useful for L2 networks where the gateway address
	// must remain free. For L3-only use cases (e.g. BGP routing) where no
	// gateway is present, leave this disabled (the default).
	ExcludeGateway bool `json:"exclude_gateway,omitempty"`
	// OptimisticIPAM, when true, bypasses the leader election lock and
	// relies solely on the optimistic concurrency control (resourceVersion
	// checks with retries) built into the Kubernetes storage backend.
	// This significantly reduces allocation latency in large clusters
	// (600+ pods) where leader election contention causes slow attaches.
	// Trade-off: slightly higher retry rates under heavy concurrent
	// allocation but much lower average latency. See upstream #510, #508.
	OptimisticIPAM bool `json:"optimistic_ipam,omitempty"`
	// EnableL3 enables L3/routed mode for all IP ranges. In L3 mode, all IPs
	// in each subnet are usable — there is no network or broadcast address
	// exclusion. This is appropriate for pure L3 (BGP, routed) environments
	// where every IP is individually routable and there is no broadcast domain.
	// When true, pools do not require a gateway to be configured.
	EnableL3 bool `json:"enable_l3,omitempty"`
	// SleepForRace is a debug parameter that adds artificial delay (in seconds)
	// before pool updates to simulate race conditions. Capped at MaxSleepForRace.
	SleepForRace int `json:"sleep_for_race,omitempty"`
	// Gateway is the parsed net.IP of GatewayStr. It is not directly populated
	// from JSON; instead, GatewayStr is parsed via backwardsCompatibleIPAddress.
	Gateway net.IP
	// Kubernetes holds Kubernetes-specific configuration (kubeconfig path, API root).
	Kubernetes KubernetesConfig `json:"kubernetes,omitempty"`
	// ConfigurationPath is an optional path to the whereabouts flat file configuration.
	ConfigurationPath string `json:"configuration_path"`
	// PodName is the name of the pod requesting an IP, set from CNI_ARGS.
	PodName string
	// PodNamespace is the namespace of the pod requesting an IP, set from CNI_ARGS.
	PodNamespace string
	// NetworkName optionally names the network for multi-tenant scenarios,
	// creating separate IPPool CRs per network name.
	NetworkName string `json:"network_name,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for IPAMConfig.
// It uses an internal alias type to avoid infinite recursion (the alias has no
// custom unmarshaler), and converts string IP fields (RangeStart, RangeEnd,
// Gateway) to net.IP via backwardsCompatibleIPAddress.
func (ic *IPAMConfig) UnmarshalJSON(data []byte) error {
	type IPAMConfigAlias struct {
		Name                     string
		Type                     string               `json:"type"`
		Routes                   []*cnitypes.Route    `json:"routes"`
		Addresses                []Address            `json:"addresses,omitempty"`
		IPRanges                 []RangeConfiguration `json:"ipRanges"`
		NodeSliceSize            string               `json:"node_slice_size"`
		OmitRanges               []string             `json:"exclude,omitempty"`
		DNS                      cnitypes.DNS         `json:"dns"`
		Range                    string               `json:"range"`
		RangeStart               string               `json:"range_start,omitempty"`
		RangeEnd                 string               `json:"range_end,omitempty"`
		GatewayStr               string               `json:"gateway"`
		LeaderLeaseDuration      int                  `json:"leader_lease_duration,omitempty"`
		LeaderRenewDeadline      int                  `json:"leader_renew_deadline,omitempty"`
		LeaderRetryPeriod        int                  `json:"leader_retry_period,omitempty"`
		LogFile                  string               `json:"log_file"`
		LogLevel                 string               `json:"log_level"`
		ReconcilerCronExpression string               `json:"reconciler_cron_expression,omitempty"`
		OverlappingRanges        bool                 `json:"enable_overlapping_ranges,omitempty"`
		ExcludeGateway           bool                 `json:"exclude_gateway,omitempty"`
		OptimisticIPAM           bool                 `json:"optimistic_ipam,omitempty"`
		EnableL3                 bool                 `json:"enable_l3,omitempty"`
		SleepForRace             int                  `json:"sleep_for_race,omitempty"`
		Gateway                  string
		Kubernetes               KubernetesConfig `json:"kubernetes,omitempty"`
		ConfigurationPath        string           `json:"configuration_path"`
		PodName                  string
		PodNamespace             string
		NetworkName              string `json:"network_name,omitempty"`
	}

	ipamConfigAlias := IPAMConfigAlias{
		OverlappingRanges: DefaultOverlappingIPsFeatures,
		SleepForRace:      DefaultSleepForRace,
	}
	if err := json.Unmarshal(data, &ipamConfigAlias); err != nil {
		return err
	}

	*ic = IPAMConfig{
		Name:                     ipamConfigAlias.Name,
		Type:                     ipamConfigAlias.Type,
		Routes:                   ipamConfigAlias.Routes,
		Addresses:                ipamConfigAlias.Addresses,
		IPRanges:                 ipamConfigAlias.IPRanges,
		OmitRanges:               ipamConfigAlias.OmitRanges,
		DNS:                      ipamConfigAlias.DNS,
		Range:                    ipamConfigAlias.Range,
		RangeStart:               backwardsCompatibleIPAddress(ipamConfigAlias.RangeStart),
		RangeEnd:                 backwardsCompatibleIPAddress(ipamConfigAlias.RangeEnd),
		NodeSliceSize:            ipamConfigAlias.NodeSliceSize,
		GatewayStr:               ipamConfigAlias.GatewayStr,
		LeaderLeaseDuration:      ipamConfigAlias.LeaderLeaseDuration,
		LeaderRenewDeadline:      ipamConfigAlias.LeaderRenewDeadline,
		LeaderRetryPeriod:        ipamConfigAlias.LeaderRetryPeriod,
		LogFile:                  ipamConfigAlias.LogFile,
		LogLevel:                 ipamConfigAlias.LogLevel,
		OverlappingRanges:        ipamConfigAlias.OverlappingRanges,
		ExcludeGateway:           ipamConfigAlias.ExcludeGateway,
		OptimisticIPAM:           ipamConfigAlias.OptimisticIPAM,
		EnableL3:                 ipamConfigAlias.EnableL3,
		ReconcilerCronExpression: ipamConfigAlias.ReconcilerCronExpression,
		SleepForRace:             ipamConfigAlias.SleepForRace,
		Gateway:                  backwardsCompatibleIPAddress(ipamConfigAlias.Gateway),
		Kubernetes:               ipamConfigAlias.Kubernetes,
		ConfigurationPath:        ipamConfigAlias.ConfigurationPath,
		PodName:                  ipamConfigAlias.PodName,
		PodNamespace:             ipamConfigAlias.PodNamespace,
		NetworkName:              ipamConfigAlias.NetworkName,
	}
	return nil
}

func (ic *IPAMConfig) GetPodRef() string {
	return fmt.Sprintf("%s/%s", ic.PodNamespace, ic.PodName)
}

func backwardsCompatibleIPAddress(ip string) net.IP {
	var ipAddr net.IP
	if sanitizedIP, err := sanitizeIP(ip); err == nil {
		ipAddr = sanitizedIP
	}
	return ipAddr
}

// IPAMEnvArgs are the environment vars we expect.
type IPAMEnvArgs struct {
	cnitypes.CommonArgs
	IP                         cnitypes.UnmarshallableString `json:"ip,omitempty"`
	GATEWAY                    cnitypes.UnmarshallableString `json:"gateway,omitempty"`
	K8S_POD_NAME               cnitypes.UnmarshallableString //revive:disable-line
	K8S_POD_NAMESPACE          cnitypes.UnmarshallableString //revive:disable-line
	K8S_POD_INFRA_CONTAINER_ID cnitypes.UnmarshallableString //revive:disable-line
}

// KubernetesConfig describes the kubernetes-specific configuration details.
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

// IPReservation is an address that has been reserved by this plugin.
type IPReservation struct {
	// IP is the reserved IP address.
	IP net.IP `json:"ip"`
	// ContainerID is the CNI container ID that owns this reservation.
	ContainerID string `json:"id"`
	// PodRef is the "namespace/name" reference to the owning pod.
	PodRef string `json:"podref"`
	// IfName is the network interface name within the container.
	IfName string `json:"ifName"`
	// IsAllocated is an internal flag used during iteration to mark IPs that
	// are reserved by overlapping ranges but should not be persisted to the pool.
	IsAllocated bool
}

func (ir IPReservation) String() string {
	return fmt.Sprintf("IP: %s is reserved for pod: %s", ir.IP.String(), ir.PodRef)
}

const (
	// Allocate operation identifier.
	Allocate = 0
	// Deallocate operation identifier.
	Deallocate = 1
)

var ErrNoIPRanges = errors.New("no IP ranges in whereabouts config")
