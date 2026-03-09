// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

var _ = Describe("OverlappingRangeReconciler", func() {
	const (
		resName      = "test-reservation"
		resNamespace = "default"
		interval     = 30 * time.Second
	)

	var (
		ctx        context.Context
		reconciler *OverlappingRangeReconciler
		req        ctrl.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: resNamespace,
				Name:      resName,
			},
		}
	})

	buildReconciler := func(objs ...client.Object) {
		scheme := newTestScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
			WithObjects(objs...).
			Build()
		fakeRecorder := events.NewFakeRecorder(10)
		go func() {
			for event := range fakeRecorder.Events {
				_ = event
			}
		}()
		reconciler = &OverlappingRangeReconciler{
			client:            fakeClient,
			recorder:          fakeRecorder,
			reconcileInterval: interval,
		}
	}

	Context("when the reservation's pod exists", func() {
		It("should not delete the reservation and requeue", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			// Verify reservation still exists.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		})
	})

	Context("when the reservation's pod is missing", func() {
		It("should delete the reservation", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "default/missing-pod",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Verify reservation was deleted.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})
	})

	Context("when the reservation is not found", func() {
		It("should return no error and no requeue", func() {
			buildReconciler() // no objects

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("when the reservation has an invalid podRef", func() {
		It("should delete the reservation", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "invalid-no-slash",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Verify reservation was deleted.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, req.NamespacedName, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})
	})

	Context("when the reservation has an empty podRef", func() {
		It("should requeue with reconcileInterval", func() {
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resName,
					Namespace: resNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					ContainerID: "abc123",
					PodRef:      "",
					IfName:      "eth0",
				},
			}
			buildReconciler(reservation)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))
		})
	})
})
