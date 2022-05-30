// package main
package reconciler

import (
	"context"
	"time"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
)

const (
	defaultReconcilerTimeout = 30
)

// TODO: get ip_test.go working with this - currently idk if it does...
func InvokeIPReconciler(returnErr chan error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultReconcilerTimeout*time.Second))
	defer cancel()

	ipReconcileLoop, err := NewReconcileLooper(ctx, defaultReconcilerTimeout)
	if err != nil {
		_ = logging.Errorf("failed to create the reconcile looper: %v", err)
		returnErr <- err
		return
	}

	cleanedUpIps, err := ipReconcileLoop.ReconcileIPPools(ctx)
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

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(ctx); err != nil {
		returnErr <- err
		return
	}

	logging.Verbosef("no errors with ip reconciler...returning in a sec")
	returnErr <- err
	return
}
