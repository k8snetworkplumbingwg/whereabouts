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

var _ = Describe("NodeSlicePoolValidator", func() {
	var (
		ctx       context.Context
		validator *NodeSlicePoolValidator
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = &NodeSlicePoolValidator{}
	})

	Context("ValidateCreate", func() {
		It("should accept a valid NodeSlicePool", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
			}
			warnings, err := validator.ValidateCreate(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should reject a NodeSlicePool with empty range", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "",
					SliceSize: "/24",
				},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CIDR is required"))
		})

		It("should reject a NodeSlicePool with invalid range", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "not-a-cidr",
					SliceSize: "/24",
				},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid spec.range"))
		})

		It("should reject a NodeSlicePool with empty sliceSize", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "",
				},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("sliceSize is required"))
		})

		It("should reject a NodeSlicePool with invalid sliceSize", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "abc",
				},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid spec.sliceSize"))
		})

		It("should accept a NodeSlicePool with '/24' format", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
			}
			warnings, err := validator.ValidateCreate(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should accept a NodeSlicePool with '24' format (no slash)", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "24",
				},
			}
			warnings, err := validator.ValidateCreate(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})

	Context("ValidateUpdate", func() {
		It("should accept a valid update with same spec", func() {
			oldPool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
			}
			newPool := oldPool.DeepCopy()
			warnings, err := validator.ValidateUpdate(ctx, oldPool, newPool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should allow the reconciler to re-slice (range and sliceSize change)", func() {
			oldPool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
				Spec:       whereaboutsv1alpha1.NodeSlicePoolSpec{Range: "10.0.0.0/16", SliceSize: "/24"},
			}
			newPool := oldPool.DeepCopy()
			newPool.Spec.Range = "10.1.0.0/16"
			newPool.Spec.SliceSize = "/28"
			_, err := validator.ValidateUpdate(ctx, oldPool, newPool)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject an update with an invalid range", func() {
			oldPool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
				Spec:       whereaboutsv1alpha1.NodeSlicePoolSpec{Range: "10.0.0.0/16", SliceSize: "/24"},
			}
			newPool := oldPool.DeepCopy()
			newPool.Spec.Range = "not-a-cidr"
			_, err := validator.ValidateUpdate(ctx, oldPool, newPool)
			Expect(err).To(MatchError(ContainSubstring("invalid spec.range")))
		})
	})

	DescribeTable("sliceSize vs range",
		func(rng, slice string, wantErr bool) {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
				Spec:       whereaboutsv1alpha1.NodeSlicePoolSpec{Range: rng, SliceSize: slice},
			}
			_, err := validator.ValidateCreate(ctx, pool)
			if wantErr {
				Expect(err).To(MatchError(ContainSubstring("invalid spec.sliceSize")))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("slice equal to range", "10.0.0.0/24", "/24", false),
		Entry("slice larger than range", "10.0.0.0/24", "/16", true),
		Entry("slice beyond IPv4 bits", "10.0.0.0/16", "/33", true),
		Entry("IPv6 slice", "fd00::/48", "/64", false),
		Entry("IPv6 slice beyond bits", "fd00::/48", "/129", true),
	)

	Context("ValidateDelete", func() {
		It("should always succeed", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
			}
			warnings, err := validator.ValidateDelete(ctx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})
})
