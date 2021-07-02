package main

import (
	"context"
	"fmt"
	"net"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/dougbtv/whereabouts/pkg/api/v1alpha1"
	"github.com/dougbtv/whereabouts/pkg/reconciler"
	"github.com/dougbtv/whereabouts/pkg/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Whereabouts IP reconciler", func() {
	const (
		ipRange     = "10.10.10.0/16"
		namespace   = "testns"
		networkName = "net1"
		podName     = "pod1"
	)

	var (
		reconcileLooper *reconciler.ReconcileLooper
	)

	Context("a single running pod", func() {
		var pod *v1.Pod

		BeforeEach(func() {
			var err error
			pod, err = k8sClientSet.CoreV1().Pods(namespace).Create(generatePod(namespace, podName, networkName))
			Expect(err).NotTo(HaveOccurred())
		})

		Context("with IP from a single IPPool", func() {
			const poolName = "pool1"

			var pool *v1alpha1.IPPool

			BeforeEach(func() {
				pool = generateIPPoolSpec(ipRange, namespace, poolName, pod.Name)
				Expect(k8sClient.Create(context.Background(), pool)).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(context.Background(), pool)).NotTo(HaveOccurred())
			})

			Context("the pod dies", func() {
				BeforeEach(func() {
					Expect(k8sClientSet.CoreV1().Pods(namespace).Delete(pod.Name, &metav1.DeleteOptions{})).NotTo(HaveOccurred())
				})

				Context("reconciling the IPPool", func() {
					BeforeEach(func() {
						var err error
						reconcileLooper, err = reconciler.NewReconcileLooper(kubeConfigPath, context.TODO())
						Expect(err).NotTo(HaveOccurred())
					})

					It("should report the deleted IP reservation", func() {
						expectedIPReservation := types.IPReservation{
							IP:     net.ParseIP("10.10.10.1"),
							PodRef: fmt.Sprintf("%s/%s", namespace, podName),
						}
						Expect(reconcileLooper.ReconcileIPPools()).To(ConsistOf(expectedIPReservation))
					})

					It("the pool's orphaned IP should be deleted after the reconcile loop", func() {
						_, err := reconcileLooper.ReconcileIPPools()
						Expect(err).NotTo(HaveOccurred())
						var poolAfterCleanup v1alpha1.IPPool
						poolKey := k8stypes.NamespacedName{Namespace: namespace, Name: pool.Name}
						Expect(k8sClient.Get(context.Background(), poolKey, &poolAfterCleanup)).To(Succeed())
						Expect(poolAfterCleanup.Spec.Allocations).To(BeEmpty())
					})
				})
			})
		})
	})

	Context("multiple pods", func() {
		const (
			deadPodIndex = 0
			livePodIndex = 1
			numberOfPods = 2
		)

		var pods []v1.Pod

		BeforeEach(func() {
			for i := 0; i < numberOfPods; i++ {
				pod := generatePod(namespace, fmt.Sprintf("pod%d", i+1), networkName)
				if i == livePodIndex {
					_, err := k8sClientSet.CoreV1().Pods(namespace).Create(pod)
					Expect(err).NotTo(HaveOccurred())
				}
				pods = append(pods, *pod)
			}
		})

		AfterEach(func() {
			Expect(k8sClientSet.CoreV1().Pods(namespace).Delete(pods[livePodIndex].Name, &metav1.DeleteOptions{})).NotTo(HaveOccurred())
			pods = nil
		})

		Context("each with IP from the same IPPool", func() {
			const poolName = "pool1"

			var pool *v1alpha1.IPPool

			BeforeEach(func() {
				var podNames []string
				for _, pod := range pods {
					podNames = append(podNames, pod.Name)
				}
				pool = generateIPPoolSpec(ipRange, namespace, poolName, podNames...)
				Expect(k8sClient.Create(context.Background(), pool)).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(context.Background(), pool)).NotTo(HaveOccurred())
			})

			Context("reconciling the IPPool", func() {
				BeforeEach(func() {
					var err error
					reconcileLooper, err = reconciler.NewReconcileLooper(kubeConfigPath, context.TODO())
					Expect(err).NotTo(HaveOccurred())
				})

				It("should report the dead pod's IP address as deleted", func() {
					expectedReservation := types.IPReservation{
						IP:     net.ParseIP("10.10.10.1"),
						PodRef: fmt.Sprintf("%s/%s", namespace, pods[deadPodIndex].Name),
					}
					deletedIPAddrs, err := reconcileLooper.ReconcileIPPools()
					Expect(err).NotTo(HaveOccurred())
					Expect(deletedIPAddrs).To(ConsistOf(expectedReservation))
				})

				It("the IPPool should have only the IP reservation of the live pod", func() {
					deletedIPAddrs, err := reconcileLooper.ReconcileIPPools()
					Expect(err).NotTo(HaveOccurred())
					Expect(deletedIPAddrs).NotTo(BeEmpty())

					var poolAfterCleanup v1alpha1.IPPool
					poolKey := k8stypes.NamespacedName{Namespace: namespace, Name: pool.Name}
					Expect(k8sClient.Get(context.Background(), poolKey, &poolAfterCleanup)).To(Succeed())

					remainingAllocation := map[string]v1alpha1.IPAllocation{
						"2": {
							PodRef: fmt.Sprintf("%s/%s", namespace, pods[livePodIndex].Name),
						},
					}
					Expect(poolAfterCleanup.Spec.Allocations).To(Equal(remainingAllocation))
				})
			})
		})
	})
})

func generateIPPoolSpec(ipRange string, namespace string, poolName string, podNames ...string) *v1alpha1.IPPool {
	allocations := map[string]v1alpha1.IPAllocation{}
	for i, podName := range podNames {
		allocations[fmt.Sprintf("%d", i+1)] = v1alpha1.IPAllocation{
			PodRef: fmt.Sprintf("%s/%s", namespace, podName),
		}
	}
	return &v1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: poolName},
		Spec: v1alpha1.IPPoolSpec{
			Range:       ipRange,
			Allocations: allocations,
		},
	}
}

func generatePod(namespace string, podName string, networks ...string) *v1.Pod {
	networkAnnotations := map[string]string{"k8s.v1.cni.cncf.io/networks": strings.Join(networks, ",")}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   namespace,
			Annotations: networkAnnotations,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    podName,
					Image:   "alpine",
					Command: []string{"/bin/bash", "-c", "sleep 2000000000000"},
				},
			},
		},
	}
}
