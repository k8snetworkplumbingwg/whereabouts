// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"

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

func makeNADConfig(networkName, ipRange, sliceSize string) string {
	conf := map[string]interface{}{
		"name": "testnet",
		"ipam": map[string]interface{}{
			"type":            "whereabouts",
			"range":           ipRange,
			"node_slice_size": sliceSize,
		},
	}
	if networkName != "" {
		conf["ipam"].(map[string]interface{})["network_name"] = networkName
	}
	b, err := json.Marshal(conf)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

var _ = Describe("NodeSliceReconciler", func() {
	const (
		nadName      = "test-nad"
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

	Context("when the NAD is not found", func() {
		It("should return no error and no requeue", func() {
			buildReconciler()

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("when the NAD has no whereabouts IPAM config", func() {
		It("should skip without error", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: `{"name":"testnet","ipam":{"type":"host-local"}}`,
				},
			}
			buildReconciler(nad)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when the NAD has no node_slice_size", func() {
		It("should skip without error", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: `{"name":"testnet","ipam":{"type":"whereabouts","range":"10.0.0.0/16"}}`,
				},
			}
			buildReconciler(nad)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when the NAD has valid config and no existing pool", func() {
		It("should create a NodeSlicePool", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-1",
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("", "10.0.0.0/16", "/24"),
				},
			}
			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}}
			buildReconciler(nad, node1, node2)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify a NodeSlicePool was created. Pool name is the NAD name when network_name is empty.
			var pool whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "testnet"}, &pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.Spec.Range).To(Equal("10.0.0.0/16"))
			Expect(pool.Spec.SliceSize).To(Equal("/24"))
			Expect(pool.OwnerReferences).To(HaveLen(1))
			Expect(pool.OwnerReferences[0].UID).To(Equal(nad.UID))

			// Verify slice stats are populated.
			Expect(pool.Status.TotalSlices).To(BeNumerically(">", 0))
			Expect(pool.Status.AssignedSlices).To(BeNumerically("<=", pool.Status.TotalSlices))
			Expect(pool.Status.FreeSlices).To(Equal(pool.Status.TotalSlices - pool.Status.AssignedSlices))
		})
	})

	Context("when a pool exists and a new node is added", func() {
		It("should assign the new node to a free slot", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-1",
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("", "10.0.0.0/16", "/24"),
				},
			}
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testnet",
					Namespace: nadNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: nad.APIVersion,
							Kind:       nad.Kind,
							Name:       nad.Name,
							UID:        nad.UID,
						},
					},
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
				Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
					Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
						{SliceRange: "10.0.0.0/24", NodeName: "node-a"},
						{SliceRange: "10.0.1.0/24", NodeName: ""},
						{SliceRange: "10.0.2.0/24", NodeName: ""},
					},
				},
			}
			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}}
			buildReconciler(nad, pool, node1, node2)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify node-b was assigned.
			var updated whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "testnet"}, &updated)
			Expect(err).NotTo(HaveOccurred())

			nodeNames := map[string]bool{}
			for _, a := range updated.Status.Allocations {
				if a.NodeName != "" {
					nodeNames[a.NodeName] = true
				}
			}
			Expect(nodeNames).To(HaveKey("node-a"))
			Expect(nodeNames).To(HaveKey("node-b"))

			// Verify slice stats after node assignment.
			Expect(updated.Status.TotalSlices).To(Equal(int32(3)))
			Expect(updated.Status.AssignedSlices).To(Equal(int32(2)))
			Expect(updated.Status.FreeSlices).To(Equal(int32(1)))
		})
	})

	Context("when a pool exists and a node is removed", func() {
		It("should clear the removed node's assignment", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-1",
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: makeNADConfig("", "10.0.0.0/16", "/24"),
				},
			}
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testnet",
					Namespace: nadNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: nad.APIVersion,
							Kind:       nad.Kind,
							Name:       nad.Name,
							UID:        nad.UID,
						},
					},
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
				Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
					Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
						{SliceRange: "10.0.0.0/24", NodeName: "node-a"},
						{SliceRange: "10.0.1.0/24", NodeName: "node-removed"},
						{SliceRange: "10.0.2.0/24", NodeName: ""},
					},
				},
			}
			// Only node-a exists; node-removed was deleted.
			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			buildReconciler(nad, pool, node1)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "testnet"}, &updated)
			Expect(err).NotTo(HaveOccurred())

			// Verify node-removed is cleared.
			for _, a := range updated.Status.Allocations {
				Expect(a.NodeName).NotTo(Equal("node-removed"))
			}

			// Verify slice stats after node removal.
			Expect(updated.Status.TotalSlices).To(Equal(int32(3)))
			Expect(updated.Status.AssignedSlices).To(Equal(int32(1)))
			Expect(updated.Status.FreeSlices).To(Equal(int32(2)))
		})
	})

	Context("when the NAD range changes (spec changed)", func() {
		It("should update the pool spec and reallocate", func() {
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-1",
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					// Range changed from /16 to /20.
					Config: makeNADConfig("", "10.0.0.0/20", "/24"),
				},
			}
			pool := &whereaboutsv1alpha1.NodeSlicePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testnet",
					Namespace: nadNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: nad.APIVersion,
							Kind:       nad.Kind,
							Name:       nad.Name,
							UID:        nad.UID,
						},
					},
				},
				Spec: whereaboutsv1alpha1.NodeSlicePoolSpec{
					Range:     "10.0.0.0/16",
					SliceSize: "/24",
				},
				Status: whereaboutsv1alpha1.NodeSlicePoolStatus{
					Allocations: []whereaboutsv1alpha1.NodeSliceAllocation{
						{SliceRange: "10.0.0.0/24", NodeName: "node-a"},
					},
				},
			}
			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			buildReconciler(nad, pool, node1)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var updated whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "testnet"}, &updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Spec.Range).To(Equal("10.0.0.0/20"))

			// Verify slice stats after spec update.
			Expect(updated.Status.TotalSlices).To(BeNumerically(">", 0))
			Expect(updated.Status.FreeSlices).To(Equal(updated.Status.TotalSlices - updated.Status.AssignedSlices))
		})
	})

	Context("when a conflist NAD is used", func() {
		It("should parse the IPAM config from plugins array", func() {
			confListConfig := `{"name":"testnet","plugins":[{"type":"bridge"},{"type":"whereabouts","ipam":{"type":"whereabouts","range":"10.0.0.0/16","node_slice_size":"/24"}}]}`
			nad := &nadv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: nadNamespace,
					UID:       "nad-uid-1",
				},
				Spec: nadv1.NetworkAttachmentDefinitionSpec{
					Config: confListConfig,
				},
			}
			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			buildReconciler(nad, node1)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var pool whereaboutsv1alpha1.NodeSlicePool
			err = reconciler.client.Get(ctx, types.NamespacedName{Namespace: nadNamespace, Name: "testnet"}, &pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.Spec.Range).To(Equal("10.0.0.0/16"))
		})
	})

	Context("parseNADIPAMConfig", func() {
		It("should parse single plugin config", func() {
			config := `{"name":"mynet","ipam":{"type":"whereabouts","range":"10.0.0.0/24","node_slice_size":"/28","network_name":"custom-name"}}`
			conf, err := parseNADIPAMConfig(config)
			Expect(err).NotTo(HaveOccurred())
			Expect(conf.Range).To(Equal("10.0.0.0/24"))
			Expect(conf.NodeSliceSize).To(Equal("/28"))
			Expect(conf.NetworkName).To(Equal("custom-name"))
			Expect(conf.Name).To(Equal("mynet"))
		})

		It("should parse conflist config", func() {
			config := `{"name":"mynet","plugins":[{"type":"bridge"},{"ipam":{"type":"whereabouts","range":"fd00::/64","node_slice_size":"/80"}}]}`
			conf, err := parseNADIPAMConfig(config)
			Expect(err).NotTo(HaveOccurred())
			Expect(conf.Range).To(Equal("fd00::/64"))
			Expect(conf.NodeSliceSize).To(Equal("/80"))
		})

		It("should return error for non-whereabouts config", func() {
			config := `{"name":"mynet","ipam":{"type":"host-local"}}`
			_, err := parseNADIPAMConfig(config)
			Expect(err).To(HaveOccurred())
		})

		It("should return error for empty config", func() {
			_, err := parseNADIPAMConfig("")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("makeAllocations", func() {
		It("should create allocations with node assignments", func() {
			subnets := []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24"}
			nodes := []string{"node-a", "node-b"}

			allocs := makeAllocations(subnets, nodes)
			Expect(allocs).To(HaveLen(3))
			Expect(allocs[0].SliceRange).To(Equal("10.0.0.0/24"))
			Expect(allocs[0].NodeName).To(Equal("node-a"))
			Expect(allocs[1].SliceRange).To(Equal("10.0.1.0/24"))
			Expect(allocs[1].NodeName).To(Equal("node-b"))
			Expect(allocs[2].SliceRange).To(Equal("10.0.2.0/24"))
			Expect(allocs[2].NodeName).To(Equal(""))
		})

		It("should handle more nodes than subnets", func() {
			subnets := []string{"10.0.0.0/24"}
			nodes := []string{"node-a", "node-b", "node-c"}

			allocs := makeAllocations(subnets, nodes)
			Expect(allocs).To(HaveLen(1))
			Expect(allocs[0].NodeName).To(Equal("node-a"))
		})

		It("should handle empty inputs", func() {
			allocs := makeAllocations(nil, nil)
			Expect(allocs).To(BeEmpty())
		})
	})
})
