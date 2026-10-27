// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"
	"net"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"

	"github.com/k8snetworkplumbingwg/whereabouts/internal/validation"
)

// NodeSlicePoolValidator validates NodeSlicePool resources.
type NodeSlicePoolValidator struct{}

var nodeslicepoolLog = ctrl.Log.WithName("webhook").WithName("nodeslicepool")

var _ admission.Validator[*whereaboutsv1alpha1.NodeSlicePool] = &NodeSlicePoolValidator{}

// SetupNodeSlicePoolWebhook registers the NodeSlicePool validating webhook.
func SetupNodeSlicePoolWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.NodeSlicePool{}).
		WithValidator(&NodeSlicePoolValidator{}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-nodeslicepool,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=nodeslicepools,verbs=create;update,versions=v1alpha1,name=vnodeslicepool.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates a NodeSlicePool on creation.
func (v *NodeSlicePoolValidator) ValidateCreate(_ context.Context, pool *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	err := validateNodeSlicePool(pool)
	if err != nil {
		nodeslicepoolLog.Info("rejected", "name", pool.Name, "operation", "create", "reason", err.Error())
	}
	recordValidation("nodeslicepool", "create", err)
	return nil, err
}

// ValidateUpdate validates a NodeSlicePool on update.
// spec.range and spec.sliceSize are intentionally mutable: they mirror the
// NetworkAttachmentDefinition and the node-slice reconciler re-slices the pool
// (updatePoolSpec) whenever the NAD changes.
func (v *NodeSlicePoolValidator) ValidateUpdate(_ context.Context, _, pool *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	err := validateNodeSlicePool(pool)
	if err != nil {
		nodeslicepoolLog.Info("rejected", "name", pool.Name, "operation", "update", "reason", err.Error())
	}
	recordValidation("nodeslicepool", "update", err)
	return nil, err
}

// ValidateDelete is a no-op.
func (v *NodeSlicePoolValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	recordValidation("nodeslicepool", "delete", nil)
	return nil, nil
}

func validateNodeSlicePool(pool *whereaboutsv1alpha1.NodeSlicePool) error {
	// Validate Range is a valid CIDR.
	_, ipNet, err := net.ParseCIDR(pool.Spec.Range)
	if err != nil {
		return fmt.Errorf("invalid spec.range: %w", validation.ValidateCIDR(pool.Spec.Range))
	}

	// Validate SliceSize is parseable and fits the range: a slice cannot be
	// larger than the range nor longer than the address family allows.
	sliceSize, err := validation.ValidateSliceSize(pool.Spec.SliceSize)
	if err != nil {
		return fmt.Errorf("invalid spec.sliceSize: %w", err)
	}
	rangePrefix, bits := ipNet.Mask.Size()
	if sliceSize < rangePrefix || sliceSize > bits {
		return fmt.Errorf("invalid spec.sliceSize %q: must be between /%d (the range prefix) and /%d for range %s",
			pool.Spec.SliceSize, rangePrefix, bits, pool.Spec.Range)
	}

	return nil
}
