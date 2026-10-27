// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

// ---------------------------------------------------------------
// denormalizeIPName
// ---------------------------------------------------------------
var _ = Describe("denormalizeIPName", func() {
	It("parses a plain IPv4 address", func() {
		ip := denormalizeIPName("10.0.0.5")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
	})

	It("parses a plain IPv6 address with dashes instead of colons", func() {
		ip := denormalizeIPName("fd00--1")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("fd00::1"))).To(BeTrue())
	})

	It("parses a network-name prefixed IPv4", func() {
		ip := denormalizeIPName("mynet-10.0.0.5")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
	})

	It("parses a network-name prefixed IPv6", func() {
		ip := denormalizeIPName("mynet-fd00--1")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("fd00::1"))).To(BeTrue())
	})

	It("parses a multi-dash network-name prefixed IPv4", func() {
		ip := denormalizeIPName("my-fancy-net-10.0.0.1")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("10.0.0.1"))).To(BeTrue())
	})

	It("returns nil for garbage input", func() {
		ip := denormalizeIPName("not-an-ip-at-all")
		Expect(ip).To(BeNil())
	})

	It("returns nil for empty string", func() {
		ip := denormalizeIPName("")
		Expect(ip).To(BeNil())
	})

	It("handles IPv6 full form with dashes", func() {
		// Previously had infinite loop bug - now fixed.
		ip := denormalizeIPName("2001-0db8-0000-0000-0000-0000-0000-0001")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("2001:db8::1"))).To(BeTrue())
	})

	It("handles IPv4 with dots directly (no prefix)", func() {
		ip := denormalizeIPName("192.168.1.100")
		Expect(ip).NotTo(BeNil())
		Expect(ip.Equal(net.ParseIP("192.168.1.100"))).To(BeTrue())
	})
})

// ---------------------------------------------------------------
// parsePodRef
// ---------------------------------------------------------------
var _ = Describe("parsePodRef", func() {
	It("parses valid namespace/name", func() {
		ns, name, ok := parsePodRef("default/my-pod")
		Expect(ok).To(BeTrue())
		Expect(ns).To(Equal("default"))
		Expect(name).To(Equal("my-pod"))
	})

	It("rejects string without slash", func() {
		_, _, ok := parsePodRef("no-slash")
		Expect(ok).To(BeFalse())
	})

	It("rejects empty string", func() {
		_, _, ok := parsePodRef("")
		Expect(ok).To(BeFalse())
	})

	It("rejects /name (empty namespace)", func() {
		_, _, ok := parsePodRef("/my-pod")
		Expect(ok).To(BeFalse())
	})

	It("rejects ns/ (empty name)", func() {
		_, _, ok := parsePodRef("ns/")
		Expect(ok).To(BeFalse())
	})

	It("handles multiple slashes (only splits on first)", func() {
		ns, name, ok := parsePodRef("ns/pod/extra")
		Expect(ok).To(BeTrue())
		Expect(ns).To(Equal("ns"))
		Expect(name).To(Equal("pod/extra"))
	})

	It("rejects just a slash", func() {
		_, _, ok := parsePodRef("/")
		Expect(ok).To(BeFalse())
	})
})

// ---------------------------------------------------------------
// isPodMarkedForDeletion
// ---------------------------------------------------------------
var _ = Describe("isPodMarkedForDeletion", func() {
	It("returns true for DisruptionTarget+True+DeletionByTaintManager", func() {
		conditions := []corev1.PodCondition{
			{
				Type:   corev1.DisruptionTarget,
				Status: corev1.ConditionTrue,
				Reason: "DeletionByTaintManager",
			},
		}
		Expect(isPodMarkedForDeletion(conditions)).To(BeTrue())
	})

	It("returns false for DisruptionTarget+False", func() {
		conditions := []corev1.PodCondition{
			{
				Type:   corev1.DisruptionTarget,
				Status: corev1.ConditionFalse,
				Reason: "DeletionByTaintManager",
			},
		}
		Expect(isPodMarkedForDeletion(conditions)).To(BeFalse())
	})

	It("returns false for DisruptionTarget+True but wrong reason", func() {
		conditions := []corev1.PodCondition{
			{
				Type:   corev1.DisruptionTarget,
				Status: corev1.ConditionTrue,
				Reason: "SomeOtherReason",
			},
		}
		Expect(isPodMarkedForDeletion(conditions)).To(BeFalse())
	})

	It("returns false for wrong condition type", func() {
		conditions := []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
				Reason: "DeletionByTaintManager",
			},
		}
		Expect(isPodMarkedForDeletion(conditions)).To(BeFalse())
	})

	It("returns false for nil conditions", func() {
		Expect(isPodMarkedForDeletion(nil)).To(BeFalse())
	})

	It("returns false for empty conditions", func() {
		Expect(isPodMarkedForDeletion([]corev1.PodCondition{})).To(BeFalse())
	})

	It("returns true when mixed with other conditions", func() {
		conditions := []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			{Type: corev1.DisruptionTarget, Status: corev1.ConditionTrue, Reason: "DeletionByTaintManager"},
			{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
		}
		Expect(isPodMarkedForDeletion(conditions)).To(BeTrue())
	})
})

// ---------------------------------------------------------------
// isPodUsingIP
// ---------------------------------------------------------------
var _ = Describe("isPodUsingIP", func() {
	makeNetworkStatusAnnotation := func(statuses []nadv1.NetworkStatus) string {
		b, _ := json.Marshal(statuses)
		return string(b)
	}

	It("returns true when pod has no network-status annotation (assume valid)", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeTrue())
	})

	It("returns true when annotation is empty", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: ""},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeTrue())
	})

	It("returns true when annotation is malformed JSON", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: "not-json"},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeTrue())
	})

	It("returns true when IP matches a non-default network", func() {
		statuses := []nadv1.NetworkStatus{
			{Name: "default/eth0", Default: true, IPs: []string{"192.168.1.1"}},
			{Name: "default/net1", Default: false, IPs: []string{"10.0.0.1"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: makeNetworkStatusAnnotation(statuses)},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeTrue())
	})

	It("returns false when IP is not in any non-default network", func() {
		statuses := []nadv1.NetworkStatus{
			{Name: "default/eth0", Default: true, IPs: []string{"10.0.0.1"}},
			{Name: "default/net1", Default: false, IPs: []string{"10.0.0.2"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: makeNetworkStatusAnnotation(statuses)},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeFalse())
	})

	It("returns false when IP is only on the default network", func() {
		statuses := []nadv1.NetworkStatus{
			{Name: "default/eth0", Default: true, IPs: []string{"10.0.0.1"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: makeNetworkStatusAnnotation(statuses)},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeFalse())
	})

	It("handles IPv6 comparison using net.IP.Equal", func() {
		statuses := []nadv1.NetworkStatus{
			{Name: "default/net1", Default: false, IPs: []string{"fd00::1"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: makeNetworkStatusAnnotation(statuses)},
			},
		}
		// Use full-form IPv6 to test Equal comparison.
		Expect(isPodUsingIP(pod, net.ParseIP("fd00:0000:0000:0000:0000:0000:0000:0001"))).To(BeTrue())
	})

	It("returns false when there are non-default networks but none match", func() {
		statuses := []nadv1.NetworkStatus{
			{Name: "default/net1", Default: false, IPs: []string{"10.0.0.2", "10.0.0.3"}},
			{Name: "default/net2", Default: false, IPs: []string{"10.0.1.1"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: makeNetworkStatusAnnotation(statuses)},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeFalse())
	})

	It("returns false when annotation is empty array", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: "[]"},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeFalse())
	})

	It("handles unparseable IP in annotation gracefully", func() {
		statuses := []nadv1.NetworkStatus{
			{Name: "default/net1", Default: false, IPs: []string{"not-an-ip", "10.0.0.1"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod1",
				Annotations: map[string]string{nadv1.NetworkStatusAnnot: makeNetworkStatusAnnotation(statuses)},
			},
		}
		Expect(isPodUsingIP(pod, net.ParseIP("10.0.0.1"))).To(BeTrue())
	})
})

// ---------------------------------------------------------------
// IPPoolReconciler - DeletionTimestamp / pool being deleted
// ---------------------------------------------------------------
var _ = Describe("IPPoolReconciler extended", func() {
	const (
		poolName      = "extended-test-pool"
		poolNamespace = "default"
		poolRange     = "10.0.0.0/24"
		interval      = 30 * time.Second
	)

	var (
		ctx        context.Context
		reconciler *IPPoolReconciler
		req        ctrl.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: poolNamespace,
				Name:      poolName,
			},
		}
	})

	buildReconciler := func(objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}, &whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
			WithObjects(objs...).
			Build()
		reconciler = &IPPoolReconciler{
			client:            fakeClient,
			recorder:          events.NewFakeRecorder(10),
			reconcileInterval: interval,
		}
	}

	buildReconcilerWithFlags := func(cleanupTerminating, cleanupDisrupted, verifyNetworkStatus bool, objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}, &whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
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

	Context("when the pool has a DeletionTimestamp (being deleted)", func() {
		It("should return immediately without error", func() {
			now := metav1.Now()
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:              poolName,
					Namespace:         poolNamespace,
					DeletionTimestamp: &now,
					Finalizers:        []string{ippoolFinalizer},
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {PodRef: "default/some-pod", IfName: "eth0"},
					},
				},
			}
			buildReconciler(pool)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("when a pod is running but IP not in network-status annotation", func() {
		Context("with verifyNetworkStatus enabled", func() {
			It("should remove the orphaned allocation", func() {
				statuses := []nadv1.NetworkStatus{
					{Name: "default/net1", Default: false, IPs: []string{"10.0.0.99"}},
				}
				statusJSON, _ := json.Marshal(statuses)

				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mismatch-pod",
						Namespace: "default",
						Annotations: map[string]string{
							nadv1.NetworkStatusAnnot: string(statusJSON),
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
				pool := &whereaboutsv1alpha1.IPPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:       poolName,
						Namespace:  poolNamespace,
						Finalizers: []string{ippoolFinalizer},
					},
					Spec: whereaboutsv1alpha1.IPPoolSpec{
						Range: poolRange,
						Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
							"1": {
								ContainerID: "abc123",
								PodRef:      "default/mismatch-pod",
								IfName:      "eth0",
							},
						},
					},
				}
				buildReconcilerWithFlags(false, false, true, pool, pod)

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				// Verify allocation was removed.
				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(BeEmpty())
			})
		})

		Context("with verifyNetworkStatus disabled", func() {
			It("should keep the allocation despite IP mismatch", func() {
				statuses := []nadv1.NetworkStatus{
					{Name: "default/net1", Default: false, IPs: []string{"10.0.0.99"}},
				}
				statusJSON, _ := json.Marshal(statuses)

				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mismatch-pod",
						Namespace: "default",
						Annotations: map[string]string{
							nadv1.NetworkStatusAnnot: string(statusJSON),
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
				pool := &whereaboutsv1alpha1.IPPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:       poolName,
						Namespace:  poolNamespace,
						Finalizers: []string{ippoolFinalizer},
					},
					Spec: whereaboutsv1alpha1.IPPoolSpec{
						Range: poolRange,
						Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
							"1": {
								ContainerID: "abc123",
								PodRef:      "default/mismatch-pod",
								IfName:      "eth0",
							},
						},
					},
				}
				buildReconciler(pool, pod) // verifyNetworkStatus defaults to false

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				// Verify allocation was kept.
				var updated whereaboutsv1alpha1.IPPool
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
				Expect(updated.Spec.Allocations).To(HaveLen(1))
				Expect(updated.Spec.Allocations).To(HaveKey("1"))
			})
		})
	})

	Context("when pool has mixed valid and orphaned allocations", func() {
		It("should only remove orphaned allocations", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       poolName,
					Namespace:  poolNamespace,
					Finalizers: []string{ippoolFinalizer},
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range: poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
						"1": {PodRef: "default/valid-pod", IfName: "eth0"},
						"2": {PodRef: "default/gone-pod", IfName: "eth0"},
						"3": {PodRef: "", IfName: "eth0"},
						"4": {PodRef: "invalid-no-slash", IfName: "eth0"},
					},
				},
			}
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

	Context("cleanupOverlappingReservations", func() {
		It("should delete matching overlapping reservations", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			// Reservation whose IP matches offset "5" → 10.0.0.5
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.5",
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					PodRef: "default/some-pod",
				},
			}
			buildReconciler(pool, reservation)

			err := reconciler.cleanupOverlappingReservations(ctx, pool, []string{"5"})
			Expect(err).NotTo(HaveOccurred())

			// Verify reservation was deleted.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, types.NamespacedName{
				Namespace: poolNamespace,
				Name:      "10.0.0.5",
			}, &updated)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should handle non-matching reservations gracefully", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			// Reservation at a different IP.
			reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "10.0.0.99",
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					PodRef: "default/some-pod",
				},
			}
			buildReconciler(pool, reservation)

			err := reconciler.cleanupOverlappingReservations(ctx, pool, []string{"5"})
			Expect(err).NotTo(HaveOccurred())

			// Verify reservation still exists.
			var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
			err = reconciler.client.Get(ctx, types.NamespacedName{
				Namespace: poolNamespace,
				Name:      "10.0.0.99",
			}, &updated)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle invalid key (non-numeric) gracefully", func() {
			pool := &whereaboutsv1alpha1.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: poolNamespace,
				},
				Spec: whereaboutsv1alpha1.IPPoolSpec{
					Range:       poolRange,
					Allocations: map[string]whereaboutsv1alpha1.IPAllocation{},
				},
			}
			buildReconciler(pool)

			err := reconciler.cleanupOverlappingReservations(ctx, pool, []string{"not-a-number"})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// ---------------------------------------------------------------
// NodeSliceReconciler extended
// ---------------------------------------------------------------
var _ = Describe("NodeSliceReconciler extended", func() {
	const (
		nadName      = "ext-test-nad"
		nadNamespace = "default"
	)

	var (
		ctx        context.Context
		reconciler *NodeSliceReconciler
		req        ctrl.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: nadNamespace,
				Name:      nadName,
			},
		}
	})

	buildReconciler := func(objs ...client.Object) {
		scheme := newTestScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			WithStatusSubresource(&whereaboutsv1alpha1.NodeSlicePool{}).
			Build()
		reconciler = &NodeSliceReconciler{
			client:   fakeClient,
			recorder: events.NewFakeRecorder(10),
		}
	}

	Context("ensureOwnerRef", func() {
		It("should add OwnerReference when not present", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-new",
				},
			}
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: nadNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{UID: "nad-uid-existing"},
					},
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
			}
			buildReconciler(nad, pool)

			err := reconciler.ensureOwnerRef(ctx, pool, nad)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "test-pool"}, &updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.OwnerReferences).To(HaveLen(2))
		})

		It("should not add duplicate OwnerReference", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-existing",
				},
			}
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool",
					Namespace: nadNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{UID: "nad-uid-existing"},
					},
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
			}
			buildReconciler(nad, pool)

			err := reconciler.ensureOwnerRef(ctx, pool, nad)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "test-pool"}, &updated)
			Expect(err).NotTo(HaveOccurred())
			// Should still be 1 — no duplicate added.
			Expect(updated.OwnerReferences).To(HaveLen(1))
		})
	})

	Context("mapNodeToNADs", func() {
		It("should list all NADs and return reconcile requests", func() {
			nad1 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad1", Namespace: "ns-a"},
			}
			nad2 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad2", Namespace: "ns-b"},
			}
			buildReconciler(nad1, nad2)

			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
			requests := reconciler.mapNodeToNADs(ctx, node)
			Expect(requests).To(HaveLen(2))

			nsNames := map[string]string{}
			for _, r := range requests {
				nsNames[r.Name] = r.Namespace
			}
			Expect(nsNames).To(HaveKeyWithValue("nad1", "ns-a"))
			Expect(nsNames).To(HaveKeyWithValue("nad2", "ns-b"))
		})

		It("should return empty when no NADs exist", func() {
			buildReconciler()
			requests := reconciler.mapNodeToNADs(ctx, &corev1.Node{})
			Expect(requests).To(BeEmpty())
		})
	})

	Context("checkMultiNADMismatch", func() {
		It("should pass when matching NADs have same config", func() {
			nad1 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad1", Namespace: nadNamespace, UID: "uid-1"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("shared-net", "10.0.0.0/16", "/24"),
				},
			}
			nad2 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad2", Namespace: nadNamespace, UID: "uid-2"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("shared-net", "10.0.0.0/16", "/24"),
				},
			}
			buildReconciler(nad1, nad2)

			conf := &nadIPAMConfig{
				Name:          "testnet",
				NetworkName:   "shared-net",
				Range:         "10.0.0.0/16",
				NodeSliceSize: "/24",
			}
			err := reconciler.checkMultiNADMismatch(ctx, nad1, conf)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail when NADs share network name but differ in range", func() {
			nad1 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad1", Namespace: nadNamespace, UID: "uid-1"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("shared-net", "10.0.0.0/16", "/24"),
				},
			}
			nad2 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad2", Namespace: nadNamespace, UID: "uid-2"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("shared-net", "10.1.0.0/16", "/24"),
				},
			}
			buildReconciler(nad1, nad2)

			conf := &nadIPAMConfig{
				Name:          "testnet",
				NetworkName:   "shared-net",
				Range:         "10.0.0.0/16",
				NodeSliceSize: "/24",
			}
			err := reconciler.checkMultiNADMismatch(ctx, nad1, conf)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mismatched"))
		})

		It("should not compare NADs with different network names", func() {
			nad1 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad1", Namespace: nadNamespace, UID: "uid-1"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("net-a", "10.0.0.0/16", "/24"),
				},
			}
			nad2 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad2", Namespace: nadNamespace, UID: "uid-2"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("net-b", "10.1.0.0/16", "/28"),
				},
			}
			buildReconciler(nad1, nad2)

			conf := &nadIPAMConfig{
				Name:          "testnet",
				NetworkName:   "net-a",
				Range:         "10.0.0.0/16",
				NodeSliceSize: "/24",
			}
			err := reconciler.checkMultiNADMismatch(ctx, nad1, conf)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should skip NAD with same UID (self)", func() {
			nad1 := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "nad1", Namespace: nadNamespace, UID: "uid-1"},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("shared-net", "10.0.0.0/16", "/24"),
				},
			}
			buildReconciler(nad1)

			conf := &nadIPAMConfig{
				Name:          "testnet",
				NetworkName:   "shared-net",
				Range:         "10.0.0.0/16",
				NodeSliceSize: "/24",
			}
			err := reconciler.checkMultiNADMismatch(ctx, nad1, conf)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("parseNADIPAMConfig edge cases", func() {
		It("returns error for invalid JSON", func() {
			_, err := parseNADIPAMConfig("not-json{{{")
			Expect(err).To(HaveOccurred())
		})

		It("returns error for JSON without whereabouts type", func() {
			_, err := parseNADIPAMConfig(`{"name":"test","ipam":{"type":"calico"}}`)
			Expect(err).To(HaveOccurred())
		})

		It("parses conflist where whereabouts is not first plugin", func() {
			config := `{"name":"mynet","plugins":[{"type":"bridge","ipam":{"type":"host-local"}},{"type":"tuning","ipam":{"type":"whereabouts","range":"10.0.0.0/24","node_slice_size":"/28"}}]}`
			conf, err := parseNADIPAMConfig(config)
			Expect(err).NotTo(HaveOccurred())
			Expect(conf.Range).To(Equal("10.0.0.0/24"))
			Expect(conf.NodeSliceSize).To(Equal("/28"))
		})

		It("returns error for conflist with no whereabouts plugin", func() {
			config := `{"name":"mynet","plugins":[{"type":"bridge","ipam":{"type":"host-local"}}]}`
			_, err := parseNADIPAMConfig(config)
			Expect(err).To(HaveOccurred())
		})

		It("parses single plugin config with network_name", func() {
			config := `{"name":"mynet","ipam":{"type":"whereabouts","range":"fd00::/64","node_slice_size":"/80","network_name":"custom"}}`
			conf, err := parseNADIPAMConfig(config)
			Expect(err).NotTo(HaveOccurred())
			Expect(conf.NetworkName).To(Equal("custom"))
			Expect(conf.Name).To(Equal("mynet"))
		})
	})

	Context("ensureNodeAssignments", func() {
		It("should handle pool already full (more nodes than slots)", func() {
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "full-pool",
					Namespace: nadNamespace,
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/24",
					SliceSize: "/28",
				},
				Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
					Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
						{SliceRange: "10.0.0.0/28", NodeName: "node-a"},
					},
				},
			}
			buildReconciler(pool)

			// Two nodes but only one slot.
			nodes := []string{"node-a", "node-b"}
			result, err := reconciler.ensureNodeAssignments(ctx, pool, nodes)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("using network_name for pool name", func() {
		It("should use network_name as pool name when set", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-1",
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("custom-pool-name", "10.0.0.0/16", "/24"),
				},
			}
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			buildReconciler(nad, node)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Pool should be named "custom-pool-name", not "testnet".
			var pool whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "custom-pool-name"}, &pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.Spec.Range).To(Equal("10.0.0.0/16"))
		})
	})
})

// ---------------------------------------------------------------
// OverlappingRangeReconciler extended
// ---------------------------------------------------------------
var _ = Describe("OverlappingRangeReconciler extended", func() {
	const (
		resNamespace = "default"
		interval     = 30 * time.Second
	)

	var (
		ctx        context.Context
		reconciler *OverlappingRangeReconciler
		req        ctrl.Request
	)

	buildReconciler := func(objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithStatusSubresource(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
			WithObjects(objs...).
			Build()
		reconciler = &OverlappingRangeReconciler{
			client:            fakeClient,
			recorder:          events.NewFakeRecorder(10),
			reconcileInterval: interval,
		}
	}

	buildReconcilerWithFlags := func(cleanupTerminating, cleanupDisrupted bool, objs ...client.Object) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithStatusSubresource(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
			WithObjects(objs...).
			Build()
		reconciler = &OverlappingRangeReconciler{
			client:             fakeClient,
			recorder:           events.NewFakeRecorder(10),
			reconcileInterval:  interval,
			cleanupTerminating: cleanupTerminating,
			cleanupDisrupted:   cleanupDisrupted,
		}
	}

	Context("when the pod is marked for deletion (DisruptionTarget)", func() {
		Context("with cleanupDisrupted enabled", func() {
			It("should delete the reservation", func() {
				ctx = context.Background()
				resName := "ext-or-deletion"
				req = ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: resNamespace, Name: resName,
					},
				}

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
				reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resName,
						Namespace: resNamespace,
					},
					Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
						ContainerID: "abc123",
						PodRef:      "default/evicted-pod",
						IfName:      "eth0",
					},
				}
				buildReconcilerWithFlags(false, true, reservation, pod)

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

		Context("with cleanupDisrupted disabled", func() {
			It("should keep the reservation for a disrupted pod", func() {
				ctx = context.Background()
				resName := "ext-or-disrupted-disabled"
				req = ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: resNamespace, Name: resName,
					},
				}

				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "evicted-pod-2",
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
				reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resName,
						Namespace: resNamespace,
					},
					Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
						ContainerID: "abc123",
						PodRef:      "default/evicted-pod-2",
						IfName:      "eth0",
					},
				}
				buildReconciler(reservation, pod) // cleanupDisrupted defaults to false

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				// Verify reservation still exists.
				var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			})
		})
	})

	Context("when the pod is terminating (DeletionTimestamp set)", func() {
		Context("with cleanupTerminating enabled", func() {
			It("should delete the reservation", func() {
				ctx = context.Background()
				resName := "ext-or-terminating"
				req = ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: resNamespace, Name: resName,
					},
				}

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
				reservation := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resName,
						Namespace: resNamespace,
					},
					Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
						ContainerID: "abc123",
						PodRef:      "default/terminating-pod",
						IfName:      "eth0",
					},
				}
				buildReconcilerWithFlags(true, false, reservation, pod)

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

		Context("with cleanupTerminating disabled (default)", func() {
			It("should keep the reservation for a terminating pod", func() {
				ctx = context.Background()
				resName := "ext-or-terminating-disabled"
				req = ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: resNamespace, Name: resName,
					},
				}

				now := metav1.Now()
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "terminating-pod-2",
						Namespace:         "default",
						DeletionTimestamp: &now,
						Finalizers:        []string{"test.example.com/block-deletion"},
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
						PodRef:      "default/terminating-pod-2",
						IfName:      "eth0",
					},
				}
				buildReconciler(reservation, pod) // cleanupTerminating defaults to false

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(interval))

				// Verify reservation still exists.
				var updated whereaboutsv1alpha1.OverlappingRangeIPReservation
				Expect(reconciler.client.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			})
		})
	})
})

// ---------------------------------------------------------------
// recordNodeSliceMetrics edge cases
// ---------------------------------------------------------------
var _ = Describe("recordNodeSliceMetrics edge cases", func() {
	It("handles empty allocations", func() {
		Expect(func() {
			recordNodeSliceMetrics("empty-pool", nil)
		}).NotTo(Panic())
	})

	It("handles all assigned", func() {
		allocations := []whereaboutsv1alpha1.NodeSliceAllocation{
			{SliceRange: "10.0.0.0/28", NodeName: "n1"},
			{SliceRange: "10.0.0.16/28", NodeName: "n2"},
		}
		Expect(func() {
			recordNodeSliceMetrics("all-assigned-pool", allocations)
		}).NotTo(Panic())
	})

	It("handles none assigned", func() {
		allocations := []whereaboutsv1alpha1.NodeSliceAllocation{
			{SliceRange: "10.0.0.0/28", NodeName: ""},
			{SliceRange: "10.0.0.16/28", NodeName: ""},
		}
		Expect(func() {
			recordNodeSliceMetrics("none-assigned-pool", allocations)
		}).NotTo(Panic())
	})
})

// ---------------------------------------------------------------
// makeAllocations edge cases
// ---------------------------------------------------------------
var _ = Describe("makeAllocations edge cases", func() {
	It("handles empty subnets with nodes", func() {
		allocs := makeAllocations(nil, []string{"node-a", "node-b"})
		Expect(allocs).To(BeEmpty())
	})

	It("handles subnets with empty nodes", func() {
		allocs := makeAllocations([]string{"10.0.0.0/24"}, nil)
		Expect(allocs).To(HaveLen(1))
		Expect(allocs[0].NodeName).To(Equal(""))
	})

	It("handles equal count of subnets and nodes", func() {
		allocs := makeAllocations(
			[]string{"10.0.0.0/24", "10.0.1.0/24"},
			[]string{"node-a", "node-b"},
		)
		Expect(allocs).To(HaveLen(2))
		Expect(allocs[0].NodeName).To(Equal("node-a"))
		Expect(allocs[1].NodeName).To(Equal("node-b"))
	})
})

// ---------------------------------------------------------------
// removeAllocations (unit test)
// ---------------------------------------------------------------
var _ = Describe("removeAllocations", func() {
	It("removes specified keys from allocations", func() {
		pool := &whereaboutsv1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "remove-test",
				Namespace: "default",
			},
			Spec: whereaboutsv1alpha1.IPPoolSpec{
				Range: "10.0.0.0/24",
				Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
					"0": {PodRef: "default/pod-a"},
					"1": {PodRef: "default/pod-b"},
					"2": {PodRef: "default/pod-c"},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithObjects(pool).
			Build()

		removeAllocations(pool, []string{"0", "2"})

		// Persist the in-memory change via the fake client.
		err := fakeClient.Update(context.Background(), pool)
		Expect(err).NotTo(HaveOccurred())

		var updated whereaboutsv1alpha1.IPPool
		err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "remove-test", Namespace: "default"}, &updated)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Spec.Allocations).To(HaveLen(1))
		Expect(updated.Spec.Allocations).To(HaveKey("1"))
	})

	It("handles empty key list gracefully", func() {
		pool := &whereaboutsv1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "remove-empty", Namespace: "default"},
			Spec: whereaboutsv1alpha1.IPPoolSpec{
				Range: "10.0.0.0/24",
				Allocations: map[string]whereaboutsv1alpha1.IPAllocation{
					"0": {PodRef: "default/pod-a"},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithObjects(pool).
			Build()

		removeAllocations(pool, []string{})

		// Persist the in-memory change via the fake client.
		err := fakeClient.Update(context.Background(), pool)
		Expect(err).NotTo(HaveOccurred())

		var updated whereaboutsv1alpha1.IPPool
		err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "remove-empty", Namespace: "default"}, &updated)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Spec.Allocations).To(HaveLen(1))
	})
})

// Suppress unused import warnings.
var _ = fmt.Sprintf

// ---------------------------------------------------------------
// computeSliceStats
// ---------------------------------------------------------------
var _ = Describe("computeSliceStats", func() {
	It("computes stats for a pool with mixed assigned and free slices", func() {
		pool := &whereaboutsv1alpha1.NodeSlicePool{
			Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
				Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
					{SliceRange: "10.0.0.0/24", NodeName: "node-a"},
					{SliceRange: "10.0.1.0/24", NodeName: "node-b"},
					{SliceRange: "10.0.2.0/24", NodeName: ""},
					{SliceRange: "10.0.3.0/24", NodeName: ""},
				},
			},
		}

		computeSliceStats(pool)

		Expect(pool.Status.TotalSlices).To(Equal(int32(4)))
		Expect(pool.Status.AssignedSlices).To(Equal(int32(2)))
		Expect(pool.Status.FreeSlices).To(Equal(int32(2)))
	})

	It("computes stats for a fully assigned pool", func() {
		pool := &whereaboutsv1alpha1.NodeSlicePool{
			Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
				Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
					{SliceRange: "10.0.0.0/24", NodeName: "node-a"},
					{SliceRange: "10.0.1.0/24", NodeName: "node-b"},
				},
			},
		}

		computeSliceStats(pool)

		Expect(pool.Status.TotalSlices).To(Equal(int32(2)))
		Expect(pool.Status.AssignedSlices).To(Equal(int32(2)))
		Expect(pool.Status.FreeSlices).To(Equal(int32(0)))
	})

	It("computes stats for an empty pool", func() {
		pool := &whereaboutsv1alpha1.NodeSlicePool{
			Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
				Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{},
			},
		}

		computeSliceStats(pool)

		Expect(pool.Status.TotalSlices).To(Equal(int32(0)))
		Expect(pool.Status.AssignedSlices).To(Equal(int32(0)))
		Expect(pool.Status.FreeSlices).To(Equal(int32(0)))
	})

	It("computes stats when all slices are free", func() {
		pool := &whereaboutsv1alpha1.NodeSlicePool{
			Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
				Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
					{SliceRange: "10.0.0.0/24", NodeName: ""},
					{SliceRange: "10.0.1.0/24", NodeName: ""},
					{SliceRange: "10.0.2.0/24", NodeName: ""},
				},
			},
		}

		computeSliceStats(pool)

		Expect(pool.Status.TotalSlices).To(Equal(int32(3)))
		Expect(pool.Status.AssignedSlices).To(Equal(int32(0)))
		Expect(pool.Status.FreeSlices).To(Equal(int32(3)))
	})
})

// ---------------------------------------------------------------
// PatchHelper
// ---------------------------------------------------------------
var _ = Describe("PatchHelper", func() {
	var (
		scheme     = newTestScheme()
		ctx        = context.Background()
		pool       *whereaboutsv1alpha1.IPPool
		fakeClient client.Client
	)

	makePool := func(name string, allocs map[string]whereaboutsv1alpha1.IPAllocation) *whereaboutsv1alpha1.IPPool {
		return &whereaboutsv1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: whereaboutsv1alpha1.IPPoolSpec{
				Range:       "10.0.0.0/24",
				Allocations: allocs,
			},
		}
	}

	BeforeEach(func() {
		pool = makePool("patch-test", map[string]whereaboutsv1alpha1.IPAllocation{
			"1": {PodRef: "default/pod-a", ContainerID: "aaa", IfName: "eth0"},
		})
	})

	Context("when nothing changes", func() {
		It("should not call the API server (no-op)", func() {
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool).
				Build()

			helper, err := NewPatchHelper(pool, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// No mutations — Patch should be a no-op.
			Expect(helper.HasChanges(pool)).To(BeFalse())
			Expect(helper.Patch(ctx, pool)).To(Succeed())

			// Verify object is unchanged.
			var fetched whereaboutsv1alpha1.IPPool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "patch-test", Namespace: "default"}, &fetched)).To(Succeed())
			Expect(fetched.Spec.Allocations).To(HaveLen(1))
		})
	})

	Context("when only status changes", func() {
		It("should update status without touching spec", func() {
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool).
				Build()

			helper, err := NewPatchHelper(pool, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Mutate only status.
			markReady(pool, ReasonReconciled, "all good")
			pool.Status.UsedIPs = 1
			pool.Status.FreeIPs = 253

			Expect(helper.HasChanges(pool)).To(BeTrue())
			Expect(helper.Patch(ctx, pool)).To(Succeed())

			var fetched whereaboutsv1alpha1.IPPool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "patch-test", Namespace: "default"}, &fetched)).To(Succeed())
			Expect(fetched.Status.UsedIPs).To(Equal(int32(1)))
			Expect(fetched.Status.FreeIPs).To(Equal(int32(253)))
			// Spec must be untouched.
			Expect(fetched.Spec.Allocations).To(HaveLen(1))
		})
	})

	Context("when only spec changes", func() {
		It("should update spec without touching status", func() {
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool).
				Build()

			helper, err := NewPatchHelper(pool, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Mutate only spec.
			pool.Spec.Allocations["5"] = whereaboutsv1alpha1.IPAllocation{
				PodRef: "default/pod-b", ContainerID: "bbb", IfName: "eth0",
			}

			Expect(helper.HasChanges(pool)).To(BeTrue())
			Expect(helper.Patch(ctx, pool)).To(Succeed())

			var fetched whereaboutsv1alpha1.IPPool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "patch-test", Namespace: "default"}, &fetched)).To(Succeed())
			Expect(fetched.Spec.Allocations).To(HaveLen(2))
			Expect(fetched.Spec.Allocations).To(HaveKey("5"))
		})
	})

	Context("when both spec and status change", func() {
		It("should update both spec and status", func() {
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool).
				Build()

			helper, err := NewPatchHelper(pool, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Mutate spec: remove an allocation.
			removeAllocations(pool, []string{"1"})

			// Mutate status: set stats and conditions.
			pool.Status.OrphanedIPs = 1
			pool.Status.UsedIPs = 0
			pool.Status.FreeIPs = 254
			markReady(pool, ReasonOrphansCleaned, "cleaned 1 orphan")

			Expect(helper.HasChanges(pool)).To(BeTrue())
			Expect(helper.Patch(ctx, pool)).To(Succeed())

			var fetched whereaboutsv1alpha1.IPPool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "patch-test", Namespace: "default"}, &fetched)).To(Succeed())
			// Spec: allocation removed.
			Expect(fetched.Spec.Allocations).To(BeEmpty())
			// Status: stats set.
			Expect(fetched.Status.OrphanedIPs).To(Equal(int32(1)))
			Expect(fetched.Status.UsedIPs).To(Equal(int32(0)))
			Expect(fetched.Status.FreeIPs).To(Equal(int32(254)))
		})
	})

	Context("when metadata changes (labels)", func() {
		It("should update metadata via spec patch", func() {
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool).
				Build()

			helper, err := NewPatchHelper(pool, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Mutate metadata.
			pool.Labels = map[string]string{"team": "t-caas"}

			Expect(helper.HasChanges(pool)).To(BeTrue())
			Expect(helper.Patch(ctx, pool)).To(Succeed())

			var fetched whereaboutsv1alpha1.IPPool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "patch-test", Namespace: "default"}, &fetched)).To(Succeed())
			Expect(fetched.Labels).To(HaveKeyWithValue("team", "t-caas"))
		})
	})

	Context("when a nil PatchHelper is used", func() {
		It("should be a safe no-op", func() {
			var helper *PatchHelper
			Expect(helper.Patch(ctx, pool)).To(Succeed())
			Expect(helper.HasChanges(pool)).To(BeFalse())
		})
	})

	Context("with NodeSlicePool (status-only)", func() {
		It("should update slice stats via status", func() {
			nsp := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{Name: "nsp-test", Namespace: "default"},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "24",
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.NodeSlicePool{}).
				WithObjects(nsp).
				Build()

			helper, err := NewPatchHelper(nsp, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			nsp.Status.TotalSlices = 256
			nsp.Status.AssignedSlices = 3
			nsp.Status.FreeSlices = 253
			markReady(nsp, ReasonPoolCreated, "created pool")

			Expect(helper.Patch(ctx, nsp)).To(Succeed())

			var fetched whereaboutsv1alpha1.NodeSlicePool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "nsp-test", Namespace: "default"}, &fetched)).To(Succeed())
			Expect(fetched.Status.TotalSlices).To(Equal(int32(256)))
			Expect(fetched.Status.AssignedSlices).To(Equal(int32(3)))
			Expect(fetched.Status.FreeSlices).To(Equal(int32(253)))
		})
	})

	Context("with OverlappingRangeIPReservation (status-only)", func() {
		It("should update conditions via status", func() {
			orip := &whereaboutsv1alpha1.OverlappingRangeIPReservation{
				ObjectMeta: metav1.ObjectMeta{Name: "10.0.0.5", Namespace: "default"},
				Spec: whereaboutsv1alpha1.OverlappingRangeIPReservationSpec{
					PodRef: "default/my-pod",
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.OverlappingRangeIPReservation{}).
				WithObjects(orip).
				Build()

			helper, err := NewPatchHelper(orip, fakeClient)
			Expect(err).NotTo(HaveOccurred())

			markReady(orip, ReasonValidated, "pod exists")

			Expect(helper.Patch(ctx, orip)).To(Succeed())

			var fetched whereaboutsv1alpha1.OverlappingRangeIPReservation
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "10.0.0.5", Namespace: "default"}, &fetched)).To(Succeed())
			Expect(fetched.Status.Conditions).NotTo(BeEmpty())
		})
	})

	Context("idempotency", func() {
		It("should be a no-op when called twice with the same state", func() {
			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&whereaboutsv1alpha1.IPPool{}).
				WithObjects(pool).
				Build()

			// First patch: set status.
			helper1, err := NewPatchHelper(pool, fakeClient)
			Expect(err).NotTo(HaveOccurred())
			pool.Status.UsedIPs = 1
			markReady(pool, ReasonReconciled, "done")
			Expect(helper1.Patch(ctx, pool)).To(Succeed())

			// Re-read the object from "server" to get fresh state.
			var refreshed whereaboutsv1alpha1.IPPool
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "patch-test", Namespace: "default"}, &refreshed)).To(Succeed())

			// Second patch: no mutations — should be no-op.
			helper2, err := NewPatchHelper(&refreshed, fakeClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(helper2.HasChanges(&refreshed)).To(BeFalse())
			Expect(helper2.Patch(ctx, &refreshed)).To(Succeed())
		})
	})
})
