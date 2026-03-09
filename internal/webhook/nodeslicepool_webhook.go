// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
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
func (v *NodeSlicePoolValidator) ValidateUpdate(_ context.Context, oldPool, pool *whereaboutsv1alpha1.NodeSlicePool) (admission.Warnings, error) {
	var errs []error
	if oldPool != nil {
		if oldPool.Spec.Range != pool.Spec.Range {
			errs = append(errs, fmt.Errorf("spec.range is immutable and cannot be changed (was %q, now %q)", oldPool.Spec.Range, pool.Spec.Range))
		}
		if oldPool.Spec.SliceSize != pool.Spec.SliceSize {
			errs = append(errs, fmt.Errorf("spec.sliceSize is immutable and cannot be changed (was %q, now %q)", oldPool.Spec.SliceSize, pool.Spec.SliceSize))
		}
	}
	if len(errs) > 0 {
		if agg := kerrors.NewAggregate(errs); agg != nil {
			err := fmt.Errorf("immutable field(s) changed: %s", agg)
			nodeslicepoolLog.Info("rejected", "name", pool.Name, "operation", "update", "reason", err.Error())
			recordValidation("nodeslicepool", "update", err)
			return nil, err
		}
	}
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
	if err := validation.ValidateCIDR(pool.Spec.Range); err != nil {
		return fmt.Errorf("invalid spec.range: %w", err)
	}

	// Validate SliceSize is parseable.
	_, err := validation.ValidateSliceSize(pool.Spec.SliceSize)
	if err != nil {
		return fmt.Errorf("invalid spec.sliceSize: %w", err)
	}

	return nil
}
