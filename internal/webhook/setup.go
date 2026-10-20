// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package webhook provides validating admission webhooks for Whereabouts CRDs.
package webhook

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Setup is a manager.Runnable that waits for certs to be ready before
// registering validating webhooks. Use ReadyCheck as the readyz checker
// to block readiness until webhooks are fully registered.
type Setup struct {
	mgr       manager.Manager
	certReady <-chan struct{}
	ready     atomic.Bool
}

// NewSetup returns a Runnable that registers validating webhooks after the
// certificate is provisioned.
func NewSetup(mgr manager.Manager, certReady <-chan struct{}) *Setup {
	return &Setup{mgr: mgr, certReady: certReady}
}

// Start blocks until certs are ready, then registers webhooks.
func (s *Setup) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("webhook-setup")

	// Wait for cert-controller to signal readiness.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.certReady:
		log.Info("certificates ready, registering webhooks")
	}

	if err := SetupIPPoolWebhook(s.mgr); err != nil {
		return fmt.Errorf("registering IPPool webhook: %w", err)
	}
	if err := SetupNodeSlicePoolWebhook(s.mgr); err != nil {
		return fmt.Errorf("registering NodeSlicePool webhook: %w", err)
	}
	if err := SetupOverlappingRangeWebhook(s.mgr); err != nil {
		return fmt.Errorf("registering OverlappingRangeIPReservation webhook: %w", err)
	}

	s.ready.Store(true)
	log.Info("webhooks registered")

	// Block until the manager is stopped.
	<-ctx.Done()
	return nil
}

// NeedLeaderElection implements the LeaderElectionRunnable interface.
// Webhooks must serve on every replica, not just the leader.
func (s *Setup) NeedLeaderElection() bool { return false }

// ReadyCheck returns a healthz.Checker that reports ready only after webhooks
// have been registered (not just after certs are provisioned).
func (s *Setup) ReadyCheck() healthz.Checker {
	return func(_ *http.Request) error {
		if s.ready.Load() {
			return nil
		}
		return fmt.Errorf("webhooks not yet registered: waiting for TLS certificate provisioning or webhook registration to complete")
	}
}
