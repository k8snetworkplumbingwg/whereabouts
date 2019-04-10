package config

import (
  "encoding/json"
  "fmt"
  cnitypes "github.com/containernetworking/cni/pkg/types"
  "github.com/containernetworking/cni/pkg/types/020"
  "github.com/dougbtv/whereabouts/logging"
  "github.com/dougbtv/whereabouts/types"
  "net"
  "strings"
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
    return nil, "", err
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

  _, _, err := net.ParseCIDR(n.IPAM.Range)
  if err != nil {
    return nil, "", fmt.Errorf("invalid CIDR %s: %s", n.IPAM.Range, err)
  }

  // fmt.Printf("Range IP: %s / Subnet: %s", ip, subnet)

  if n.IPAM.GatewayStr != "" {
    gwip := net.ParseIP(n.IPAM.GatewayStr)
    if gwip == nil {
      return nil, "", fmt.Errorf("Couldn't parse gateway IP: %s", n.IPAM.GatewayStr)
    }
    n.IPAM.Gateway = gwip
  }

  // Validate all ranges
  numV4 := 0
  numV6 := 0

  for i := range n.IPAM.Addresses {
    ip, addr, err := net.ParseCIDR(n.IPAM.Addresses[i].AddressStr)
    if err != nil {
      return nil, "", fmt.Errorf("invalid CIDR %s: %s", n.IPAM.Addresses[i].AddressStr, err)
    }
    n.IPAM.Addresses[i].Address = *addr
    n.IPAM.Addresses[i].Address.IP = ip

    if err := canonicalizeIP(&n.IPAM.Addresses[i].Address.IP); err != nil {
      return nil, "", fmt.Errorf("invalid address %d: %s", i, err)
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
    e := types.IPAMEnvArgs{}
    err := cnitypes.LoadArgs(envArgs, &e)
    if err != nil {
      return nil, "", err
    }

    if e.IP != "" {
      for _, item := range strings.Split(string(e.IP), ",") {
        ipstr := strings.TrimSpace(item)

        ip, subnet, err := net.ParseCIDR(ipstr)
        if err != nil {
          return nil, "", fmt.Errorf("invalid CIDR %s: %s", ipstr, err)
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
          return nil, "", fmt.Errorf("invalid gateway address: %s", item)
        }

        for i := range n.IPAM.Addresses {
          if n.IPAM.Addresses[i].Address.Contains(gwip) {
            n.IPAM.Addresses[i].Gateway = gwip
          }
        }
      }
    }
  }

  // CNI spec 0.2.0 and below supported only one v4 and v6 address
  if numV4 > 1 || numV6 > 1 {
    for _, v := range types020.SupportedVersions {
      if n.CNIVersion == v {
        return nil, "", fmt.Errorf("CNI version %v does not support more than 1 address per family", n.CNIVersion)
      }
    }
  }

  // Copy net name into IPAM so not to drag Net struct around
  n.IPAM.Name = n.Name

  return n.IPAM, n.CNIVersion, nil
}
