package main

import (
	"context"
	"fmt"
	"net"

	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	cniversion "github.com/containernetworking/cni/pkg/version"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/iphelpers"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/version"
)

func main() {
	skel.PluginMain(func(args *skel.CmdArgs) error {
		ipamConf, confVersion, err := config.LoadIPAMConfig(args.StdinData, args.Args)
		if err != nil {
			logging.Errorf("IPAM configuration load failed: %s", err)
			return err
		}
		logging.Debugf("ADD - IPAM configuration successfully read: %+v", *ipamConf)
		ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, *ipamConf)
		if err != nil {
			return logging.Errorf("failed to create Kubernetes IPAM manager: %v", err)
		}
		defer func() { safeCloseKubernetesBackendConnection(ipam) }()
		return cmdAdd(args, ipam, confVersion)
	},
		cmdCheck,
		func(args *skel.CmdArgs) error {
			ipamConf, _, err := config.LoadIPAMConfig(args.StdinData, args.Args)
			if err != nil {
				logging.Errorf("IPAM configuration load failed: %s", err)
				return err
			}
			logging.Debugf("DEL - IPAM configuration successfully read: %+v", *ipamConf)

			ipam, err := kubernetes.NewKubernetesIPAM(args.ContainerID, *ipamConf)
			if err != nil {
				return logging.Errorf("IPAM client initialization error: %v", err)
			}
			defer func() { safeCloseKubernetesBackendConnection(ipam) }()
			return cmdDel(args, ipam)
		},
		cniversion.All,
		fmt.Sprintf("whereabouts %s", version.GetFullVersionWithRuntimeInfo()),
	)
}

func safeCloseKubernetesBackendConnection(ipam *kubernetes.KubernetesIPAM) {
	if err := ipam.Close(); err != nil {
		_ = logging.Errorf("failed to close the connection to the K8s backend: %v", err)
	}
}

func cmdCheck(args *skel.CmdArgs) error {
	// TODO
	return fmt.Errorf("CNI CHECK method is not implemented")
}

func cmdAdd(args *skel.CmdArgs, client *kubernetes.KubernetesIPAM, cniVersion string) error {
	// Initialize our result, and assign DNS & routing.
	result := &current.Result{}
	result.DNS = client.Config.DNS
	result.Routes = client.Config.Routes

	logging.Debugf("Beginning IPAM for ContainerID: %v", args.ContainerID)
	var newips []net.IPNet

	ctx, cancel := context.WithTimeout(context.Background(), types.AddTimeLimit)
	defer cancel()

	newips, err := kubernetes.IPManagement(ctx, types.Allocate, client.Config, client)
	if err != nil {
		logging.Errorf("Error at storage engine: %s", err)
		return fmt.Errorf("error at storage engine: %w", err)
	}

	var useVersion string
	for _, newip := range newips {
		// Determine if v4 or v6.
		if iphelpers.IsIPv4(newip.IP) {
			useVersion = "4"
		} else {
			useVersion = "6"
		}

		result.IPs = append(result.IPs, &current.IPConfig{
			Version: useVersion,
			Address: newip,
			Gateway: client.Config.Gateway})
	}

	// Assign all the static IP elements.
	for _, v := range client.Config.Addresses {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version: v.Version,
			Address: v.Address,
			Gateway: v.Gateway})
	}

	return cnitypes.PrintResult(result, cniVersion)
}

func cmdDel(args *skel.CmdArgs, client *kubernetes.KubernetesIPAM) error {
	logging.Debugf("Beginning delete for ContainerID: %v", args.ContainerID)

	ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
	defer cancel()

	_, err := kubernetes.IPManagement(ctx, types.Deallocate, client.Config, client)
	if err != nil {
		logging.Verbosef("WARNING: Problem deallocating IP: %s", err)
	}

	return nil
}
