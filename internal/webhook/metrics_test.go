// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

// getCounterValue reads the current value of a Counter metric.
func getCounterValue(counter interface{ Write(*dto.Metric) error }) float64 {
	var m dto.Metric
	ExpectWithOffset(1, counter.Write(&m)).To(Succeed())
	return m.GetCounter().GetValue()
}

var _ = Describe("Webhook Metrics", func() {
	BeforeEach(func() {
		// Ensure metric deltas are measured from a clean slate per spec.
		webhookValidationTotal.Reset()
	})

	Context("recordValidation", func() {
		It("should increment allowed counter for successful validation", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "allowed"))
			recordValidation("ippool", "create", nil)
			after := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "allowed"))
			Expect(after - before).To(Equal(float64(1)))
		})

		It("should increment rejected counter for failed validation", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "rejected"))
			recordValidation("ippool", "create", fmt.Errorf("test error"))
			after := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "rejected"))
			Expect(after - before).To(Equal(float64(1)))
		})
	})

	Context("IPPool webhook metrics", func() {
		It("should record allowed for valid create", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "allowed"))
			v := &IPPoolValidator{}
			_, err := v.ValidateCreate(context.Background(), &whereaboutsv1alpha1.IPPool{
				Spec: whereaboutsv1alpha1.IPPoolSpec{Range: "10.0.0.0/24"},
			})
			Expect(err).NotTo(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "allowed"))
			Expect(after - before).To(Equal(float64(1)))
		})

		It("should record rejected for invalid create", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "rejected"))
			v := &IPPoolValidator{}
			_, err := v.ValidateCreate(context.Background(), &whereaboutsv1alpha1.IPPool{
				Spec: whereaboutsv1alpha1.IPPoolSpec{Range: "not-a-cidr"},
			})
			Expect(err).To(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "create", "rejected"))
			Expect(after - before).To(Equal(float64(1)))
		})

		It("should record allowed for valid update", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "update", "allowed"))
			v := &IPPoolValidator{}
			_, err := v.ValidateUpdate(context.Background(),
				&whereaboutsv1alpha1.IPPool{Spec: whereaboutsv1alpha1.IPPoolSpec{Range: "10.0.0.0/24"}},
				&whereaboutsv1alpha1.IPPool{Spec: whereaboutsv1alpha1.IPPoolSpec{Range: "10.0.0.0/24"}},
			)
			Expect(err).NotTo(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "update", "allowed"))
			Expect(after - before).To(Equal(float64(1)))
		})

		It("should record allowed for delete", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "delete", "allowed"))
			v := &IPPoolValidator{}
			_, err := v.ValidateDelete(context.Background(), &whereaboutsv1alpha1.IPPool{})
			Expect(err).NotTo(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("ippool", "delete", "allowed"))
			Expect(after - before).To(Equal(float64(1)))
		})
	})

	Context("NodeSlicePool webhook metrics", func() {
		It("should record rejected for invalid create", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("nodeslicepool", "create", "rejected"))
			v := &NodeSlicePoolValidator{}
			_, err := v.ValidateCreate(context.Background(), &whereaboutsv1alpha1.NodeSlicePool{
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{Range: "not-a-cidr", SliceSize: "28"},
			})
			Expect(err).To(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("nodeslicepool", "create", "rejected"))
			Expect(after - before).To(Equal(float64(1)))
		})

		It("should record allowed for valid create", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("nodeslicepool", "create", "allowed"))
			v := &NodeSlicePoolValidator{}
			_, err := v.ValidateCreate(context.Background(), &whereaboutsv1alpha1.NodeSlicePool{
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{Range: "10.0.0.0/16", SliceSize: "28"},
			})
			Expect(err).NotTo(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("nodeslicepool", "create", "allowed"))
			Expect(after - before).To(Equal(float64(1)))
		})
	})

	Context("OverlappingRange webhook metrics", func() {
		It("should record rejected for immutable update", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("overlappingrange", "update", "rejected"))
			v := &OverlappingRangeValidator{}
			_, err := v.ValidateUpdate(context.Background(),
				&whereaboutsv1alpha1.OverlappingRangeIPReservation{
					Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{PodRef: "ns/old"},
				},
				&whereaboutsv1alpha1.OverlappingRangeIPReservation{
					Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{PodRef: "ns/new"},
				},
			)
			Expect(err).To(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("overlappingrange", "update", "rejected"))
			Expect(after - before).To(Equal(float64(1)))
		})

		It("should record allowed for valid create", func() {
			before := getCounterValue(webhookValidationTotal.WithLabelValues("overlappingrange", "create", "allowed"))
			v := &OverlappingRangeValidator{}
			_, err := v.ValidateCreate(context.Background(), &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{PodRef: "ns/pod"},
			})
			Expect(err).NotTo(HaveOccurred())
			after := getCounterValue(webhookValidationTotal.WithLabelValues("overlappingrange", "create", "allowed"))
			Expect(after - before).To(Equal(float64(1)))
		})
	})
})
