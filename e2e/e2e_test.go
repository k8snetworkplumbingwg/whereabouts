package whereabouts_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/typed/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	wbstorage "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
)

const (
	testNetworkName = "wa-nad"
	testNamespace   = "default"
	testImage       = "quay.io/dougbtv/alpine:latest"
	ipv4TestRange   = "10.10.0.0/16"
	singlePodName   = "whereabouts-basic-test"
	createTimeout   = 10 * time.Second
	deleteTimeout   = 2 * createTimeout
)

func TestWhereaboutsE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "whereabouts-e2e")
}

var _ = Describe("Whereabouts functionality", func() {
	Context("Test setup", func() {
		var (
			clientInfo   *ClientInfo
			netAttachDef *nettypes.NetworkAttachmentDefinition
			pod          *core.Pod
		)

		BeforeEach(func() {
			config, err := clusterConfig()
			Expect(err).NotTo(HaveOccurred())

			clientInfo, err = NewClientInfo(config)
			Expect(err).NotTo(HaveOccurred())

			netAttachDef = macvlanNetworkWithWhereaboutsIPAMNetwork()

			By("creating a NetworkAttachmentDefinition for whereabouts")
			_, err = clientInfo.addNetAttachDef(netAttachDef)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(clientInfo.delNetAttachDef(netAttachDef)).To(Succeed())
		})

		Context("Single pod tests", func() {
			BeforeEach(func() {
				By("creating a pod with whereabouts net-attach-def")
				var err error
				pod, err = clientInfo.provisionPod(
					podTierLabel(singlePodName),
					podNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("deleting pod with whereabouts net-attach-def")
				Expect(clientInfo.deletePod(pod)).To(Succeed())
			})

			It("allocates a single pod within the correct IP range", func() {
				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIP, err := secondaryIfaceIPValue(pod)
				Expect(err).NotTo(HaveOccurred())
				Expect(inRange(ipv4TestRange, secondaryIfaceIP)).To(Succeed())
			})
		})

		Context("stateful set tests", func() {
			const (
				initialReplicaNumber = 20
				ipPoolNamespace      = "kube-system"
				namespace            = "default"
				serviceName          = "web"
				selector             = "app=" + serviceName
				statefulSetName      = "statefulthingy"
			)

			BeforeEach(func() {
				var err error
				_, err = clientInfo.provisionStatefulSet(statefulSetName, namespace, serviceName, initialReplicaNumber)
				Expect(err).NotTo(HaveOccurred())
				Expect(
					clientInfo.Client.CoreV1().Pods(namespace).List(
						context.TODO(), metav1.ListOptions{LabelSelector: selector})).To(
					WithTransform(func(podList *core.PodList) int { return len(podList.Items) }, Equal(initialReplicaNumber)))
			})

			AfterEach(func() {
				const noReplicas = 0
				Expect(clientInfo.deleteStatefulSet(namespace, serviceName, selector)).To(Succeed())
				Expect(
					clientInfo.Client.CoreV1().Pods(namespace).List(
						context.TODO(), metav1.ListOptions{LabelSelector: selector})).To(
					WithTransform(func(podList *core.PodList) int { return len(podList.Items) }, Equal(noReplicas)))

				poolAllocations := func(ipPool *v1alpha1.IPPool) map[string]v1alpha1.IPAllocation {
					return ipPool.Spec.Allocations
				}
				Expect(
					clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
						context.TODO(),
						wbstorage.NormalizeRange(ipv4TestRange),
						metav1.GetOptions{})).To(
					WithTransform(poolAllocations, BeEmpty()))
			})

			It("IPPools feature allocations", func() {
				ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(context.TODO(), wbstorage.NormalizeRange(ipv4TestRange), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ipPool.Spec.Allocations)).To(Equal(initialReplicaNumber))
			})

			When("the statefulset is scaled up then down", func() {
				const deltaInstances = 5

				BeforeEach(func() {
					Expect(clientInfo.scaleStatefulSet(serviceName, namespace, deltaInstances)).To(Succeed())
					Expect(clientInfo.scaleStatefulSet(serviceName, namespace, -deltaInstances)).To(Succeed())
				})

				It("scale down then up does not leave stale addresses", func() {
					const scaleTimeout = createTimeout * 2

					Eventually(func() (int, error) {
						associatedPods, err := clientInfo.Client.CoreV1().Pods(namespace).List(
							context.TODO(), metav1.ListOptions{LabelSelector: selector})
						if err != nil {
							return -1, err
						}

						return len(associatedPods.Items), nil
					}, scaleTimeout).Should(Equal(initialReplicaNumber), "we should have the original pod number again")

					Eventually(func() (int, error) {
						ipPool, err := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace).Get(
							context.TODO(), wbstorage.NormalizeRange(ipv4TestRange), metav1.GetOptions{})
						if err != nil {
							return -1, err
						}

						return len(ipPool.Spec.Allocations), nil
					}, scaleTimeout).Should(Equal(initialReplicaNumber), "we should have one allocation for each live pod")
				})
			})
		})
	})
})

func clusterConfig() (*rest.Config, error) {
	const kubeconfig = "KUBECONFIG"

	kubeconfigPath, found := os.LookupEnv(kubeconfig)
	if !found {
		return nil, fmt.Errorf("must provide the path to the kubeconfig via the `KUBECONFIG` env variable")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func podTierLabel(podTier string) map[string]string {
	const tier = "tier"
	return map[string]string{tier: podTier}
}

func podNetworkSelectionElements(networkNames ...string) map[string]string {
	return map[string]string{
		nettypes.NetworkAttachmentAnnot: strings.Join(networkNames, ","),
	}
}

type ClientInfo struct {
	Client    *kubernetes.Clientset
	NetClient netclient.K8sCniCncfIoV1Interface
	WbClient  wbclient.Interface
}

func NewClientInfo(config *rest.Config) (*ClientInfo, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	netClient, err := netclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	wbClient, err := wbclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &ClientInfo{
		Client:    clientSet,
		NetClient: netClient,
		WbClient:  wbClient,
	}, nil
}

func (c *ClientInfo) addNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) (*nettypes.NetworkAttachmentDefinition, error) {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Create(context.TODO(), netattach, metav1.CreateOptions{})
}

func (c *ClientInfo) delNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) error {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Delete(context.TODO(), netattach.Name, metav1.DeleteOptions{})
}

func (c *ClientInfo) provisionPod(label, annotations map[string]string) (*core.Pod, error) {
	pod := podObject("wa-e2e-pod", label, annotations)
	pod, err := c.Client.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := WaitForPodReady(c.Client, pod.Namespace, pod.Name, createTimeout); err != nil {
		return nil, err
	}

	pod, err = c.Client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

func (c *ClientInfo) deletePod(pod *core.Pod) error {
	if err := c.Client.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	if err := WaitForPodToDisappear(c.Client, pod.GetNamespace(), pod.GetName(), deleteTimeout); err != nil {
		return err
	}
	return nil
}

func (c *ClientInfo) provisionStatefulSet(statefulSetName string, namespace string, serviceName string, replicas int) (*v1.StatefulSet, error) {
	const statefulSetCreateTimeout = 2 * createTimeout
	statefulSet, err := c.Client.AppsV1().StatefulSets(namespace).Create(
		context.TODO(),
		statefulSetSpec(statefulSetName, serviceName, replicas, podNetworkSelectionElements(testNetworkName)),
		metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := WaitForStatefulSetReady(
		c.Client,
		namespace,
		serviceName,
		replicas,
		statefulSetCreateTimeout); err != nil {
		return nil, err
	}
	return statefulSet, nil
}

func (c *ClientInfo) deleteStatefulSet(namespace string, serviceName string, labelSelector string) error {
	const statefulSetDeleteTimeout = 2 * deleteTimeout
	if err := c.Client.AppsV1().StatefulSets(namespace).Delete(context.TODO(), serviceName, metav1.DeleteOptions{}); err != nil {
		return err
	}

	return WaitForStatefulSetGone(c.Client, namespace, serviceName, labelSelector, statefulSetDeleteTimeout)
}

func (c *ClientInfo) scaleStatefulSet(statefulSetName string, namespace string, deltaInstance int) error {
	statefulSet, err := c.Client.AppsV1().StatefulSets(namespace).Get(context.TODO(), statefulSetName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	newReplicas := *statefulSet.Spec.Replicas + int32(deltaInstance)
	statefulSet.Spec.Replicas = &newReplicas

	if _, err := c.Client.AppsV1().StatefulSets(namespace).Update(context.TODO(), statefulSet, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

// Returns a network attachment definition object configured by provided parameters
func generateNetAttachDefSpec(name, namespace, config string) *nettypes.NetworkAttachmentDefinition {
	return &nettypes.NetworkAttachmentDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "NetworkAttachmentDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: nettypes.NetworkAttachmentDefinitionSpec{
			Config: config,
		},
	}
}

func macvlanNetworkWithWhereaboutsIPAMNetwork() *nettypes.NetworkAttachmentDefinition {
	macvlanConfig := `{
        "cniVersion": "0.3.0",
      	"disableCheck": true,
        "plugins": [
            {
                "type": "macvlan",
              	"master": "eth0",
              	"mode": "bridge",
              	"ipam": {
                    "type": "whereabouts",
                    "leader_lease_duration": 1500,
                    "leader_renew_deadline": 1000,
                    "leader_retry_period": 500,
                    "range": "10.10.0.0/16",
                    "log_level": "debug",
                    "log_file": "/tmp/wb"
              	}
            }
        ]
    }`
	return generateNetAttachDefSpec(testNetworkName, testNamespace, macvlanConfig)
}

func podObject(podName string, label, annotations map[string]string) *core.Pod {
	return &core.Pod{
		ObjectMeta: podMeta(podName, label, annotations),
		Spec:       podSpec("samplepod"),
	}
}

func podSpec(containerName string) core.PodSpec {
	return core.PodSpec{
		Containers: []core.Container{
			{
				Name:    containerName,
				Command: containerCmd(),
				Image:   testImage,
			},
		},
	}
}

func podMeta(podName string, label map[string]string, annotations map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:        podName,
		Namespace:   testNamespace,
		Labels:      label,
		Annotations: annotations,
	}
}

func statefulSetSpec(statefulSetName string, serviceName string, replicaNumber int, annotations map[string]string) *v1.StatefulSet {
	const labelKey = "app"

	replicas := int32(replicaNumber)
	webAppLabels := map[string]string{labelKey: serviceName}
	return &v1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: serviceName},
		Spec: v1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: webAppLabels,
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: podMeta(statefulSetName, webAppLabels, annotations),
				Spec:       podSpec(statefulSetName),
			},
			ServiceName:         serviceName,
			PodManagementPolicy: v1.ParallelPodManagement,
		},
	}
}

func containerCmd() []string {
	return []string{"/bin/ash", "-c", "trap : TERM INT; sleep infinity & wait"}
}

func filterNetworkStatus(
	networkStatuses []nettypes.NetworkStatus, predicate func(nettypes.NetworkStatus) bool) *nettypes.NetworkStatus {
	for i, networkStatus := range networkStatuses {
		if predicate(networkStatus) {
			return &networkStatuses[i]
		}
	}
	return nil
}

func secondaryIfaceIPValue(pod *core.Pod) (string, error) {
	podNetStatus, found := pod.Annotations[nettypes.NetworkStatusAnnot]
	if !found {
		return "", fmt.Errorf("the pod must feature the `networks-status` annotation")
	}

	var netStatus []nettypes.NetworkStatus
	if err := json.Unmarshal([]byte(podNetStatus), &netStatus); err != nil {
		return "", err
	}

	secondaryInterfaceNetworkStatus := filterNetworkStatus(netStatus, func(status nettypes.NetworkStatus) bool {
		return status.Interface == "net1"
	})

	if len(secondaryInterfaceNetworkStatus.IPs) == 0 {
		return "", fmt.Errorf("the pod does not have IPs for its secondary interfaces")
	}

	return secondaryInterfaceNetworkStatus.IPs[0], nil
}

func inRange(cidr string, ip string) error {
	_, cidrRange, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}

	if cidrRange.Contains(net.ParseIP(ip)) {
		return nil
	}

	return fmt.Errorf("ip [%s] is NOT in range %s", ip, cidr)
}
