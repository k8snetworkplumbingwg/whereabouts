package main

import (
	"context"
	"flag"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/storage"
	"os"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
)

func main() {
	kubeConfigFile := flag.String("kubeconfig", "", "the path to the Kubernetes configuration file")
	flag.Parse()

	if *kubeConfigFile == "" {
		_ = logging.Errorf("must specify the kubernetes config file, via the '-kubeconfig' flag")
		os.Exit(kubeconfigNotFound)
	}

	ctx, cancel := context.WithTimeout(context.Background(), storage.RequestTimeout)
	defer cancel()
	ipReconcileLoop, err := reconciler.NewReconcileLooper(*kubeConfigFile, ctx)
	if err != nil {
		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
		os.Exit(couldNotStartOrphanedIPMonitor)
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools()
	if err != nil {
		_ = logging.Errorf("failed to clean up IP for allocations: %v", err)
		os.Exit(failedToReconcileIPPools)
	}
	if len(cleanedUpIps) > 0 {
		logging.Debugf("successfully cleanup IPs: %+v", cleanedUpIps)
	} else {
		logging.Debugf("no IP addresses to cleanup")
	}

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(); err != nil {
		os.Exit(failedToReconcileClusterWideIPs)
	}
}
