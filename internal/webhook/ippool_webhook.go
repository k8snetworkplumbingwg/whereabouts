// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"

	"github.com/k8snetworkplumbingwg/whereabouts/internal/validation"
)

// IPPoolValidator validates IPPool resources.
type IPPoolValidator struct{}

var ippoolLog = ctrl.Log.WithName("webhook").WithName("ippool")

var _ admission.Validator[*whereaboutsv1alpha1.IPPool] = &IPPoolValidator{}

// SetupIPPoolWebhook registers the IPPool validating webhook with the manager.
func SetupIPPoolWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.IPPool{}).
		WithValidator(&IPPoolValidator{}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-ippool,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=ippools,verbs=create;update,versions=v1alpha1,name=vippool.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates an IPPool on creation.
func (v *IPPoolValidator) ValidateCreate(_ context.Context, pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	w, err := validateIPPool(pool)
	if err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "create", "reason", err.Error())
	}
	recordValidation("ippool", "create", err)
	return w, err
}

// ValidateUpdate validates an IPPool on update.
func (v *IPPoolValidator) ValidateUpdate(_ context.Context, oldPool, pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	if err := validateRangeChange(oldPool, pool); err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "update", "reason", err.Error())
		recordValidation("ippool", "update", err)
		return nil, err
	}
	w, err := validateIPPool(pool)
	if err != nil {
		ippoolLog.Info("rejected", "name", pool.Name, "operation", "update", "reason", err.Error())
	}
	recordValidation("ippool", "update", err)
	return w, err
}

// ValidateDelete is a no-op — deletes are always allowed.
func (v *IPPoolValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	recordValidation("ippool", "delete", nil)
	return nil, nil
}

// validateRangeChange rejects spec.range changes that would remap existing
// allocations. Allocations are stored as offsets from the IP in spec.range, so
// a change is only safe when the pool is empty, or when the base IP stays the
// same and the new range contains the old one (pure expansion).
func validateRangeChange(oldPool, pool *whereaboutsv1alpha1.IPPool) error {
	if oldPool == nil || oldPool.Spec.Range == pool.Spec.Range || len(pool.Spec.Allocations) == 0 {
		return nil
	}
	oldIP, oldNet, err := oldPool.ParseCIDR()
	if err != nil {
		// The stored range is unparsable; nothing meaningful to preserve.
		return nil //nolint:nilerr // let validateIPPool judge the new range
	}
	newIP, newNet, err := pool.ParseCIDR()
	if err != nil {
		return nil //nolint:nilerr // validateIPPool reports the invalid range
	}
	oldOnes, _ := oldNet.Mask.Size()
	newOnes, _ := newNet.Mask.Size()
	if !oldIP.Equal(newIP) || newOnes > oldOnes || !newNet.Contains(oldNet.IP) {
		return fmt.Errorf("spec.range cannot change from %q to %q while the pool has %d allocation(s): "+
			"allocations are stored as offsets from the range IP, so only an expansion that keeps the same base IP is allowed",
			oldPool.Spec.Range, pool.Spec.Range, len(pool.Spec.Allocations))
	}
	return nil
}

func validateIPPool(pool *whereaboutsv1alpha1.IPPool) (admission.Warnings, error) {
	var warnings admission.Warnings

	// Validate Range is a valid CIDR.
	if err := validation.ValidateCIDR(pool.Spec.Range); err != nil {
		return nil, fmt.Errorf("invalid spec.range: %w", err)
	}

	// Validate allocation podRefs.
	for key, alloc := range pool.Spec.Allocations {
		if alloc.PodRef == "" {
			warnings = append(warnings, fmt.Sprintf("allocation %s has empty podRef", key))
			continue
		}
		if err := validation.ValidatePodRef(alloc.PodRef, false); err != nil {
			return nil, fmt.Errorf("allocation %s: %w", key, err)
		}
	}

	return warnings, nil
}
