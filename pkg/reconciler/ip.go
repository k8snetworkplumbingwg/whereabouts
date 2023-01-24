package reconciler

import (
	"context"
	"time"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
)

const (
	defaultReconcilerTimeout = 30
)

func ReconcileIPs(errorChan chan error) {
	logging.Verbosef("starting reconciler run")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultReconcilerTimeout*time.Second))
	defer cancel()

	ipReconcileLoop, err := NewReconcileLooper(ctx, defaultReconcilerTimeout)
	if err != nil {
		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
		errorChan <- err
		return
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools(ctx)
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

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(ctx); err != nil {
		errorChan <- err
		return
	}

	errorChan <- nil
}
