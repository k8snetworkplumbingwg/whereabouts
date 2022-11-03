package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"

	cnitypes "github.com/containernetworking/cni/pkg/types"
	types020 "github.com/containernetworking/cni/pkg/types/020"
	"github.com/imdario/mergo"

	netutils "k8s.io/utils/net"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// canonicalizeIP makes sure a provided ip is in standard form
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

// LoadIPAMConfig creates IPAMConfig using json encoded configuration provided
// as `bytes`. At the moment values provided in envArgs are ignored so there
// is no possibility to overload the json configuration using envArgs
func LoadIPAMConfig(bytes []byte, envArgs string, extraConfigPaths ...string) (*types.IPAMConfig, string, error) {

	var n types.Net
	if err := json.Unmarshal(bytes, &n); err != nil {
		return nil, "", fmt.Errorf("LoadIPAMConfig - JSON Parsing Error: %s / bytes: %s", err, bytes)
	}

	if n.IPAM == nil {
		return nil, "", fmt.Errorf("IPAM config missing 'ipam' key")
	} else if !isNetworkRelevant(n.IPAM) {
		return nil, "", NewInvalidPluginError(n.IPAM.Type)
	}

	args := types.IPAMEnvArgs{}
	if err := cnitypes.LoadArgs(envArgs, &args); err != nil {
		return nil, "", fmt.Errorf("LoadArgs - CNI Args Parsing Error: %s", err)
	}
	n.IPAM.PodName = string(args.K8S_POD_NAME)
	n.IPAM.PodNamespace = string(args.K8S_POD_NAMESPACE)

	flatipam, foundflatfile, err := GetFlatIPAM(false, n.IPAM, extraConfigPaths...)
	if err != nil {
		return nil, "", err
	}

	// Now let's try to merge the configurations...
	// NB: Don't try to do any initialization before this point or it won't account for merged flat file.
	if err := mergo.Merge(&n, flatipam); err != nil {
		logging.Errorf("Merge error with flat file: %s", err)
	}

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
				return nil, "", fmt.Errorf("invalid CIDR (do you have the 'range' parameter set for Whereabouts?) '%s': %s", r[1], err)
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
				return nil, "", fmt.Errorf("invalid CIDR %s: %s", n.IPAM.IPRanges[idx].Range, err)
			}
			n.IPAM.IPRanges[idx].Range = ipNet.String()
			if n.IPAM.IPRanges[idx].RangeStart == nil {
				firstip = netutils.ParseIPSloppy(firstip.Mask(ipNet.Mask).String()) // if range_start is not net then pick the first network address
				n.IPAM.IPRanges[idx].RangeStart = firstip
			}
		}
	}

	n.IPAM.OmitRanges = nil
	n.IPAM.Range = ""
	n.IPAM.RangeStart = nil
	n.IPAM.RangeEnd = nil

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
	for i := range n.IPAM.OmitRanges {
		_, _, err := netutils.ParseCIDRSloppy(n.IPAM.OmitRanges[i])
		if err != nil {
			return nil, "", fmt.Errorf("invalid CIDR in exclude list %s: %s", n.IPAM.OmitRanges[i], err)
		}
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
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func configureStatic(n *types.Net, args types.IPAMEnvArgs) error {

	// Validate all ranges
	numV4 := 0
	numV6 := 0

	for i := range n.IPAM.Addresses {
		ip, addr, err := netutils.ParseCIDRSloppy(n.IPAM.Addresses[i].AddressStr)
		if err != nil {
			return fmt.Errorf("invalid CIDR in addresses %s: %s", n.IPAM.Addresses[i].AddressStr, err)
		}
		n.IPAM.Addresses[i].Address = *addr
		n.IPAM.Addresses[i].Address.IP = ip

		if err := canonicalizeIP(&n.IPAM.Addresses[i].Address.IP); err != nil {
			return fmt.Errorf("invalid address %d: %s", i, err)
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
		for _, v := range types020.SupportedVersions {
			if n.CNIVersion == v {
				return fmt.Errorf("CNI version %v does not support more than 1 address per family", n.CNIVersion)
			}
		}
	}

	return nil

}

func GetFlatIPAM(isControlLoop bool, IPAM *types.IPAMConfig, extraConfigPaths ...string) (types.Net, string, error) {
	// Once we have our basics, let's look for our (optional) configuration file
	confdirs := []string{"/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf", "/etc/cni/net.d/whereabouts.d/whereabouts.conf", "/host/etc/cni/net.d/whereabouts.d/whereabouts.conf"}
	confdirs = append(confdirs, extraConfigPaths...)
	// We prefix the optional configuration path (so we look there first)

	if !isControlLoop && IPAM != nil {
		if IPAM.ConfigurationPath != "" {
			confdirs = append([]string{IPAM.ConfigurationPath}, confdirs...)
		}
	}

	// Cycle through the path and parse the JSON config
	flatipam := types.Net{}
	foundflatfile := ""
	for _, confpath := range confdirs {
		if pathExists(confpath) {
			jsonFile, err := os.Open(confpath)
			if err != nil {
				return flatipam, foundflatfile, fmt.Errorf("error opening flat configuration file @ %s with: %s", confpath, err)
			}

			defer jsonFile.Close()

			jsonBytes, err := ioutil.ReadAll(jsonFile)
			if err != nil {
				return flatipam, foundflatfile, fmt.Errorf("LoadIPAMConfig Flatfile (%s) - ioutil.ReadAll error: %s", confpath, err)
			}

			if err := json.Unmarshal(jsonBytes, &flatipam.IPAM); err != nil {
				return flatipam, foundflatfile, fmt.Errorf("LoadIPAMConfig Flatfile (%s) - JSON Parsing Error: %s / bytes: %s", confpath, err, jsonBytes)
			}

			foundflatfile = confpath
			return flatipam, foundflatfile, err
		}
	}
	var err error
	return flatipam, foundflatfile, err
}

func handleEnvArgs(n *types.Net, numV6 int, numV4 int, args types.IPAMEnvArgs) (int, int, error) {

	if args.IP != "" {
		for _, item := range strings.Split(string(args.IP), ",") {
			ipstr := strings.TrimSpace(item)

			ip, subnet, err := netutils.ParseCIDRSloppy(ipstr)
			if err != nil {
				return numV6, numV4, fmt.Errorf("invalid CIDR %s: %s", ipstr, err)
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
	return fmt.Sprintf("only interested in networks whose IPAM type is 'whereabouts'. This one was: %s", e.ipamType)
}

func storageError() error {
	return fmt.Errorf("you have not configured the storage engine (looks like you're using an invalid `kubernetes.kubeconfig` parameter in your config)")
}
