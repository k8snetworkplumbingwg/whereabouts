package whereabouts_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/typed/k8s.cni.cncf.io/v1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
	kubeClient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"

	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	wbLabelEqual     = "tier=whereabouts-scale-test"
	testNamespace    = "kube-system"
	rsCreateTimeout  = 600 * time.Second
	podCreateTimeout = 10 * time.Second
	podDeleteTimeout = 2 * podCreateTimeout
	testNetworkName  = "wa-nad"
	testImage        = "quay.io/dougbtv/alpine:latest"
	rsName           = "whereabouts-scale-test"
	ipPoolName       = "10.10.0.0/16"
)

func TestWhereaboutsE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "whereabouts-e2e")
}

var _ = Describe("Whereabouts functionality", func() {
	Context("Test setup", func() {
		var (
			clientInfo   *ClientInfo
			envVars      *envVars
			netAttachDef *nettypes.NetworkAttachmentDefinition
			pod          *core.Pod
			replicaSet   *apps.ReplicaSet
		)

		BeforeEach(func() {
			var (
				config *rest.Config
				err    error
			)

			envVars, err = environment()
			Expect(err).NotTo(HaveOccurred())

			config, err = clusterConfig(envVars.kubeconfigPath)
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
				const singlePodName = "whereabouts-basic-test"
				var err error

				By("creating a pod with whereabouts net-attach-def")
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
				const ipv4TestRange = "10.10.0.0/16"

				By("checking pod IP is within whereabouts IPAM range")
				secondaryIfaceIP, err := secondaryIfaceIPValue(pod)
				Expect(err).NotTo(HaveOccurred())
				Expect(inRange(ipv4TestRange, secondaryIfaceIP)).To(Succeed())
			})
		})

		Context("Replicaset tests", func() {

			const (
				emptyReplicaSet = 0
				rsSteadyTimeout = 2 * rsCreateTimeout
			)

			var k8sIPAM *kubeClient.KubernetesIPAM

			BeforeEach(func() {
				By("creating a replicaset with whereabouts net-attach-def")
				var err error

				k8sIPAM, err = kubeClient.NewKubernetesIPAMWithNamespace("", types.IPAMConfig{
					Kubernetes: types.KubernetesConfig{
						KubeConfigPath: envVars.kubeconfigPath,
					},
				}, testNamespace)
				Expect(err).NotTo(HaveOccurred())

				replicaSet, err = clientInfo.provisionReplicaSet(
					emptyReplicaSet,
					podTierLabel(rsName),
					podNetworkSelectionElements(testNetworkName),
				)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("removing replicas and expecting 0 IP pool allocations")
				Expect(checkZeroIPPoolAllocationsAndReplicas(clientInfo, k8sIPAM, rsName, testNamespace)).To(Succeed())

				By("deleting replicaset with whereabouts net-attach-def")
				Expect(clientInfo.deleteReplicaSet(replicaSet)).To(Succeed())
			})

			It("allocates each IP pool entry with a unique pod IP", func() {
				By("creating max number of pods and checking IP Pool validity")
				for i := 0; i < envVars.numThrashIter; i++ {
					Expect(checkZeroIPPoolAllocationsAndReplicas(clientInfo, k8sIPAM, rsName, testNamespace)).To(Succeed())

					// Increase to desired number of replicas for test
					allPods, err := clientInfo.Client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
					Expect(err).NotTo(HaveOccurred())

					replicaSet, err = clientInfo.updateReplicaSet(replicaSetObject(
						envVars.maxReplicas(allPods.Items),
						podTierLabel(rsName),
						podNetworkSelectionElements(testNetworkName),
					))
					Expect(err).NotTo(HaveOccurred())

					Expect(WaitForReplicaSetSteadyState(clientInfo.Client, testNamespace, wbLabelEqual, replicaSet, rsSteadyTimeout)).To(Succeed())

					Expect(iPPoolConsistency(envVars.kubeconfigPath, k8sIPAM, clientInfo.Client)).To(Succeed())
				}
			})
		})
	})
})

func iPPoolConsistency(kubeconfigPath string, k8sIPAM *kubeClient.KubernetesIPAM, cs *kubernetes.Clientset) error {
	By("checking if there are any stale IPs in IP pool or any IPs in IP pool that are not seen attached to a pod")

	By("Forcing reconciliation of the cluster...")
	if err := runIpReconciler(kubeconfigPath); err != nil {
		return err
	}

	By("Comparing pod ip assignments to ippool reservations")
	ipPool, err := k8sIPAM.GetIPPool(context.Background(), ipPoolName)
	if err != nil {
		return err
	}

	ipPoolAllocations := ipPool.Allocations()
	podList, err := ListPods(cs, testNamespace, wbLabelEqual)
	if err != nil {
		return err
	}

	if err := findStaleIPAddresses(ipPoolAllocations, podList); err != nil {
		return err
	}

	if err := findMissingAllocations(ipPoolAllocations, podList); err != nil {
		return err
	}

	return nil
}

func findStaleIPAddresses(ipPoolAllocations []types.IPReservation, podList *core.PodList) error {
	var found bool
	var podIP, reservedIP string
	var err error

	for _, allocation := range ipPoolAllocations {
		found = false
		for _, pod := range podList.Items {
			podIP, err = secondaryIfaceIPValue(&pod)
			if err != nil {
				return err
			}
			reservedIP = allocation.IP.String()

			if reservedIP == podIP {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("Possible stale IP Pool: failed to find pod for IP Pool IP %s\n", allocation.IP)
		}
	}

	return nil
}

func findMissingAllocations(ipPoolAllocations []types.IPReservation, podList *core.PodList) error {
	var found bool
	var podIP, reservedIP string
	var err error

	for _, pod := range podList.Items {
		found = false
		for _, allocation := range ipPoolAllocations {
			podIP, err = secondaryIfaceIPValue(&pod)
			if err != nil {
				return err
			}
			reservedIP = allocation.IP.String()

			if reservedIP == podIP {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("possible pod IP not recorded in IP pool: failed to find IP pool allocation for pod IP %s\n", podIP)
		}
	}

	return nil
}

type envVars struct {
	kubeconfigPath      string
	numComputeNodes     int
	fillPercentCapacity int
	numThrashIter       int
}

func environment() (*envVars, error) {
	var err error

	kubeconfigPath := kubeConfig()
	numComputeNodes, err := computeNodes()
	if err != nil {
		return nil, err
	}
	fillPercentCapacity, err := fillPercent()
	if err != nil {
		return nil, err
	}
	numThrashIter, err := thrashIter()
	if err != nil {
		return nil, err
	}

	return &envVars{
		kubeconfigPath:      kubeconfigPath,
		numComputeNodes:     numComputeNodes,
		fillPercentCapacity: fillPercentCapacity,
		numThrashIter:       numThrashIter,
	}, nil
}

func kubeConfig() string {
	const kubeconfig = "KUBECONFIG"
	kubeconfigPath, found := os.LookupEnv(kubeconfig)
	if !found {
		kubeconfigPath = "${HOME}/.kube/config"
	}
	return kubeconfigPath
}

func computeNodes() (int, error) {
	const numCompute = "NUMBER_OF_COMPUTE_NODES"
	numComputeNodes, found := os.LookupEnv(numCompute)
	if !found {
		numComputeNodes = "2"
	}
	return strconv.Atoi(numComputeNodes)
}

func fillPercent() (int, error) {
	const fillCapcity = "FILL_PERCENT_CAPACITY"
	fillPercentCapacity, found := os.LookupEnv(fillCapcity)
	if !found {
		fillPercentCapacity = "50"
	}
	return strconv.Atoi(fillPercentCapacity)
}

func thrashIter() (int, error) {
	const numThrash = "NUMBER_OF_THRASH_ITER"
	numThrashIter, found := os.LookupEnv(numThrash)
	if !found {
		numThrashIter = "1"
	}
	return strconv.Atoi(numThrashIter)
}

func clusterConfig(kubeconfigPath string) (*rest.Config, error) {
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

	return &ClientInfo{
		Client:    clientSet,
		NetClient: netClient,
	}, nil
}

func (c *ClientInfo) addNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) (*nettypes.NetworkAttachmentDefinition, error) {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Create(context.TODO(), netattach, metav1.CreateOptions{})
}

func (c *ClientInfo) delNetAttachDef(netattach *nettypes.NetworkAttachmentDefinition) error {
	return c.NetClient.NetworkAttachmentDefinitions(netattach.ObjectMeta.Namespace).Delete(context.TODO(), netattach.Name, metav1.DeleteOptions{})
}

func (c *ClientInfo) provisionPod(label, annotations map[string]string) (*core.Pod, error) {
	pod := podObject(label, annotations)
	pod, err := c.Client.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := WaitForPodReady(c.Client, pod.Namespace, pod.Name, podCreateTimeout); err != nil {
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

	if err := WaitForPodToDisappear(c.Client, pod.GetNamespace(), pod.GetName(), podDeleteTimeout); err != nil {
		return err
	}
	return nil
}

func (c *ClientInfo) provisionReplicaSet(replicaCount int32, label, annotations map[string]string) (*apps.ReplicaSet, error) {
	replicaSet, err := c.Client.AppsV1().ReplicaSets(testNamespace).Create(
		context.Background(),
		replicaSetObject(replicaCount, label, annotations),
		metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := WaitForPodBySelector(c.Client, testNamespace, wbLabelEqual, rsCreateTimeout); err != nil {
		return nil, err
	}

	replicaSet, err = c.Client.AppsV1().ReplicaSets(testNamespace).Get(context.Background(), replicaSet.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return replicaSet, nil
}

func (c *ClientInfo) updateReplicaSet(replicaSet *apps.ReplicaSet) (*apps.ReplicaSet, error) {
	replicaSet, err := c.Client.AppsV1().ReplicaSets(testNamespace).Update(context.Background(), replicaSet, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}
	return replicaSet, nil
}

func (c *ClientInfo) deleteReplicaSet(replicaSet *apps.ReplicaSet) error {
	const rsDeleteTimeout = 2 * rsCreateTimeout
	if err := c.Client.AppsV1().ReplicaSets(testNamespace).Delete(context.Background(), replicaSet.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	if err := WaitForReplicaSetToDisappear(c.Client, replicaSet.GetNamespace(), replicaSet.GetName(), rsDeleteTimeout); err != nil {
		return err
	}
	return nil
}

func (v envVars) maxReplicas(allPods []core.Pod) int32 {
	const maxPodsPerNode = 110
	return int32(
		(v.numComputeNodes*maxPodsPerNode - (len(allPods))) * v.fillPercentCapacity / 100)
}

// Waits for all replicas to be fully removed from replicaset, and checks that there are 0 ip pool allocations
func checkZeroIPPoolAllocationsAndReplicas(clientInfo *ClientInfo, k8sIPAM *kubeClient.KubernetesIPAM, rsName, testNamespace string) error {
	const (
		emptyReplicaSet   = 0
		rsSteadyTimeout   = 2 * rsCreateTimeout
		zeroIPPoolTimeout = 2 * time.Minute
	)
	var err error

	replicaSet, err := clientInfo.updateReplicaSet(replicaSetObject(
		emptyReplicaSet,
		podTierLabel(rsName),
		podNetworkSelectionElements(testNetworkName),
	))
	if err != nil {
		return err
	}

	if err = WaitForReplicaSetSteadyState(clientInfo.Client, testNamespace, wbLabelEqual, replicaSet, rsSteadyTimeout); err != nil {
		return err
	}

	if err = WaitForZeroIPPoolAllocations(k8sIPAM, ipPoolName, zeroIPPoolTimeout); err != nil {
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

func replicaSetObject(replicaCount int32, label map[string]string, annotations map[string]string) *apps.ReplicaSet {
	numReplicas := &replicaCount

	return &apps.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ReplicaSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rsName,
			Namespace: testNamespace,
			Labels:    label,
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: numReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: label,
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      label,
					Annotations: annotations,
					Namespace:   testNamespace,
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
			},
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

func runIpReconciler(kubeconfigPath string) error {
	const defaultReconcilerTimeout = 30

	var err error
	var ipReconcileLoop *reconciler.ReconcileLooper

	logLevel := "error"

	logging.SetLogLevel(logLevel)

	ipReconcileLoop, err = reconciler.NewReconcileLooperWithKubeconfig(context.Background(), kubeconfigPath, defaultReconcilerTimeout)
	if err != nil {
		return fmt.Errorf("failed to create the reconcile looper: %v", err)
	}

	_, err = ipReconcileLoop.ReconcileIPPools(context.Background())
	if err != nil {
		return fmt.Errorf("failed to clean up IP for allocations: %v", err)
	}

	if err := ipReconcileLoop.ReconcileOverlappingIPAddresses(context.Background()); err != nil {
		return fmt.Errorf("failed to reconcile clusterwide IPs: %v", err)
	}
	return nil
}
