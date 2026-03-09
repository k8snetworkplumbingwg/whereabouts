// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/k8snetworkplumbingwg/whereabouts/internal/controller"
	"github.com/k8snetworkplumbingwg/whereabouts/internal/webhook"
	"github.com/k8snetworkplumbingwg/whereabouts/internal/webhook/certrotator"
)

// leaderElectionID is the identity used for the controller-manager leader
// election lease. Extracted as a constant so it is easy to locate and update,
// e.g. to avoid conflicts during migration from the old ip-control-loop leader
// lock.
const leaderElectionID = "whereabouts-controller.whereabouts.cni.cncf.io"

func newControllerCommand() *cobra.Command {
	var (
		metricsAddr          string
		healthProbeAddr      string
		leaderElect          bool
		leaderElectNamespace string
		reconcileInterval    time.Duration
		webhookPort          int
		certDir              string
		namespace            string
		webhookServiceName   string
		webhookSecretName    string
		webhookConfigName    string
		cleanupTerminating   bool
		cleanupDisrupted     bool
		verifyNetworkStatus  bool
	)

	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Run leader-elected reconcilers and validating webhooks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger(cmd)
			log := ctrl.Log.WithName("controller")

			cfg, err := ctrl.GetConfig()
			if err != nil {
				return fmt.Errorf("loading kubeconfig: %w", err)
			}

			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme: scheme,
				Metrics: server.Options{
					BindAddress: metricsAddr,
				},
				HealthProbeBindAddress:        healthProbeAddr,
				LeaderElection:                leaderElect,
				LeaderElectionID:              leaderElectionID,
				LeaderElectionNamespace:       leaderElectNamespace,
				LeaderElectionReleaseOnCancel: true,
				// All replicas serve webhooks; only the leader runs reconcilers.
				WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
					Port:    webhookPort,
					CertDir: certDir,
				}),
			})
			if err != nil {
				return err
			}

			// Reconcilers (leader-elected).
			if err := controller.SetupWithManager(mgr, reconcileInterval, controller.ReconcilerOptions{
				CleanupTerminating:  cleanupTerminating,
				CleanupDisrupted:    cleanupDisrupted,
				VerifyNetworkStatus: verifyNetworkStatus,
			}); err != nil {
				return err
			}

			// Certificate rotation for the webhook server.
			certReady := make(chan struct{})
			ctx := cmd.Context()
			if err := certrotator.Enable(ctx, mgr, certrotator.Options{
				Namespace:   namespace,
				CertDir:     certDir,
				DNSName:     fmt.Sprintf("%s.%s.svc", webhookServiceName, namespace),
				SecretName:  webhookSecretName,
				WebhookName: webhookConfigName,
				// CAOrganization defaults to "whereabouts" (see certrotator.Options).
				IsReady: certReady,
			}); err != nil {
				return err
			}

			// Register webhooks after cert bootstrap.
			webhookSetup := webhook.NewSetup(mgr, certReady)
			if err := mgr.Add(webhookSetup); err != nil {
				return err
			}

			if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				return err
			}
			if err := mgr.AddReadyzCheck("readyz", webhookSetup.ReadyCheck()); err != nil {
				return err
			}

			log.Info("starting controller manager")
			return mgr.Start(ctrl.SetupSignalHandler())
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the Prometheus metrics endpoint binds to")
	cmd.Flags().StringVar(&healthProbeAddr, "health-probe-bind-address", ":8081", "Address the health/readiness probes bind to")
	cmd.Flags().BoolVar(&leaderElect, "leader-elect", true, "Enable leader election for the controller manager")
	cmd.Flags().StringVar(&leaderElectNamespace, "leader-elect-namespace", "", "Namespace for leader election lease (when empty, controller-runtime defaults to the manager's in-cluster namespace)")
	cmd.Flags().DurationVar(&reconcileInterval, "reconcile-interval", 30*time.Second, "Interval for periodic reconciliation of IP pools")
	cmd.Flags().IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/var/run/webhook-certs", "Directory for TLS certificates")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace where the operator runs (required for webhook cert DNS)")
	utilruntime.Must(cobra.MarkFlagRequired(cmd.Flags(), "namespace"))
	cmd.Flags().StringVar(&webhookServiceName, "webhook-service-name", "whereabouts-webhook-service", "Name of the webhook Service (used for TLS certificate DNS SAN)")
	cmd.Flags().StringVar(&webhookSecretName, "webhook-secret-name", "whereabouts-webhook-cert", "Name of the Secret storing webhook TLS certificates")
	cmd.Flags().StringVar(&webhookConfigName, "webhook-config-name", "whereabouts-validating-webhook-configuration", "Name of the ValidatingWebhookConfiguration to inject CA into")
	cmd.Flags().BoolVar(&cleanupTerminating, "cleanup-terminating-pods", false, "Treat terminating pods (DeletionTimestamp set) as orphaned and release their IPs immediately")
	cmd.Flags().BoolVar(&cleanupDisrupted, "cleanup-disrupted-pods", true, "Treat pods with DisruptionTarget condition (taint-manager eviction) as orphaned")
	cmd.Flags().BoolVar(&verifyNetworkStatus, "verify-network-status", true, "Verify allocated IPs against Multus network-status annotation on pods")

	return cmd
}
