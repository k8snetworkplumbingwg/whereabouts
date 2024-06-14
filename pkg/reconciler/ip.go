package reconciler

import (
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
)

func ReconcileIPs(errorChan chan error) {
	logging.Verbosef("starting reconciler run")

	ipReconcileLoop, err := NewReconcileLooper()
	if err != nil {
		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
		errorChan <- err
		return
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools()
	if err != nil {
		_ = logging.Errorf("failed to clean up IP for allocations: %v", err)
		errorChan <- err
		return
	}

	if len(cleanedUpIps) > 0 {
		logging.Debugf("successfully cleanup IPs: %+v", cleanedUpIps)
	} else {
		logging.Debugf("no IP addresses to cleanup")
	}

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(); err != nil {
		errorChan <- err
		return
	}

	errorChan <- nil
}
