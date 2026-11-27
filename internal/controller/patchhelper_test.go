// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

var _ = Describe("PatchHelper", func() {
	const (
		poolName      = "patchhelper-pool"
		poolNamespace = "default"
	)

	var (
		ctx        context.Context
		fakeClient client.Client
		key        types.NamespacedName
	)

	newPool := func() *whereaboutsv1alpha1.IPPool {
		return &whereaboutsv1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      poolName,
				Namespace: poolNamespace,
			},
			Spec: whereaboutsv1alpha1.IPPoolSpec{
				Range: "10.10.0.0/24",
				Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
					"1": {PodRef: "default/pod-a"},
					"2": {PodRef: "default/pod-b"},
				},
			},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		key = types.NamespacedName{Name: poolName, Namespace: poolNamespace}
		fakeClient = fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
			WithObjects(newPool()).
			Build()
	})

	It("persists an allocation removal when nothing else wrote to the pool", func() {
		var pool whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &pool)).To(Succeed())

		helper, err := NewPatchHelper(&pool, fakeClient)
		Expect(err).NotTo(HaveOccurred())

		removeAllocations(&pool, []string{"1"})
		Expect(helper.Patch(ctx, &pool)).To(Succeed())

		var stored whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &stored)).To(Succeed())
		Expect(stored.Spec.Allocations).NotTo(HaveKey("1"))
		Expect(stored.Spec.Allocations).To(HaveKey("2"))
	})

	It("refuses to drop an allocation when the pool changed since the snapshot", func() {
		var pool whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &pool)).To(Succeed())

		helper, err := NewPatchHelper(&pool, fakeClient)
		Expect(err).NotTo(HaveOccurred())

		// Simulate the CNI plugin reassigning key "1" to a new pod while this
		// reconcile is in flight. This bumps the stored resourceVersion.
		var concurrent whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &concurrent)).To(Succeed())
		concurrent.Spec.Allocations["1"] = whereaboutsv1alpha1.IPAllocation{PodRef: "default/pod-new"}
		Expect(fakeClient.Update(ctx, &concurrent)).To(Succeed())

		// The reconcile still believes key "1" is orphaned.
		removeAllocations(&pool, []string{"1"})
		err = helper.Patch(ctx, &pool)

		Expect(err).To(HaveOccurred())
		Expect(IsConflictError(err)).To(BeTrue())

		// The in-use allocation survives, so the IP is not handed out twice.
		var stored whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &stored)).To(Succeed())
		Expect(stored.Spec.Allocations).To(HaveKey("1"))
		Expect(stored.Spec.Allocations["1"].PodRef).To(Equal("default/pod-new"))
	})

	It("makes no API call when the object is unchanged", func() {
		var pool whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &pool)).To(Succeed())
		rv := pool.ResourceVersion

		helper, err := NewPatchHelper(&pool, fakeClient)
		Expect(err).NotTo(HaveOccurred())
		Expect(helper.HasChanges(&pool)).To(BeFalse())
		Expect(helper.Patch(ctx, &pool)).To(Succeed())

		var stored whereaboutsv1alpha1.IPPool
		Expect(fakeClient.Get(ctx, key, &stored)).To(Succeed())
		Expect(stored.ResourceVersion).To(Equal(rv))
	})
})

var _ = Describe("IsConflictError", func() {
	It("reports false for nil", func() {
		Expect(IsConflictError(nil)).To(BeFalse())
	})

	It("sees a conflict wrapped by fmt.Errorf", func() {
		conflict := apierrors.NewConflict(
			whereaboutsv1alpha1.Resource("ippools"),
			"pool", fmt.Errorf("changed"))
		Expect(IsConflictError(fmt.Errorf("patching spec/metadata: %w", conflict))).To(BeTrue())
	})

	It("sees a conflict inside an aggregate, which has no Unwrap", func() {
		conflict := apierrors.NewConflict(
			whereaboutsv1alpha1.Resource("ippools"),
			"pool", fmt.Errorf("changed"))
		agg := kerrors.NewAggregate([]error{fmt.Errorf("patching spec/metadata: %w", conflict)})
		Expect(apierrors.IsConflict(agg)).To(BeFalse(), "guards the reason this helper exists")
		Expect(IsConflictError(agg)).To(BeTrue())
	})

	It("reports false for an unrelated error", func() {
		Expect(IsConflictError(fmt.Errorf("boom"))).To(BeFalse())
	})
})
