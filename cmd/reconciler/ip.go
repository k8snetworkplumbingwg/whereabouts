// package main
package reconciler

import (
	"context"
	"errors"
	"time"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
)

const (
	defaultReconcilerTimeout = 30
	// reconcilerTimeout        = flag.Int("timeout", defaultReconcilerTimeout, "the value for a request timeout in seconds.") // what about this is not constant? >:(
)

func InvokeIPReconciler(returnErr chan error) {
	go func() {
		doInvokeIPReconciler(returnErr)
	}()
	select {
	case <-time.After(5 * time.Minute):
		returnErr <- errors.New("ip reconciler timed out")
		return
	case <-returnErr:
		logging.Verbosef("finishing ip reconciler run")
		return
	}
}

// TODO: get ip_test.go working with this - currently idk if it does...
func doInvokeIPReconciler(returnErr chan error) {
	ipReconcileLoop, err := reconciler.NewReconcileLooper(context.Background(), defaultReconcilerTimeout)
	if err != nil {
		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
		returnErr <- err
		return
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools(context.Background())
	if err != nil {
		_ = logging.Errorf("failed to clean up IP for allocations: %v", err)
		returnErr <- err
		return
	}

	if len(cleanedUpIps) > 0 {
		logging.Debugf("successfully cleanup IPs: %+v", cleanedUpIps)
	} else {
		logging.Debugf("no IP addresses to cleanup")
	}

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(context.Background()); err != nil {
		returnErr <- err
		return
	}

	logging.Verbosef("no errors with ip reconciler...returning in a sec")
	returnErr <- err
	return
}

// i think this function will get deleted
// func main() {
// 	kubeConfigFile := flag.String("kubeconfig", "", "the path to the Kubernetes configuration file")
// 	logLevel := flag.String("log-level", "error", "the logging level for the `ip-reconciler` app. Valid values are: \"debug\", \"verbose\", \"error\", and \"panic\".")
// 	reconcilerTimeout := flag.Int("timeout", defaultReconcilerTimeout, "the value for a request timeout in seconds.")
// 	flag.Parse()

// 	logging.SetLogLevel(*logLevel)

// 	var err error
// 	var ipReconcileLoop *reconciler.ReconcileLooper
// 	if kubeConfigFile == nil {
// 		ipReconcileLoop, err = reconciler.NewReconcileLooper(context.Background(), *reconcilerTimeout)
// 	} else {
// 		ipReconcileLoop, err = reconciler.NewReconcileLooperWithKubeconfig(context.Background(), *kubeConfigFile, *reconcilerTimeout)
// 	}
// 	if err != nil {
// 		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
// 		os.Exit(couldNotStartOrphanedIPMonitor)
// 	}

// 	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools(context.Background())
// 	if err != nil {
// 		_ = logging.Errorf("failed to clean up IP for allocations: %v", err)
// 		os.Exit(failedToReconcileIPPools)
// 	}
// 	if len(cleanedUpIps) > 0 {
// 		logging.Debugf("successfully cleanup IPs: %+v", cleanedUpIps)
// 	} else {
// 		logging.Debugf("no IP addresses to cleanup")
// 	}

// 	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(context.Background()); err != nil {
// 		os.Exit(failedToReconcileClusterWideIPs)
// 	}
// }
