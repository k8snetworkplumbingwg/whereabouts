package reconciler

import (
	"context"
	"flag"
	"os"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
)

const defaultReconcilerTimeout = 30

func main() {
	kubeConfigFile := flag.String("kubeconfig", "", "the path to the Kubernetes configuration file")
	logLevel := flag.String("log-level", "error", "the logging level for the `ip-reconciler` app. Valid values are: \"debug\", \"verbose\", \"error\", and \"panic\".")
	reconcilerTimeout := flag.Int("timeout", defaultReconcilerTimeout, "the value for a request timeout in seconds.")
	flag.Parse()

	logging.SetLogLevel(*logLevel)

	var err error
	var ipReconcileLoop *reconciler.ReconcileLooper
	if kubeConfigFile == nil {
		ipReconcileLoop, err = reconciler.NewReconcileLooper(context.Background(), *reconcilerTimeout)
	} else {
		ipReconcileLoop, err = reconciler.NewReconcileLooperWithKubeconfig(context.Background(), *kubeConfigFile, *reconcilerTimeout)
	}
	if err != nil {
		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
		os.Exit(couldNotStartOrphanedIPMonitor)
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools(context.Background())
	if err != nil {
		_ = logging.Errorf("failed to clean up IP for allocations: %v", err)
		os.Exit(failedToReconcileIPPools)
	}
	if len(cleanedUpIps) > 0 {
		logging.Debugf("successfully cleanup IPs: %+v", cleanedUpIps)
	} else {
		logging.Debugf("no IP addresses to cleanup")
	}

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(context.Background()); err != nil {
		os.Exit(failedToReconcileClusterWideIPs)
	}
}
