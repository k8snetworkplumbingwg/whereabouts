// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

var _ = Describe("OverlappingRangeValidator", func() {
	var (
		ctx       context.Context
		validator *OverlappingRangeValidator
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = &OverlappingRangeValidator{}
	})

	Context("ValidateCreate", func() {
		It("should accept a valid OverlappingRangeIPReservation", func() {
			res := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			warnings, err := validator.ValidateCreate(ctx, res)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should reject an ORIP with empty podRef", func() {
			res := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "",
					IfName:      "eth0",
				},
			}
			_, err := validator.ValidateCreate(ctx, res)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("podRef is required"))
		})

		It("should reject an ORIP with invalid podRef format", func() {
			res := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "invalid-no-slash",
					IfName:      "eth0",
				},
			}
			_, err := validator.ValidateCreate(ctx, res)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("namespace/name format"))
		})
	})

	Context("ValidateUpdate", func() {
		It("should accept an update with identical spec", func() {
			oldRes := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			newRes := oldRes.DeepCopy()
			warnings, err := validator.ValidateUpdate(ctx, oldRes, newRes)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should reject a PodRef change", func() {
			oldRes := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			newRes := oldRes.DeepCopy()
			newRes.Spec.PodRef = "default/other-pod"
			_, err := validator.ValidateUpdate(ctx, oldRes, newRes)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.podRef is immutable"))
		})

		It("should reject an IfName change", func() {
			oldRes := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			newRes := oldRes.DeepCopy()
			newRes.Spec.IfName = "net1"
			_, err := validator.ValidateUpdate(ctx, oldRes, newRes)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.ifName is immutable"))
		})

		It("should reject a ContainerID change", func() {
			oldRes := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			newRes := oldRes.DeepCopy()
			newRes.Spec.ContainerID = "def456"
			_, err := validator.ValidateUpdate(ctx, oldRes, newRes)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.containerID is immutable"))
		})

		It("should reject update when oldRes is nil", func() {
			newRes := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			_, err := validator.ValidateUpdate(ctx, nil, newRes)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("old object is nil"))
		})
	})

	Context("ValidateDelete", func() {
		It("should always succeed", func() {
			res := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			warnings, err := validator.ValidateDelete(ctx, res)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})
})
