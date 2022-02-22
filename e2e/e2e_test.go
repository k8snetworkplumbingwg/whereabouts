package whereabouts_e2e

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"

	"time"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/typed/k8s.cni.cncf.io/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Global Constants
const (
	testNetworkName = "wa-nad"
	testNamespace   = "default"
	testImage       = "quay.io/dougbtv/alpine:latest"
	ipv4TestRange   = "10.10.0.0/16"
	singlePodName   = "whereabouts-basic-test"
)

// ClientInfo contains information given from k8s client
type ClientInfo struct {
	Client    kubernetes.Interface
	NetClient netclient.K8sCniCncfIoV1Interface
}

func TestWhereaboutsE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "whereabouts-e2e")
}

var _ = Describe("Whereabouts functionality", func() {
	Context("Test setup", func() {
		// Declare variables
		var (
			err          error
			kubeconfig   string
			label        map[string]string
			annotations  map[string]string
			clientInfo   ClientInfo
			clientSet    *kubernetes.Clientset
			netClient    netclient.K8sCniCncfIoV1Interface
			netAttachDef *nettypes.NetworkAttachmentDefinition
			config       *rest.Config
			netStatus    []nettypes.NetworkStatus
			pod          *core.Pod
		)

		BeforeEach(func() {
			var found bool
			kubeconfig, found = os.LookupEnv("KUBECONFIG")
			Expect(found).To(BeTrue(), "must provide the path to the kubeconfig via the `KUBECONFIG` env variable")

			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			Expect(err).To(BeNil())

			// Create k8sclient and ClientInfo object
			clientSet, err = kubernetes.NewForConfig(config)
			Expect(err).To(BeNil())
			netClient, err = netclient.NewForConfig(config)
			Expect(err).To(BeNil())

			clientInfo = ClientInfo{
				Client:    clientSet,
				NetClient: netClient,
			}
			netAttachDef = macvlanNetworkWithWhereaboutsIPAMNetwork()

			label = make(map[string]string)
			annotations = make(map[string]string)
			annotations["k8s.v1.cni.cncf.io/networks"] = testNetworkName

			// Create a net-attach-def
			By("creating a NetworkAttachmentDefinition for whereabouts")
			_, err = clientInfo.addNetAttachDef(netAttachDef)
			Expect(err).To(BeNil())
		})

		AfterEach(func() {
			Expect(clientInfo.delNetAttachDef(netAttachDef)).To(Succeed())
		})

		Context("Single pod tests", func() {
			BeforeEach(func() {
				By("creating a pod with whereabouts net-attach-def")
				label["tier"] = singlePodName
				pod = provisionPod(label, annotations, clientSet)
			})

			AfterEach(func() {
				By("deleting pod with whereabouts net-attach-def")
				deletePod(pod, clientSet)
			})

			It("allocates a single pod with the correct IP range", func() {
				// Get net1 IP address from pod
				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIP := secondaryIfaceIPValue(pod, netStatus)
				Expect(inRange(ipv4TestRange, secondaryIfaceIP)).To(BeTrue())
			})
		})
	})
})

// AddNetAttachDef adds a net-attach-def into kubernetes
// Returns a NAD object and an error variable
func (c *ClientInfo) addNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) (*nettypes.NetworkAttachmentDefinition, error) {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Create(context.TODO(), netattach, metav1.CreateOptions{})
}

// DelNetAttachDef removes a net-attach-def from kubernetes
// Returns an error variable
func (c *ClientInfo) delNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) error {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Delete(context.TODO(), netattach.Name, metav1.DeleteOptions{})
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

// Returns a network attachment definition object configured for whereabouts
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

// returns a pod object and creates the pod in kubernetes
func provisionPod(label, annotations map[string]string, clientSet *kubernetes.Clientset) *core.Pod {
	// Create pod
	pod := podObject(label, annotations)
	pod, err := clientSet.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	Expect(err).To(BeNil())

	// Wait for pod to become ready
	Expect(WaitForPodReady(clientSet, pod.Namespace, pod.Name, 10*time.Second)).To(Succeed())

	// Update pod object
	pod, err = clientSet.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	Expect(err).To(BeNil())

	return pod
}

// takes in a pod object and deletes it in kubernetes - the function also waits for the pod to explicitly not exist (aka finish terminating)
func deletePod(pod *core.Pod, clientSet *kubernetes.Clientset) {
	Expect(clientSet.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})).To(Succeed())
	Eventually(func() error {
		_, err := clientSet.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		return err
	}, 20*time.Second, time.Second).ShouldNot(BeNil()) // eventually, to make this cleaner, instead of this, check if error is NotFound/IsNotFound
}

// Takes in a label and whereabouts annotations
// Returns a pod object with a whereabouts annotation
func podObject(label, annotations map[string]string) *core.Pod {
	return &core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "wa-e2e-pod",
			Namespace:   testNamespace,
			Labels:      label,
			Annotations: annotations,
		},
		Spec: core.PodSpec{
			Containers: []core.Container{
				{
					Name:    "samplepod",
					Command: containerCmd(),
					Image:   testImage,
				},
			},
		},
	}
}

func containerCmd() []string {
	return []string{"/bin/ash", "-c", "trap : TERM INT; sleep infinity & wait"}
}

func filterNetworkStatus(networkStatuses []nettypes.NetworkStatus, predicate func(nettypes.NetworkStatus) bool) *nettypes.NetworkStatus {
	for i, networkStatus := range networkStatuses {
		if predicate(networkStatus) {
			return &networkStatuses[i]
		}
	}
	return nil
}

func secondaryIfaceIPValue(pod *core.Pod, netStatus []nettypes.NetworkStatus) string {
	podNetStatus, found := pod.Annotations[nettypes.NetworkStatusAnnot]
	Expect(found).To(BeTrue(), "expected the pod to have a `networks-status` annotation")
	Expect(json.Unmarshal([]byte(podNetStatus), &netStatus)).To(Succeed())
	Expect(netStatus).NotTo(BeEmpty())
	// Check if interface is net1 and if IP is in range
	secondaryInterfaceNetworkStatus := filterNetworkStatus(netStatus, func(status nettypes.NetworkStatus) bool {
		return status.Interface == "net1"
	})
	Expect(secondaryInterfaceNetworkStatus.IPs).NotTo(BeEmpty())
	secondaryIfaceIP := secondaryInterfaceNetworkStatus.IPs[0]

	return secondaryIfaceIP
}

func inRange(cidr string, ip string) bool {
	_, cidrRange, err := net.ParseCIDR(cidr)
	Expect(err).To(BeNil())
	ipInRangeCandidate := net.ParseIP(ip)

	return cidrRange.Contains(ipInRangeCandidate)
}
