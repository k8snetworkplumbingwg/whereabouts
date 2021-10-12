package main

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	cniversion "github.com/containernetworking/cni/pkg/version"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/allocate"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/version"
)

func main() {
	skel.PluginMain(
		cmdAdd,
		cmdCheck,
		cmdDel,
		cniversion.All,
		fmt.Sprintf("whereabouts %s", version.GetFullVersionWithRuntimeInfo()),
	)
}

func cmdCheck(args *skel.CmdArgs) error {
	// TODO
	return fmt.Errorf("CNI CHECK method is not implemented")
}

func cmdAdd(args *skel.CmdArgs) error {
	ipamConf, confVersion, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		logging.Errorf("IPAM configuration load failed: %s", err)
		return err
	}
	logging.Debugf("ADD - IPAM configuration successfully read: %+v", filterConf(*ipamConf))

	// Initialize our result, and assign DNS & routing.
	result := &current.Result{}
	result.DNS = ipamConf.DNS
	result.Routes = ipamConf.Routes

	logging.Debugf("Beginning IPAM for ContainerID: %v", args.ContainerID)
	var newip net.IPNet

	ctx, cancel := context.WithTimeout(context.Background(), types.AddTimeLimit)
	defer cancel()

	switch ipamConf.Datastore {
	case types.DatastoreETCD:
		newip, err = storage.IPManagementEtcd(ctx, types.Allocate, *ipamConf, args.ContainerID, getPodRef(args.Args))
	case types.DatastoreKubernetes:
		newip, err = kubernetes.IPManagement(ctx, types.Allocate, *ipamConf, args.ContainerID, getPodRef(args.Args))
	}
	if err != nil {
		logging.Errorf("Error at storage engine: %s", err)
		return fmt.Errorf("Error at storage engine: %w", err)
	}

	// Determine if v4 or v6.
	var useVersion string
	if allocate.IsIPv4(newip.IP) {
		useVersion = "4"
	} else {
		useVersion = "6"
	}

	result.IPs = append(result.IPs, &current.IPConfig{
		Version: useVersion,
		Address: newip,
		Gateway: ipamConf.Gateway})

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
	ipamConf, _, err := config.LoadIPAMConfig(args.StdinData, args.Args)
	if err != nil {
		logging.Errorf("IPAM configuration load failed: %s", err)
		return err
	}
	logging.Debugf("DEL - IPAM configuration successfully read: %+v", filterConf(*ipamConf))
	logging.Debugf("Beginning delete for ContainerID: %v", args.ContainerID)

	ctx, cancel := context.WithTimeout(context.Background(), types.DelTimeLimit)
	defer cancel()

	switch ipamConf.Datastore {
	case types.DatastoreETCD:
		_, err = storage.IPManagementEtcd(ctx, types.Deallocate, *ipamConf, args.ContainerID, getPodRef(args.Args))
	case types.DatastoreKubernetes:
		_, err = kubernetes.IPManagement(ctx, types.Deallocate, *ipamConf, args.ContainerID, getPodRef(args.Args))
	}
	if err != nil {
		logging.Verbosef("WARNING: Problem deallocating IP: %s", err)
		// return fmt.Errorf("Error deallocating IP: %s", err)
	}

	return nil
}

func filterConf(conf types.IPAMConfig) types.IPAMConfig {
	new := conf
	new.EtcdPassword = "*********"
	return new
}

// GetPodRef constructs the PodRef string from CNI arguments.
// It returns an empty string, if K8S_POD_NAMESPACE & K8S_POD_NAME arguments are not provided.
func getPodRef(args string) string {
	podNs := ""
	podName := ""

	for _, arg := range strings.Split(args, ";") {
		if strings.HasPrefix(arg, "K8S_POD_NAMESPACE=") {
			podNs = strings.TrimPrefix(arg, "K8S_POD_NAMESPACE=")
		}
		if strings.HasPrefix(arg, "K8S_POD_NAME=") {
			podName = strings.TrimPrefix(arg, "K8S_POD_NAME=")
		}
	}

	if podNs != "" && podName != "" {
		return podNs + "/" + podName
	}
	return ""
}
