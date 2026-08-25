// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

// getGaugeValue reads the current value of a Gauge with the given label.
func getGaugeValue(gauge interface{ Write(*dto.Metric) error }) float64 {
	var m dto.Metric
	ExpectWithOffset(1, gauge.Write(&m)).To(Succeed())
	return m.GetGauge().GetValue()
}

// getCounterValue reads the current value of a Counter with the given label.
func getCounterValue(counter interface{ Write(*dto.Metric) error }) float64 {
	var m dto.Metric
	ExpectWithOffset(1, counter.Write(&m)).To(Succeed())
	return m.GetCounter().GetValue()
}

var _ = Describe("Controller Metrics", func() {
	Context("IPPool reconciler metrics", func() {
		It("should report allocation count gauge", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pool",
					Namespace:  "default",
					Finalizers: []string{ippoolFinalizer},
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.0.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {PodRef: "default/pod-a", IfName: "eth0"},
						"2": {PodRef: "default/pod-b", IfName: "eth0"},
						"3": {PodRef: "default/pod-c", IfName: "eth0"},
					},
				},
			}
			podA := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			podB := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-b", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			podC := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-c", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool, podA, podB, podC).
				Build()

			r := &IPPoolReconciler{client: c, recorder: events.NewFakeRecorder(10), reconcileInterval: 30}
			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "test-pool", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			gauge := ippoolAllocationsGauge.WithLabelValues("test-pool")
			Expect(getGaugeValue(gauge)).To(Equal(float64(3)))
		})

		It("should increment orphan cleanup counter", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "orphan-pool",
					Namespace:  "default",
					Finalizers: []string{ippoolFinalizer},
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.1.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {PodRef: "default/existing-pod", IfName: "eth0"},
						"2": {PodRef: "default/gone-pod", IfName: "eth0"},
					},
				},
			}
			existingPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "existing-pod", Namespace: "default"},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			}

			c := fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool, existingPod).
				Build()

			// Record baseline counter value.
			counterBefore := getCounterValue(ippoolOrphansCleaned.WithLabelValues("orphan-pool"))

			r := &IPPoolReconciler{client: c, recorder: events.NewFakeRecorder(10), reconcileInterval: 30}
			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "orphan-pool", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			counterAfter := getCounterValue(ippoolOrphansCleaned.WithLabelValues("orphan-pool"))
			Expect(counterAfter - counterBefore).To(Equal(float64(1)))
		})

		It("should update allocation gauge after cleanup", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "gauge-update-pool",
					Namespace:  "default",
					Finalizers: []string{ippoolFinalizer},
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: "10.0.2.0/24",
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {PodRef: "default/alive", IfName: "eth0"},
						"2": {PodRef: "default/dead", IfName: "eth0"},
					},
				},
			}
			alivePod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "alive", Namespace: "default"},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			}

			c := fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool, alivePod).
				Build()

			r := &IPPoolReconciler{client: c, recorder: events.NewFakeRecorder(10), reconcileInterval: 30}
			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "gauge-update-pool", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// After cleanup, "dead" should be removed. The gauge should reflect
			// the cleaned-up state (1 remaining allocation).
			gauge := ippoolAllocationsGauge.WithLabelValues("gauge-update-pool")
			Expect(getGaugeValue(gauge)).To(Equal(float64(1)))
		})
	})

	Context("OverlappingRange reconciler metrics", func() {
		It("should increment cleaned counter when deleting orphaned reservation", func() {
			res := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.1",
					Namespace: "default",
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					PodRef: "default/gone-pod",
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithObjects(res).
				Build()

			counterBefore := getCounterValue(overlappingReservationsCleaned)

			r := &OverlappingRangeReconciler{client: c, recorder: events.NewFakeRecorder(10), reconcileInterval: 30}
			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "10.0.0.1", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			counterAfter := getCounterValue(overlappingReservationsCleaned)
			Expect(counterAfter - counterBefore).To(Equal(float64(1)))
		})
	})

	Context("NodeSlice helper metrics", func() {
		It("should report node slice pool metrics", func() {
			allocations := []whereaboutsv1alpha1.NodeSliceAllocation{
				{SliceRange: "10.0.0.0/28", NodeName: "node-1"},
				{SliceRange: "10.0.0.16/28", NodeName: "node-2"},
				{SliceRange: "10.0.0.32/28", NodeName: ""},
				{SliceRange: "10.0.0.48/28", NodeName: ""},
			}

			recordNodeSliceMetrics("test-nsp", allocations)

			slicesGauge := nodesliceSlicesGauge.WithLabelValues("test-nsp")
			nodesGauge := nodesliceNodesGauge.WithLabelValues("test-nsp")
			Expect(getGaugeValue(slicesGauge)).To(Equal(float64(4)))
			Expect(getGaugeValue(nodesGauge)).To(Equal(float64(2)))
		})
	})
})
