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

// OverlappingRangeValidator validates OverlappingRangeIPReservation resources.
type OverlappingRangeValidator struct{}

var overlappingrangeLog = ctrl.Log.WithName("webhook").WithName("overlappingrange")

var _ admission.Validator[*whereaboutsv1alpha1.OverlappingRangeIPReservation] = &OverlappingRangeValidator{}

// SetupOverlappingRangeWebhook registers the ORIP validating webhook.
func SetupOverlappingRangeWebhook(mgr manager.Manager) error {
	return builder.WebhookManagedBy(mgr, &whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
		WithValidator(&OverlappingRangeValidator{}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-whereabouts-cni-cncf-io-v1alpha1-overlappingrangeipreservation,mutating=false,failurePolicy=Fail,sideEffects=None,groups=whereabouts.cni.cncf.io,resources=overlappingrangeipreservations,verbs=create;update,versions=v1alpha1,name=voverlappingrangeipreservation.whereabouts.cni.cncf.io,admissionReviewVersions=v1

// ValidateCreate validates an OverlappingRangeIPReservation on creation.
func (v *OverlappingRangeValidator) ValidateCreate(_ context.Context, res *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	err := validateOverlappingRange(res)
	if err != nil {
		overlappingrangeLog.Info("rejected", "name", res.Name, "operation", "create", "reason", err.Error())
	}
	recordValidation("overlappingrange", "create", err)
	return nil, err
}

// ValidateUpdate validates an OverlappingRangeIPReservation on update.
// Spec fields are immutable once created.
func (v *OverlappingRangeValidator) ValidateUpdate(_ context.Context, oldRes, res *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	if oldRes == nil {
		// oldRes should always be provided by the API server for update operations.
		// Treat a nil old object as a validation error rather than silently skipping
		// immutability checks.
		err := fmt.Errorf("old object is nil in update operation")
		overlappingrangeLog.Info("rejected", "name", res.Name, "operation", "update", "reason", err.Error())
		recordValidation("overlappingrange", "update", err)
		return nil, err
	}

	if oldRes.Spec.PodRef != res.Spec.PodRef {
		err := fmt.Errorf("spec.podRef is immutable (was %q, requested %q)", oldRes.Spec.PodRef, res.Spec.PodRef)
		overlappingrangeLog.Info("rejected", "name", res.Name, "operation", "update", "reason", err.Error())
		recordValidation("overlappingrange", "update", err)
		return nil, err
	}
	if oldRes.Spec.IfName != res.Spec.IfName {
		err := fmt.Errorf("spec.ifName is immutable (was %q, requested %q)", oldRes.Spec.IfName, res.Spec.IfName)
		overlappingrangeLog.Info("rejected", "name", res.Name, "operation", "update", "reason", err.Error())
		recordValidation("overlappingrange", "update", err)
		return nil, err
	}
	if oldRes.Spec.ContainerID != res.Spec.ContainerID {
		err := fmt.Errorf("spec.containerID is immutable (was %q, requested %q)", oldRes.Spec.ContainerID, res.Spec.ContainerID)
		overlappingrangeLog.Info("rejected", "name", res.Name, "operation", "update", "reason", err.Error())
		recordValidation("overlappingrange", "update", err)
		return nil, err
	}
	err := validateOverlappingRange(res)
	if err != nil {
		overlappingrangeLog.Info("rejected", "name", res.Name, "operation", "update", "reason", err.Error())
	}
	recordValidation("overlappingrange", "update", err)
	return nil, err
}

// ValidateDelete is a no-op.
func (v *OverlappingRangeValidator) ValidateDelete(_ context.Context, _ *whereaboutsv1alpha1.OverlappingRangeIPReservation) (admission.Warnings, error) {
	recordValidation("overlappingrange", "delete", nil)
	return nil, nil
}

func validateOverlappingRange(res *whereaboutsv1alpha1.OverlappingRangeIPReservation) error {
	if err := validation.ValidatePodRef(res.Spec.PodRef, true); err != nil {
		return fmt.Errorf("spec.podRef: %w", err)
	}
	return nil
}
