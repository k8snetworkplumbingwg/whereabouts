// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(nadv1.AddToScheme(scheme))
	return scheme
}

var _ = Describe("IPPoolReconciler", func() {
	const (
		poolName      = "test-pool"
		poolNamespace = "default"
		poolRange     = "10.0.0.0/24"
		interval      = 30 * time.Second
	)

	var (
		ctx        context.Context
		scheme     *runtime.Scheme
		reconciler *IPPoolReconciler
		req        ctrl.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = newTestScheme()
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: poolNamespace,
				Name:      poolName,
			},
		}
	})

	// buildReconciler creates the reconciler with no feature flags enabled.
	buildReconciler := func(objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
			WithObjects(objs...).
			Build()
		reconciler = &IPPoolReconciler{
			client:            fakeClient,
			recorder:          events.NewFakeRecorder(10),
			reconcileInterval: interval,
		}
	}

	// buildReconcilerWithFlags creates the reconciler with specified feature flags.
	buildReconcilerWithFlags := func(cleanupTerminating, cleanupDisrupted, verifyNetworkStatus bool, objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
			WithObjects(objs...).
			Build()
		reconciler = &IPPoolReconciler{
			client:              fakeClient,
			recorder:            events.NewFakeRecorder(10),
			reconcileInterval:   interval,
			cleanupTerminating:  cleanupTerminating,
			cleanupDisrupted:    cleanupDisrupted,
			verifyNetworkStatus: verifyNetworkStatus,
		}
	}

	// poolWithFinalizer returns an IPPool that already has the cleanup
	// finalizer, simulating a pool that has been reconciled at least once.
	poolWithFinalizer := func(name, ns, cidr string, allocs map[string]whereaboutsv1alpha1.IPAllocation) *whereaboutsv1alpha1.IPPool {
		return &whereaboutsv1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       name,
				Namespace:  ns,
				Finalizers: []string{ippoolFinalizer},
			},
			Spec: whereaboutsv1alpha1.IPPoolSpec{
				Range:       cidr,
				Allocations: allocs,
			},
		}
	}

	Context("when the pool has no allocations", func() {
		It("should requeue with reconcileInterval", func() {
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{})
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))
		})

		It("should populate status with range details and zero allocations", func() {
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{})
			buildReconciler(pool)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Status.FirstIP).To(Equal("10.0.0.1"))
			Expect(updated.Status.LastIP).To(Equal("10.0.0.254"))
			Expect(updated.Status.TotalIPs).To(Equal(int32(254)))
			Expect(updated.Status.UsedIPs).To(Equal(int32(0)))
			Expect(updated.Status.FreeIPs).To(Equal(int32(254)))
			Expect(updated.Status.OrphanedIPs).To(Equal(int32(0)))
			Expect(updated.Status.PendingPods).To(Equal(int32(0)))
			Expect(updated.Status.AllocatedIPs).To(BeEmpty())
		})
	})

	Context("when the pool has a valid pod allocation", func() {
		It("should not remove the allocation", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			// Verify allocation still exists.
			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(HaveKey("1"))
		})

		It("should populate status with correct allocation counts and resolved IPs", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"5": {
					ContainerID: "abc123",
					PodRef:      "default/my-pod",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool, pod)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Status.TotalIPs).To(Equal(int32(254)))
			Expect(updated.Status.UsedIPs).To(Equal(int32(1)))
			Expect(updated.Status.FreeIPs).To(Equal(int32(253)))
			Expect(updated.Status.OrphanedIPs).To(Equal(int32(0)))
			Expect(updated.Status.PendingPods).To(Equal(int32(0)))
			Expect(updated.Status.AllocatedIPs).To(HaveLen(1))
			Expect(updated.Status.AllocatedIPs[0].IP).To(Equal("10.0.0.5"))
			Expect(updated.Status.AllocatedIPs[0].PodRef).To(Equal("default/my-pod"))
			Expect(updated.Status.AllocatedIPs[0].IfName).To(Equal("eth0"))
		})
	})

	Context("when the pool has an orphaned allocation (pod not found)", func() {
		It("should remove the orphaned allocation", func() {
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {
					ContainerID: "abc123",
					PodRef:      "default/missing-pod",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			// Verify allocation was removed.
			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})

		It("should report OrphanedIPs count in status after cleanup", func() {
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"3": {
					ContainerID: "abc1",
					PodRef:      "default/missing-1",
					IfName:      "eth0",
				},
				"7": {
					ContainerID: "abc2",
					PodRef:      "default/missing-2",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			// After cleanup, orphaned allocations are removed from spec so
			// UsedIPs=0, but OrphanedIPs should reflect the 2 cleaned entries.
			Expect(updated.Status.OrphanedIPs).To(Equal(int32(2)))
			Expect(updated.Status.UsedIPs).To(Equal(int32(0)))
			Expect(updated.Status.FreeIPs).To(Equal(int32(254)))
		})
	})

	Context("when the pool has an allocation with invalid podRef format", func() {
		It("should remove the allocation", func() {
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {
					ContainerID: "abc123",
					PodRef:      "invalid-no-slash",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})
	})

	Context("when the pool has an allocation with empty podRef", func() {
		It("should remove the allocation", func() {
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {
					ContainerID: "abc123",
					PodRef:      "",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(interval))

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Spec.Allocations).To(BeEmpty())
		})
	})

	Context("when the pool is not found", func() {
		It("should return no error and no requeue", func() {
			buildReconciler() // no objects

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("when the pool has a pending pod", func() {
		It("should requeue with shorter interval (5s)", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			}
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {
					ContainerID: "abc123",
					PodRef:      "default/pending-pod",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool, pod)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))
		})

		It("should report PendingPods count in status", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			}
			pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
				"1": {
					ContainerID: "abc123",
					PodRef:      "default/pending-pod",
					IfName:      "eth0",
				},
			})
			buildReconciler(pool, pod)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.IPPool
			Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			Expect(updated.Status.PendingPods).To(Equal(int32(1)))
			Expect(updated.Status.UsedIPs).To(Equal(int32(1)))
			Expect(updated.Status.FreeIPs).To(Equal(int32(253)))
		})
	})

	Context("when the pool has an allocation for a pod with DisruptionTarget condition", func() {
		Context("with cleanupDisrupted enabled", func() {
			It("should remove the allocation", func() {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "evicted-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: "DeletionByTaintManager",
							},
						},
					},
				}
				pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
					"1": {
						ContainerID: "abc123",
						PodRef:      "default/evicted-pod",
						IfName:      "eth0",
					},
				})
				buildReconcilerWithFlags(false, true, false, pool, pod)

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(BeEmpty())
			})
		})

		Context("with cleanupDisrupted disabled", func() {
			It("should keep the allocation for a disrupted pod", func() {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "evicted-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: "DeletionByTaintManager",
							},
						},
					},
				}
				pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
					"1": {
						ContainerID: "abc123",
						PodRef:      "default/evicted-pod",
						IfName:      "eth0",
					},
				})
				buildReconciler(pool, pod)

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(HaveLen(1))
				Expect(updated.Spec.Allocations).To(HaveKey("1"))
			})
		})
	})

	// ── Graceful node shutdown / pod termination tests (#550) ────────────────
	Context("when the pool has an allocation for a terminating pod (DeletionTimestamp set)", func() {
		Context("with cleanupTerminating enabled", func() {
			It("should remove the allocation for a pod being gracefully terminated (#550)", func() {
				now := metav1.Now()
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "terminating-pod",
						Namespace:         "default",
						DeletionTimestamp: &now,
						// DeletionTimestamp requires at least one finalizer on the object
						// in the fake client, otherwise the object would already be gone.
						Finalizers: []string{"test.example.com/block-deletion"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
				pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
					"5": {
						ContainerID: "abc123",
						PodRef:      "default/terminating-pod",
						IfName:      "eth0",
					},
				})
				buildReconcilerWithFlags(true, false, false, pool, pod)

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(BeEmpty())
				Expect(updated.Status.OrphanedIPs).To(Equal(int32(1)))
			})

			It("should release IPs from multiple terminating pods during graceful node shutdown", func() {
				now := metav1.Now()
				pod1 := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "node-shutdown-pod-1",
						Namespace:         "default",
						DeletionTimestamp: &now,
						Finalizers:        []string{"test.example.com/block-deletion"},
					},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				}
				pod2 := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "node-shutdown-pod-2",
						Namespace:         "default",
						DeletionTimestamp: &now,
						Finalizers:        []string{"test.example.com/block-deletion"},
					},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				}
				pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
					"3": {ContainerID: "c1", PodRef: "default/node-shutdown-pod-1", IfName: "eth0"},
					"7": {ContainerID: "c2", PodRef: "default/node-shutdown-pod-2", IfName: "eth0"},
				})
				buildReconcilerWithFlags(true, false, false, pool, pod1, pod2)

				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(BeEmpty())
				Expect(updated.Status.OrphanedIPs).To(Equal(int32(2)))
				Expect(updated.Status.UsedIPs).To(Equal(int32(0)))
			})
		})

		Context("with cleanupTerminating disabled (default)", func() {
			It("should keep the allocation for a terminating pod", func() {
				now := metav1.Now()
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "terminating-pod",
						Namespace:         "default",
						DeletionTimestamp: &now,
						Finalizers:        []string{"test.example.com/block-deletion"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
				pool := poolWithFinalizer(poolName, poolNamespace, poolRange, map[string]whereaboutsv1alpha1.IPAllocation{
					"5": {
						ContainerID: "abc123",
						PodRef:      "default/terminating-pod",
						IfName:      "eth0",
					},
				})
				buildReconciler(pool, pod)

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(HaveLen(1))
				Expect(updated.Spec.Allocations).To(HaveKey("5"))
			})
		})
	})
})

var _ = Describe("allocationKeyToIP", func() {
	pool := func(cidr string) *whereaboutsv1alpha1.IPPool {
		return &whereaboutsv1alpha1.IPPool{
			Spec: whereaboutsv1alpha1.IPPoolSpec{Range: cidr},
		}
	}

	It("converts a small IPv4 offset to the correct IP", func() {
		ip := allocationKeyToIP(pool("10.0.0.0/24"), "5")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
	})

	It("converts offset 0 to the network address", func() {
		ip := allocationKeyToIP(pool("192.168.1.0/24"), "0")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("192.168.1.0"))).To(BeTrue())
	})

	It("converts a small IPv6 offset to the correct IP", func() {
		ip := allocationKeyToIP(pool("fd00::/120"), "10")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("fd00::a"))).To(BeTrue())
	})

	It("handles an offset larger than int64 max for IPv6", func() {
		// Offset = 2^63 (9223372036854775808) — would overflow int64 but fits in big.Int.
		ip := allocationKeyToIP(pool("fd00::/64"), "9223372036854775808")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("fd00::8000:0:0:0"))).To(BeTrue())
	})

	It("handles an offset larger than uint64 max for IPv6", func() {
		// Offset = 2^64 (18446744073709551616) — exceeds uint64 entirely, only big.Int works.
		ip := allocationKeyToIP(pool("::/48"), "18446744073709551616")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("::1:0:0:0:0"))).To(BeTrue())
	})

	It("returns nil for a negative offset", func() {
		ip := allocationKeyToIP(pool("10.0.0.0/24"), "-1")
		Expect(ip).To(BeNil())
	})

	It("returns nil for a non-numeric key", func() {
		ip := allocationKeyToIP(pool("10.0.0.0/24"), "notanumber")
		Expect(ip).To(BeNil())
	})

	It("returns nil for an invalid CIDR range", func() {
		ip := allocationKeyToIP(pool("not-a-cidr"), "5")
		Expect(ip).To(BeNil())
	})

	It("returns nil for an empty key", func() {
		ip := allocationKeyToIP(pool("10.0.0.0/24"), "")
		Expect(ip).To(BeNil())
	})
})
