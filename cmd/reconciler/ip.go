package main

import (
	"context"
	"flag"
	"os"
	"strings"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
)

const defaultReconcilerTimeout = 30

// Returns a constant string array of known errors to match against.
func knownErrors() [3]string {
	return [3]string{
		// We ignore timeout errors, as they may be transient.
		"timeout",
		// Context deadline exceeded is allowed to happen
		"context deadline exceeded",
		// When a connection is refused, we can just bail out on this run.
		"connection refused",
	}
}

// Matches known error cases from which the process should exit cleanly.
func knownErrorCase(err error) bool {

	knownerrs := knownErrors()

	for _, knownerr := range knownerrs {
		if strings.Contains(err.Error(), knownerr) {
			logging.Verbosef("%s [matches known error] ignored: %v", knownerr, err)
			return true
		}
	}

	return false

}

func tolerateKnownErrorCasesOrFail(err error, errmsg string, errorCodeToReturn int) {
	if knownErrorCase(err) {
		// We exit zero on known error cases to not raise alarms on sensitive systems.
		os.Exit(0)
	} else {
		_ = logging.Errorf("%s: %v", errmsg, err)
		os.Exit(errorCodeToReturn)
	}
}

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
		tolerateKnownErrorCasesOrFail(err, "failed to create the reconcile looper", couldNotStartOrphanedIPMonitor)
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools(context.Background())
	if err != nil {
		tolerateKnownErrorCasesOrFail(err, "failed to clean up IP for allocations", failedToReconcileIPPools)
	}

	if len(cleanedUpIps) > 0 {
		logging.Debugf("successfully cleanup IPs: %+v", cleanedUpIps)
	} else {
		logging.Debugf("no IP addresses to cleanup")
	}

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(context.Background()); err != nil {
		tolerateKnownErrorCasesOrFail(err, "failure to ReconcileOverlappingIPAddresses", failedToReconcileClusterWideIPs)
	}
}
