package ipamclaim

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/utils"
)

func TestIPAMClaim(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipamclaim")
}

var _ = Describe("IPAMClaim reference helpers", func() {
	Describe("ReferenceFromNetworkSelectionElements", func() {
		It("returns empty for nil/empty elements", func() {
			Expect(ReferenceFromNetworkSelectionElements(nil, "net", "net1")).To(BeEmpty())
			Expect(ReferenceFromNetworkSelectionElements([]*nadv1.NetworkSelectionElement{}, "net", "net1")).To(BeEmpty())
		})

		It("matches by interface name", func() {
			elements := []*nadv1.NetworkSelectionElement{
				{Name: "other", InterfaceRequest: "net2", IPAMClaimReference: "claim-b"},
				{Name: "mynet", InterfaceRequest: "net1", IPAMClaimReference: "claim-a"},
			}
			Expect(ReferenceFromNetworkSelectionElements(elements, "mynet", "net1")).To(Equal("claim-a"))
		})

		It("matches by network name when interface is unset on the element", func() {
			elements := []*nadv1.NetworkSelectionElement{
				{Name: "mynet", IPAMClaimReference: "claim-a"},
			}
			Expect(ReferenceFromNetworkSelectionElements(elements, "mynet", "net1")).To(Equal("claim-a"))
		})

		It("falls back to the sole claim-bearing element", func() {
			elements := []*nadv1.NetworkSelectionElement{
				{Name: "mynet", IPAMClaimReference: "only-claim"},
				{Name: "other"},
			}
			Expect(ReferenceFromNetworkSelectionElements(elements, "unused", "unused")).To(Equal("only-claim"))
		})

		It("returns empty when multiple claims exist and filters do not match", func() {
			elements := []*nadv1.NetworkSelectionElement{
				{Name: "a", IPAMClaimReference: "claim-a"},
				{Name: "b", IPAMClaimReference: "claim-b"},
			}
			Expect(ReferenceFromNetworkSelectionElements(elements, "c", "net9")).To(BeEmpty())
		})
	})

	Describe("ReferenceFromCNIArgs", func() {
		It("reads ipam-claim-reference from cni args", func() {
			args := map[string]interface{}{ReferenceField: "vm-eth0"}
			Expect(ReferenceFromCNIArgs(&args)).To(Equal("vm-eth0"))
		})

		It("returns empty for missing or non-string values", func() {
			Expect(ReferenceFromCNIArgs(nil)).To(BeEmpty())
			args := map[string]interface{}{ReferenceField: 42}
			Expect(ReferenceFromCNIArgs(&args)).To(BeEmpty())
		})
	})

	Describe("ParsePodNetworkAnnotation integration", func() {
		It("extracts IPAMClaimReference for matching interface", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nadv1.NetworkAttachmentAnnot: `[{"name":"wa-nad","interface":"net1","ipam-claim-reference":"wa-persistent-claim"}]`,
					},
				},
			}

			elements, err := utils.ParsePodNetworkAnnotation(pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(elements).To(HaveLen(1))
			Expect(elements[0].InterfaceRequest).To(Equal("net1"))
			Expect(elements[0].IPAMClaimReference).To(Equal("wa-persistent-claim"))

			Expect(ReferenceFromNetworkSelectionElements(elements, "", "net1")).To(Equal("wa-persistent-claim"))
		})
	})
})
