// Package config includes configuration utilities for whereabouts.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	cnitypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	cniversion "github.com/containernetworking/cni/pkg/version"
	"github.com/imdario/mergo"

	netutils "k8s.io/utils/net"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// canonicalizeIP makes sure a provided ip is in standard form.
func canonicalizeIP(ip *net.IP) error {
	if ip.To4() != nil {
		*ip = ip.To4()
		return nil
	} else if ip.To16() != nil {
		*ip = ip.To16()
		return nil
	}
	return fmt.Errorf("IP %s not v4 nor v6", *ip)
}

// maxConfigBytes limits the size of the IPAM configuration JSON to prevent
// excessive memory allocation from malformed or malicious input. 1 MiB is
// far larger than any reasonable configuration but caps potential abuse.
const maxConfigBytes = 1 << 20 // 1 MiB

// LoadIPAMConfig creates IPAMConfig using json encoded configuration provided
// as `bytes`. At the moment values provided in envArgs are ignored so there
// is no possibility to overload the json configuration using envArgs.
func LoadIPAMConfig(bytes []byte, envArgs string, extraConfigPaths ...string) (*types.IPAMConfig, string, error) {
	if len(bytes) > maxConfigBytes {
		return nil, "", fmt.Errorf("IPAM configuration too large (%d bytes, max %d)", len(bytes), maxConfigBytes)
	}
	var n types.Net
	if err := json.Unmarshal(bytes, &n); err != nil {
		return nil, "", fmt.Errorf("LoadIPAMConfig - JSON Parsing Error: %w / bytes: %s", err, bytes)
	}

	if n.IPAM == nil {
		return nil, "", fmt.Errorf("IPAM config missing 'ipam' key")
	} else if !isNetworkRelevant(n.IPAM) {
		return nil, "", NewInvalidPluginError(n.IPAM.Type)
	}

	args := types.IPAMEnvArgs{}
	if err := cnitypes.LoadArgs(envArgs, &args); err != nil {
		return nil, "", fmt.Errorf("LoadArgs - CNI Args Parsing Error: %w", err)
	}
	n.IPAM.PodName = string(args.K8S_POD_NAME)
	n.IPAM.PodNamespace = string(args.K8S_POD_NAMESPACE)

	flatipam, foundflatfile, err := GetFlatIPAM(false, n.IPAM, extraConfigPaths...)
	if err != nil {
		// Config file not found is non-fatal — inline IPAM config may be sufficient.
		var notFoundErr *FileNotFoundError
		if !errors.As(err, &notFoundErr) {
			return nil, "", err
		}
	}

	// Now let's try to merge the configurations...
	// NB: Don't try to do any initialization before this point or it won't account for merged flat file.
	var OverlappingRanges = n.IPAM.OverlappingRanges
	if err := mergo.Merge(&n, flatipam); err != nil {
		return nil, "", logging.Errorf("merge error with flat file: %w", err)
	}
	n.IPAM.OverlappingRanges = OverlappingRanges

	// Logging
	if n.IPAM.LogFile != "" {
		logging.SetLogFile(n.IPAM.LogFile)
	}
	if n.IPAM.LogLevel != "" {
		logging.SetLogLevel(n.IPAM.LogLevel)
	}

	if foundflatfile != "" {
		logging.Debugf("Used defaults from parsed flat file config @ %s", foundflatfile)
	}

	if n.IPAM.Range != "" {
		oldRange := types.RangeConfiguration{
			OmitRanges: n.IPAM.OmitRanges,
			Range:      n.IPAM.Range,
			RangeStart: n.IPAM.RangeStart,
			RangeEnd:   n.IPAM.RangeEnd,
		}

		n.IPAM.IPRanges = append([]types.RangeConfiguration{oldRange}, n.IPAM.IPRanges...)
	}

	for idx := range n.IPAM.IPRanges {
		if r := strings.SplitN(n.IPAM.IPRanges[idx].Range, "-", 2); len(r) == 2 {
			firstip := netutils.ParseIPSloppy(r[0])
			if firstip == nil {
				return nil, "", fmt.Errorf("invalid range start IP: %s", r[0])
			}
			lastip, ipNet, err := netutils.ParseCIDRSloppy(r[1])
			if err != nil {
				return nil, "", fmt.Errorf("invalid CIDR (do you have the 'range' parameter set for Whereabouts?) '%s': %w", r[1], err)
			}
			if !ipNet.Contains(firstip) {
				return nil, "", fmt.Errorf("invalid range start for CIDR %s: %s", ipNet.String(), firstip)
			}
			n.IPAM.IPRanges[idx].Range = ipNet.String()
			n.IPAM.IPRanges[idx].RangeStart = firstip
			n.IPAM.IPRanges[idx].RangeEnd = lastip
		} else {
			firstip, ipNet, err := netutils.ParseCIDRSloppy(n.IPAM.IPRanges[idx].Range)
			if err != nil {
				logging.Debugf("invalid cidr error on range %v, within ranges %v", n.IPAM.IPRanges[idx].Range, n.IPAM.IPRanges)
				return nil, "", fmt.Errorf("invalid CIDR %s: %w", n.IPAM.IPRanges[idx].Range, err)
			}
			n.IPAM.IPRanges[idx].Range = ipNet.String()
			if n.IPAM.IPRanges[idx].RangeStart == nil {
				firstip = netutils.ParseIPSloppy(firstip.Mask(ipNet.Mask).String()) // if range_start is not net then pick the first network address
				n.IPAM.IPRanges[idx].RangeStart = firstip
			}
		}
	}

	for i := range n.IPAM.OmitRanges {
		_, _, err := netutils.ParseCIDRSloppy(n.IPAM.OmitRanges[i])
		if err != nil {
			return nil, "", fmt.Errorf("invalid CIDR in exclude list %s: %w", n.IPAM.OmitRanges[i], err)
		}
	}

	n.IPAM.OmitRanges = nil
	n.IPAM.Range = ""
	n.IPAM.RangeStart = nil
	n.IPAM.RangeEnd = nil

	// Propagate enable_l3 from the top-level IPAM config to every IP range.
	// Individual ranges may also set enable_l3 directly for mixed L2/L3 setups.
	if n.IPAM.EnableL3 {
		for idx := range n.IPAM.IPRanges {
			n.IPAM.IPRanges[idx].L3 = true
		}
	}

	if n.IPAM.Kubernetes.KubeConfigPath == "" {
		return nil, "", storageError()
	}

	if n.IPAM.GatewayStr != "" {
		gwip := netutils.ParseIPSloppy(n.IPAM.GatewayStr)
		if gwip == nil {
			return nil, "", fmt.Errorf("couldn't parse gateway IP: %s", n.IPAM.GatewayStr)
		}
		n.IPAM.Gateway = gwip
	}

	// When exclude_gateway is enabled and a gateway IP is configured, add it
	// as a /32 (or /128 for IPv6) exclusion to every IP range so the gateway
	// address is never allocated to a pod. This is useful for L2 networks
	// where the gateway must remain free. For L3-only use cases (BGP routing,
	// no gateway), this option should remain disabled (the default).
	if n.IPAM.ExcludeGateway && n.IPAM.Gateway != nil {
		suffix := "/32"
		if n.IPAM.Gateway.To4() == nil {
			suffix = "/128"
		}
		gatewayExclude := n.IPAM.Gateway.String() + suffix
		for idx := range n.IPAM.IPRanges {
			n.IPAM.IPRanges[idx].OmitRanges = append(n.IPAM.IPRanges[idx].OmitRanges, gatewayExclude)
		}
		logging.Debugf("Gateway %s excluded from allocation in all IP ranges", n.IPAM.Gateway)
	}

	if err := configureStatic(&n, args); err != nil {
		return nil, "", err
	}

	if n.IPAM.LeaderLeaseDuration == 0 {
		n.IPAM.LeaderLeaseDuration = types.DefaultLeaderLeaseDuration
	}

	if n.IPAM.LeaderRenewDeadline == 0 {
		n.IPAM.LeaderRenewDeadline = types.DefaultLeaderRenewDeadline
	}

	if n.IPAM.LeaderRetryPeriod == 0 {
		n.IPAM.LeaderRetryPeriod = types.DefaultLeaderRetryPeriod
	}

	// Copy net name into IPAM so not to drag Net struct around
	n.IPAM.Name = n.Name

	return n.IPAM, n.CNIVersion, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func configureStatic(n *types.Net, args types.IPAMEnvArgs) error {
	// Validate all ranges
	numV4 := 0
	numV6 := 0

	for i := range n.IPAM.Addresses {
		ip, addr, err := netutils.ParseCIDRSloppy(n.IPAM.Addresses[i].AddressStr)
		if err != nil {
			return fmt.Errorf("invalid CIDR in addresses %s: %w", n.IPAM.Addresses[i].AddressStr, err)
		}
		n.IPAM.Addresses[i].Address = *addr
		n.IPAM.Addresses[i].Address.IP = ip

		if err := canonicalizeIP(&n.IPAM.Addresses[i].Address.IP); err != nil {
			return fmt.Errorf("invalid address %d: %w", i, err)
		}

		if n.IPAM.Addresses[i].Address.IP.To4() != nil {
			n.IPAM.Addresses[i].Version = "4"
			numV4++
		} else {
			n.IPAM.Addresses[i].Version = "6"
			numV6++
		}
	}

	newnumV6, newnumV4, err := handleEnvArgs(n, numV6, numV4, args)
	if err != nil {
		return err
	}
	numV4 = newnumV4
	numV6 = newnumV6

	// CNI spec 0.2.0 and below supported only one v4 and v6 address
	if numV4 > 1 || numV6 > 1 {
		for _, v := range []string{"", "0.1.0", "0.2.0"} {
			if n.CNIVersion == v {
				return fmt.Errorf("CNI version %v does not support more than 1 address per family", n.CNIVersion)
			}
		}
	}

	return nil
}

func GetFlatIPAM(isControlLoop bool, ipamConfig *types.IPAMConfig, extraConfigPaths ...string) (types.Net, string, error) {
	// Once we have our basics, let's look for our (optional) configuration file
	confdirs := []string{"/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf", "/etc/cni/net.d/whereabouts.d/whereabouts.conf", "/host/etc/cni/net.d/whereabouts.d/whereabouts.conf"}
	confdirs = append(confdirs, extraConfigPaths...)
	// We prefix the optional configuration path (so we look there first)

	if !isControlLoop && ipamConfig != nil {
		if ipamConfig.ConfigurationPath != "" {
			cleanPath := filepath.Clean(ipamConfig.ConfigurationPath)
			if strings.Contains(cleanPath, "..") {
				return types.Net{}, "", fmt.Errorf("configuration_path %q contains path traversal", ipamConfig.ConfigurationPath)
			}
			confdirs = append([]string{cleanPath}, confdirs...)
		}
	}

	// Cycle through the path and parse the JSON config
	flatipam := types.Net{}
	foundflatfile := ""
	for _, confpath := range confdirs {
		if !pathExists(confpath) {
			continue
		}

		jsonFile, err := os.Open(confpath)
		if err != nil {
			return flatipam, foundflatfile, fmt.Errorf("error opening flat configuration file @ %s with: %w", confpath, err)
		}

		jsonBytes, err := io.ReadAll(jsonFile)
		jsonFile.Close()
		if err != nil {
			return flatipam, foundflatfile, fmt.Errorf("LoadIPAMConfig Flatfile (%s) - io.ReadAll error: %w", confpath, err)
		}

		if err := json.Unmarshal(jsonBytes, &flatipam.IPAM); err != nil {
			return flatipam, foundflatfile, fmt.Errorf("LoadIPAMConfig Flatfile (%s) - JSON Parsing Error: %w / bytes: %s", confpath, err, jsonBytes)
		}

		foundflatfile = confpath
		return flatipam, foundflatfile, nil
	}

	return flatipam, foundflatfile, NewFileNotFoundError()
}

func handleEnvArgs(n *types.Net, numV6 int, numV4 int, args types.IPAMEnvArgs) (v6Count, v4Count int, err error) {
	if args.IP != "" {
		for _, item := range strings.Split(string(args.IP), ",") {
			ipstr := strings.TrimSpace(item)

			ip, subnet, err := netutils.ParseCIDRSloppy(ipstr)
			if err != nil {
				return numV6, numV4, fmt.Errorf("invalid CIDR %s: %w", ipstr, err)
			}

			addr := types.Address{Address: net.IPNet{IP: ip, Mask: subnet.Mask}}
			if addr.Address.IP.To4() != nil {
				addr.Version = "4"
				numV4++
			} else {
				addr.Version = "6"
				numV6++
			}
			n.IPAM.Addresses = append(n.IPAM.Addresses, addr)
		}
	}

	if args.GATEWAY != "" {
		for _, item := range strings.Split(string(args.GATEWAY), ",") {
			gwip := netutils.ParseIPSloppy(strings.TrimSpace(item))
			if gwip == nil {
				return numV6, numV4, fmt.Errorf("invalid gateway address: %s", item)
			}

			for i := range n.IPAM.Addresses {
				if n.IPAM.Addresses[i].Address.Contains(gwip) {
					n.IPAM.Addresses[i].Gateway = gwip
				}
			}
		}
	}

	return numV6, numV4, nil
}

func LoadIPAMConfiguration(bytes []byte, envArgs string, extraConfigPaths ...string) (*types.IPAMConfig, error) {
	if len(bytes) > maxConfigBytes {
		return nil, fmt.Errorf("IPAM configuration too large (%d bytes, max %d)", len(bytes), maxConfigBytes)
	}
	pluginConfig, err := loadPluginConfig(bytes)
	if err != nil {
		return nil, err
	}

	if pluginConfig.Type == "" {
		pluginConfigList, err := loadPluginConfigList(bytes)
		if err != nil {
			return nil, err
		}

		pluginConfigList.Plugins[0].CNIVersion = pluginConfig.CNIVersion
		firstPluginBytes, err := json.Marshal(pluginConfigList.Plugins[0])
		if err != nil {
			return nil, err
		}
		ipamConfig, _, err := LoadIPAMConfig(firstPluginBytes, envArgs, extraConfigPaths...)
		if err != nil {
			return nil, err
		}
		return ipamConfig, nil
	}

	ipamConfig, _, err := LoadIPAMConfig(bytes, envArgs, extraConfigPaths...)
	if err != nil {
		return nil, err
	}
	return ipamConfig, nil
}

func loadPluginConfigList(bytes []byte) (*types.NetConfList, error) {
	var netConfList types.NetConfList
	if err := json.Unmarshal(bytes, &netConfList); err != nil {
		return nil, err
	}

	return &netConfList, nil
}

func loadPluginConfig(bytes []byte) (*cnitypes.NetConf, error) {
	var pluginConfig cnitypes.NetConf
	if err := json.Unmarshal(bytes, &pluginConfig); err != nil {
		return nil, err
	}
	return &pluginConfig, nil
}

func isNetworkRelevant(ipamConfig *types.IPAMConfig) bool {
	const relevantIPAMType = "whereabouts"
	return ipamConfig.Type == relevantIPAMType
}

type InvalidPluginError struct {
	ipamType string
}

func NewInvalidPluginError(ipamType string) *InvalidPluginError {
	return &InvalidPluginError{ipamType: ipamType}
}

func (e *InvalidPluginError) Error() string {
	return fmt.Sprintf("IPAM type must be 'whereabouts', but got '%s' \u2014 check your CNI configuration", e.ipamType)
}

type FileNotFoundError struct{}

func NewFileNotFoundError() *FileNotFoundError {
	return &FileNotFoundError{}
}

func (e *FileNotFoundError) Error() string {
	return "config file not found"
}

func storageError() error {
	return fmt.Errorf("kubernetes.kubeconfig path is required but not set \u2014 provide it in the IPAM config or in the whereabouts.conf flat file (see doc/extended-configuration.md)")
}

// ParsePrevResult extracts and converts the prevResult field from raw CNI
// stdin bytes. Returns (nil, nil) when no prevResult is present — callers
// should treat a nil result as "no previous result available.".
func ParsePrevResult(stdinData []byte) (*current.Result, error) {
	var raw struct {
		RawPrevResult map[string]interface{} `json:"prevResult,omitempty"`
	}
	if err := json.Unmarshal(stdinData, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal prevResult: %w", err)
	}
	if raw.RawPrevResult == nil {
		return nil, nil
	}

	resultBytes, err := json.Marshal(raw.RawPrevResult)
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal prevResult: %w", err)
	}

	res, err := current.NewResult(resultBytes)
	if err != nil {
		// prevResult may be in an older CNI version format — try conversion.
		rawResult, parseErr := cniversion.NewResult(raw.RawPrevResult["cniVersion"].(string), resultBytes)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse prevResult: %w (original: %v)", parseErr, err)
		}
		converted, convertErr := current.NewResultFromResult(rawResult)
		if convertErr != nil {
			return nil, fmt.Errorf("failed to convert prevResult to current version: %w", convertErr)
		}
		return converted, nil
	}

	typedResult, ok := res.(*current.Result)
	if !ok {
		return nil, fmt.Errorf("prevResult is not a *current.Result")
	}
	return typedResult, nil
}
