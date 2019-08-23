package config

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	cnitypes "github.com/containernetworking/cni/pkg/types"
	types020 "github.com/containernetworking/cni/pkg/types/020"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
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
func LoadIPAMConfig(bytes []byte, envArgs string) (*types.IPAMConfig, string, error) {
	n := types.Net{}
	if err := json.Unmarshal(bytes, &n); err != nil {
		return nil, "", fmt.Errorf("LoadIPAMConfig - JSON Parsing Error: %s / bytes: %s", err, bytes)
	}

	if n.IPAM == nil {
		return nil, "", fmt.Errorf("IPAM config missing 'ipam' key")
	}

	// Logging
	if n.IPAM.LogFile != "" {
		logging.SetLogFile(n.IPAM.LogFile)
	}
	if n.IPAM.LogLevel != "" {
		logging.SetLogLevel(n.IPAM.LogLevel)
	}

	if r := strings.SplitN(n.IPAM.Range, "-", 2); len(r) == 2 {
		firstip := net.ParseIP(r[0])
		if firstip == nil {
			return nil, "", fmt.Errorf("invalid range start IP: %s", r[0])
		}
		lastip, ipNet, err := net.ParseCIDR(r[1])
		if err != nil {
			return nil, "", fmt.Errorf("invalid CIDR %s: %s", r[1], err)
		}
		if !ipNet.Contains(firstip) {
			return nil, "", fmt.Errorf("invalid range start for CIDR %s: %s", ipNet.String(), firstip)
		}
		n.IPAM.Range = ipNet.String()
		n.IPAM.RangeStart = firstip
		n.IPAM.RangeEnd = lastip
	} else {
		firstip, ipNet, err := net.ParseCIDR(n.IPAM.Range)
		if err != nil {
			return nil, "", fmt.Errorf("invalid CIDR %s: %s", n.IPAM.Range, err)
		}
		n.IPAM.Range = ipNet.String()
		n.IPAM.RangeStart = firstip
	}

	if n.IPAM.EtcdHost == "" {
		nostoragemessage := "You have not specified a storage engine (looks like you're missing the `etcd_host` parameter in your config)"
		return nil, "", fmt.Errorf(nostoragemessage)
	}

	// fmt.Printf("Range IP: %s / Subnet: %s", ip, subnet)

	if n.IPAM.GatewayStr != "" {
		gwip := net.ParseIP(n.IPAM.GatewayStr)
		if gwip == nil {
			return nil, "", fmt.Errorf("Couldn't parse gateway IP: %s", n.IPAM.GatewayStr)
		}
		n.IPAM.Gateway = gwip
	}

	for i := range n.IPAM.OmitRanges {
		_, _, err := net.ParseCIDR(n.IPAM.OmitRanges[i])
		if err != nil {
			return nil, "", fmt.Errorf("invalid CIDR in exclude list %s: %s", n.IPAM.OmitRanges[i], err)
		}
	}

	if err := configureStatic(&n, envArgs); err != nil {
		return nil, "", err
	}

	// Copy net name into IPAM so not to drag Net struct around
	n.IPAM.Name = n.Name

	return n.IPAM, n.CNIVersion, nil
}

func configureStatic(n *types.Net, envArgs string) error {

	// Validate all ranges
	numV4 := 0
	numV6 := 0

	for i := range n.IPAM.Addresses {
		ip, addr, err := net.ParseCIDR(n.IPAM.Addresses[i].AddressStr)
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

	if envArgs != "" {
		newnumV6, newnumV4, err := handleEnvArgs(n, numV6, numV4, envArgs)
		if err != nil {
			return err
		}
		numV4 = newnumV4
		numV6 = newnumV6
	}

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

func handleEnvArgs(n *types.Net, numV6 int, numV4 int, envArgs string) (int, int, error) {

	e := types.IPAMEnvArgs{}
	err := cnitypes.LoadArgs(envArgs, &e)
	if err != nil {
		return numV6, numV4, err
	}

	if e.IP != "" {
		for _, item := range strings.Split(string(e.IP), ",") {
			ipstr := strings.TrimSpace(item)

			ip, subnet, err := net.ParseCIDR(ipstr)
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

	if e.GATEWAY != "" {
		for _, item := range strings.Split(string(e.GATEWAY), ",") {
			gwip := net.ParseIP(strings.TrimSpace(item))
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
