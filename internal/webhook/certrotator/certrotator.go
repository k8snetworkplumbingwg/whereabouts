// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package certrotator wraps cert-controller/pkg/rotator to bootstrap and
// auto-rotate self-signed TLS certificates for the webhook server.
package certrotator

import (
	"context"
	"fmt"

	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Options configures the certificate rotator.
type Options struct {
	// Namespace where the webhook secret and service live.
	Namespace string
	// CertDir is the directory to write TLS cert/key files to.
	CertDir string
	// DNSName is the SAN for the generated certificate.
	DNSName string
	// SecretName is the Kubernetes Secret holding the TLS cert/key pair.
	SecretName string
	// WebhookName is the ValidatingWebhookConfiguration resource name.
	WebhookName string
	// CAOrganization is the Organization field in the generated CA certificate.
	// Defaults to "whereabouts" if empty.
	CAOrganization string
	// IsReady is closed when the initial certificate has been provisioned.
	IsReady chan struct{}
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;update;patch

// ensureSecret creates the TLS secret if it does not already exist.
// cert-controller's refreshCertIfNeeded reads the secret with Get and its
// writeSecret uses Update (not Create), so the secret must exist before the
// rotator starts.
func ensureSecret(ctx context.Context, c client.Client, key types.NamespacedName) error {
	var existing corev1.Secret
	err := c.Get(ctx, key, &existing)
	if err == nil {
		return nil // already exists
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       {},
			corev1.TLSPrivateKeyKey: {},
		},
	}
	err = c.Create(ctx, secret)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// Enable adds a certificate rotator runnable to the manager.
func Enable(ctx context.Context, mgr manager.Manager, opts Options) error {
	log := ctrl.Log.WithName("certrotator")
	log.Info("enabling certificate rotation",
		"namespace", opts.Namespace,
		"secret", opts.SecretName,
		"webhookConfig", opts.WebhookName,
	)

	secretKey := types.NamespacedName{
		Namespace: opts.Namespace,
		Name:      opts.SecretName,
	}

	// Build a direct (non-cached) client because the manager cache is not
	// running yet at setup time.
	directClient, err := client.New(mgr.GetConfig(), client.Options{})
	if err != nil {
		return fmt.Errorf("creating direct client for secret bootstrap: %w", err)
	}

	// Ensure the secret exists before the rotator starts, because
	// cert-controller only updates existing secrets (never creates them).
	if err := ensureSecret(ctx, directClient, secretKey); err != nil {
		return err
	}
	log.Info("TLS secret ensured", "secret", secretKey)

	return rotator.AddRotator(mgr, &rotator.CertRotator{
		SecretKey:      secretKey,
		CertDir:        opts.CertDir,
		CAName:         "whereabouts-ca",
		CAOrganization: caOrg(opts.CAOrganization),
		DNSName:        opts.DNSName,
		IsReady:        opts.IsReady,
		Webhooks: []rotator.WebhookInfo{
			{
				Name: opts.WebhookName,
				Type: rotator.Validating,
			},
		},
		RequireLeaderElection:  false,
		RestartOnSecretRefresh: true,
	})
}

// caOrg returns org if non-empty, otherwise "whereabouts".
func caOrg(org string) string {
	if org != "" {
		return org
	}
	return "whereabouts"
}
