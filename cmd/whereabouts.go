package main

import (
	"fmt"
	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/dougbtv/whereabouts/pkg/allocate"
	"github.com/dougbtv/whereabouts/pkg/config"
	"github.com/dougbtv/whereabouts/pkg/logging"
)

func main() {
	// TODO: implement plugin version
	skel.PluginMain(cmdAdd, cmdGet, cmdDel, version.All, "TODO")
}

func cmdGet(args *skel.CmdArgs) error {
	// TODO
	return fmt.Errorf("CNI GET method is not implemented")
}

func cmdAdd(args *skel.CmdArgs) error {
	ipamConf, confVersion, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		logging.Errorf("IPAM configuration load failed: %s", err)
		return err
	}
	logging.Debugf("IPAM configuration successfully read: %+v", ipamConf)

	// Initialize our result, and assign DNS & routing.
	result := &current.Result{}
	result.DNS = ipamConf.DNS
	result.Routes = ipamConf.Routes

	// If there were more than one storage engine, we'd switch out here.
	if ipamConf.EtcdHost != "" {
		newip, err := allocate.AssignIP()
		if err != nil {
			return fmt.Errorf("Error assigning IP: %s", err)
		}
		result.IPs = append(result.IPs, &current.IPConfig{
			Version: "4",
			Address: newip,
			Gateway: ipamConf.Gateway})

	} else {
		return fmt.Errorf("You have not specified a storage engine (looks like you're missing the `etcd_host` parameter in your config)")
	}

	// Assign the

	// Assign all the static IP elements.
	for _, v := range ipamConf.Addresses {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version: v.Version,
			Address: v.Address,
			Gateway: v.Gateway})
	}

	return cnitypes.PrintResult(result, confVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	// TODO
	return nil
}
