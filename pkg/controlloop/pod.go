package controlloop

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	v1coreinformerfactory "k8s.io/client-go/informers"
	v1corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"
	nadlister "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/listers/k8s.cni.cncf.io/v1"
	"github.com/pkg/errors"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/allocate"
	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	wbclientset "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	wblister "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/listers/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const (
	defaultMountPath      = "/host"
	ipReconcilerQueueName = "pod-updates"
	syncPeriod            = time.Second
	whereaboutsConfigPath = "/etc/cni/net.d/whereabouts.d/whereabouts.conf"
	maxRetries            = 2
)

const (
	addressGarbageCollected        = "IPAddressGarbageCollected"
	addressGarbageCollectionFailed = "IPAddressGarbageCollectionFailed"
)

const (
	podControllerFilterKey           = "spec.nodeName"
	podControllerNodeNameEnvVariable = "NODENAME"
	noResyncPeriod                   = 0
)

type garbageCollector func(ctx context.Context, mode int, ipamConf types.IPAMConfig, client *wbclient.KubernetesIPAM) ([]net.IPNet, error)

type PodController struct {
	k8sClient               kubernetes.Interface
	wbClient                wbclientset.Interface
	arePodsSynched          cache.InformerSynced
	areIPPoolsSynched       cache.InformerSynced
	areNetAttachDefsSynched cache.InformerSynced
	podsInformer            cache.SharedIndexInformer
	ipPoolInformer          cache.SharedIndexInformer
	netAttachDefInformer    cache.SharedIndexInformer
	podLister               v1corelisters.PodLister
	ipPoolLister            wblister.IPPoolLister
	netAttachDefLister      nadlister.NetworkAttachmentDefinitionLister
	broadcaster             record.EventBroadcaster
	recorder                record.EventRecorder
	workqueue               workqueue.RateLimitingInterface
	mountPath               string
	cleanupFunc             garbageCollector
}

// NewPodController ...
func NewPodController(k8sCoreClient kubernetes.Interface, wbClient wbclientset.Interface, k8sCoreInformerFactory v1coreinformerfactory.SharedInformerFactory, wbSharedInformerFactory wbinformers.SharedInformerFactory, netAttachDefInformerFactory nadinformers.SharedInformerFactory, broadcaster record.EventBroadcaster, recorder record.EventRecorder) *PodController {
	return newPodController(k8sCoreClient, wbClient, k8sCoreInformerFactory, wbSharedInformerFactory, netAttachDefInformerFactory, broadcaster, recorder, wbclient.IPManagement)
}

// PodInformerFactory is a wrapper around NewSharedInformerFactoryWithOptions. Before returning the informer, it will
// extract the node name from environment variable "NODENAME". It will then try to look up the node with the given name.
// On success, it will create an informer that filters all pods with spec.nodeName == <value of env NODENAME>.
func PodInformerFactory(k8sClientSet kubernetes.Interface) (v1coreinformerfactory.SharedInformerFactory, error) {
	nodeName := os.Getenv(podControllerNodeNameEnvVariable)
	logging.Debugf("Filtering pods with filter key '%s' and filter value '%s'", podControllerFilterKey, nodeName)
	if _, err := k8sClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{}); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("Could not find node with node name '%s'.", nodeName))
	}
	return v1coreinformerfactory.NewSharedInformerFactoryWithOptions(
		k8sClientSet, noResyncPeriod, v1coreinformerfactory.WithTweakListOptions(
			func(options *metav1.ListOptions) {
				options.FieldSelector = fields.OneTermEqualSelector(podControllerFilterKey, nodeName).String()
			})), nil
}

func newPodController(k8sCoreClient kubernetes.Interface, wbClient wbclientset.Interface, k8sCoreInformerFactory v1coreinformerfactory.SharedInformerFactory, wbSharedInformerFactory wbinformers.SharedInformerFactory, netAttachDefInformerFactory nadinformers.SharedInformerFactory, broadcaster record.EventBroadcaster, recorder record.EventRecorder, cleanupFunc garbageCollector) *PodController {
	k8sPodFilteredInformer := k8sCoreInformerFactory.Core().V1().Pods()
	ipPoolInformer := wbSharedInformerFactory.Whereabouts().V1alpha1().IPPools()
	netAttachDefInformer := netAttachDefInformerFactory.K8sCniCncfIo().V1().NetworkAttachmentDefinitions()

	poolInformer := ipPoolInformer.Informer()
	networksInformer := netAttachDefInformer.Informer()
	podsInformer := k8sPodFilteredInformer.Informer()

	queue := workqueue.NewNamedRateLimitingQueue(
		workqueue.DefaultControllerRateLimiter(),
		ipReconcilerQueueName)

	podsInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			DeleteFunc: func(obj interface{}) {
				onPodDelete(queue, obj)
			},
		})

	return &PodController{
		k8sClient:               k8sCoreClient,
		wbClient:                wbClient,
		arePodsSynched:          podsInformer.HasSynced,
		areIPPoolsSynched:       poolInformer.HasSynced,
		areNetAttachDefsSynched: networksInformer.HasSynced,
		broadcaster:             broadcaster,
		recorder:                recorder,
		podsInformer:            podsInformer,
		ipPoolInformer:          poolInformer,
		netAttachDefInformer:    networksInformer,
		podLister:               k8sPodFilteredInformer.Lister(),
		ipPoolLister:            ipPoolInformer.Lister(),
		netAttachDefLister:      netAttachDefInformer.Lister(),
		workqueue:               queue,
		cleanupFunc:             cleanupFunc,
	}
}

// Start runs worker thread after performing cache synchronization
func (pc *PodController) Start(stopChan <-chan struct{}) {
	logging.Verbosef("starting network controller")

	if ok := cache.WaitForCacheSync(stopChan, pc.arePodsSynched, pc.areNetAttachDefsSynched, pc.areIPPoolsSynched); !ok {
		logging.Verbosef("failed waiting for caches to sync")
	}

	go wait.Until(pc.worker, syncPeriod, stopChan)
}

// Shutdown stops the PodController worker queue
func (pc *PodController) Shutdown() {
	pc.workqueue.ShutDown()
}

func (pc *PodController) worker() {
	for pc.processNextWorkItem() {
	}
}

func (pc *PodController) processNextWorkItem() bool {
	queueItem, shouldQuit := pc.workqueue.Get()
	if shouldQuit {
		return false
	}
	defer pc.workqueue.Done(queueItem)

	pod := queueItem.(*v1.Pod)
	err := pc.garbageCollectPodIPs(pod)
	logging.Verbosef("result of garbage collecting pods: %+v", err)
	pc.handleResult(pod, err)

	return true
}

func (pc *PodController) garbageCollectPodIPs(pod *v1.Pod) error {
	podNamespace := pod.GetNamespace()
	podName := pod.GetName()

	ifaceStatuses, err := podNetworkStatus(pod)
	if err != nil {
		return fmt.Errorf("failed to access the network status for pod [%s/%s]: %v", podName, podNamespace, err)
	}

	for _, ifaceStatus := range ifaceStatuses {
		if ifaceStatus.Default {
			logging.Verbosef("skipped net-attach-def for default network")
			continue
		}
		nad, err := pc.ifaceNetAttachDef(ifaceStatus)
		if err != nil {
			return fmt.Errorf("failed to get network-attachment-definition for iface %s: %+v", ifaceStatus.Name, err)
		}

		mountPath := defaultMountPath
		if pc.mountPath != "" {
			mountPath = pc.mountPath
		}

		logging.Verbosef("the NAD's config: %s", nad.Spec)
		ipamConfig, err := ipamConfiguration(nad, podNamespace, podName, mountPath)
		if err != nil && isInvalidPluginType(err) {
			logging.Debugf("error while computing something: %v", err)
			continue
		} else if err != nil {
			return fmt.Errorf("failed to create an IPAM configuration for the pod %s iface %s: %+v", podID(podNamespace, podName), ifaceStatus.Name, err)
		}

		var pools []*whereaboutsv1alpha1.IPPool
		for _, rangeConfig := range ipamConfig.IPRanges {
			pool, err := pc.ipPool(rangeConfig.Range)

			if err != nil {
				return fmt.Errorf("failed to get the IPPool data: %+v", err)
			}

			logging.Verbosef("pool range [%s]", pool.Spec.Range)

			pools = append(pools, pool)
		}

		for _, pool := range pools {
			for allocationIndex, allocation := range pool.Spec.Allocations {
				if allocation.PodRef == podID(podNamespace, podName) {
					logging.Verbosef("stale allocation to cleanup: %+v", allocation)

					client := *wbclient.NewKubernetesClient(nil, pc.k8sClient, 0)
					wbClient := &wbclient.KubernetesIPAM{
						Client: client,
						Config: *ipamConfig,
					}

					if err != nil {
						logging.Debugf("error while generating the IPAM client: %v", err)
						continue
					}
					if _, err := pc.cleanupFunc(context.TODO(), types.Deallocate, *ipamConfig, wbClient); err != nil {
						logging.Errorf("failed to cleanup allocation: %v", err)
					}
					if err := pc.addressGarbageCollected(pod, nad.GetName(), pool.Spec.Range, allocationIndex); err != nil {
						logging.Errorf("failed to issue event for successful IP address cleanup: %v", err)
					}
				}
			}
		}
	}

	return nil
}

func isInvalidPluginType(err error) bool {
	_, isInvalidPluginError := err.(*config.InvalidPluginError)
	return isInvalidPluginError
}

func (pc *PodController) handleResult(pod *v1.Pod, err error) {
	if err == nil {
		pc.workqueue.Forget(pod)
		return
	}

	podNamespace := pod.GetNamespace()
	podName := pod.GetName()
	currentRetries := pc.workqueue.NumRequeues(pod)
	if currentRetries <= maxRetries {
		logging.Verbosef(
			"re-queuing IP address reconciliation request for pod %s; retry #: %d",
			podID(podNamespace, podName),
			currentRetries)
		pc.workqueue.AddRateLimited(pod)
		return
	}

	pc.addressGarbageCollectionFailed(pod, err)
}

func (pc *PodController) ifaceNetAttachDef(ifaceStatus nadv1.NetworkStatus) (*nadv1.NetworkAttachmentDefinition, error) {
	const (
		namespaceIndex = 0
		nameIndex      = 1
	)

	logging.Debugf("pod's network status: %+v", ifaceStatus)
	ifaceInfo := strings.Split(ifaceStatus.Name, "/")
	if len(ifaceInfo) < 2 {
		return nil, fmt.Errorf("pod %s name does not feature namespace/pod name syntax", ifaceStatus.Name)
	}

	netNamespaceName := ifaceInfo[namespaceIndex]
	netName := ifaceInfo[nameIndex]

	nad, err := pc.netAttachDefLister.NetworkAttachmentDefinitions(netNamespaceName).Get(netName)
	if err != nil {
		return nil, err
	}
	return nad, nil
}

func (pc *PodController) ipPool(cidr string) (*whereaboutsv1alpha1.IPPool, error) {
	pool, err := pc.ipPoolLister.IPPools(ipPoolsNamespace()).Get(wbclient.NormalizeRange(cidr))
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func (pc *PodController) addressGarbageCollected(pod *v1.Pod, networkName string, ipRange string, allocationIndex string) error {
	if pc.recorder != nil {
		ip, _, err := net.ParseCIDR(ipRange)
		if err != nil {
			return err
		}
		index, err := strconv.Atoi(allocationIndex)
		if err != nil {
			return err
		}
		pc.recorder.Eventf(
			pod,
			v1.EventTypeNormal,
			addressGarbageCollected,
			"successful cleanup of IP address [%s] from network %s",
			allocate.IPAddOffset(ip, uint64(index)),
			networkName)
	}
	return nil
}

func (pc *PodController) addressGarbageCollectionFailed(pod *v1.Pod, err error) {
	logging.Errorf(
		"dropping pod [%s] deletion out of the queue - could not reconcile IP: %+v",
		podID(pod.GetNamespace(), pod.GetName()),
		err)

	pc.workqueue.Forget(pod)

	if pc.recorder != nil {
		pc.recorder.Eventf(
			pod,
			v1.EventTypeWarning,
			addressGarbageCollectionFailed,
			"failed to garbage collect addresses for pod %s",
			podID(pod.GetNamespace(), pod.GetName()))
	}
}

func onPodDelete(queue workqueue.RateLimitingInterface, obj interface{}) {
	pod, err := podFromTombstone(obj)
	if err != nil {
		logging.Errorf("cannot create pod object from %v on pod delete: %v", obj, err)
		return
	}

	logging.Verbosef("deleted pod [%s]", podID(pod.GetNamespace(), pod.GetName()))
	queue.Add(stripPod(pod)) // we only need the pod's metadata & its network-status annotations. Hence we strip it.
}

func podID(podNamespace string, podName string) string {
	return fmt.Sprintf("%s/%s", podNamespace, podName)
}

func podNetworkStatus(pod *v1.Pod) ([]nadv1.NetworkStatus, error) {
	var ifaceStatuses []nadv1.NetworkStatus
	networkStatus, found := pod.Annotations[nadv1.NetworkStatusAnnot]
	if found {
		if err := json.Unmarshal([]byte(networkStatus), &ifaceStatuses); err != nil {
			return nil, err
		}
	}
	return ifaceStatuses, nil
}

func ipamConfiguration(nad *nadv1.NetworkAttachmentDefinition, podNamespace string, podName string, mountPath string) (*types.IPAMConfig, error) {
	mounterWhereaboutsConfigFilePath := mountPath + whereaboutsConfigPath

	ipamConfig, err := config.LoadIPAMConfiguration([]byte(nad.Spec.Config), "", mounterWhereaboutsConfigFilePath)
	if err != nil {
		return nil, err
	}
	ipamConfig.PodName = podName
	ipamConfig.PodNamespace = podNamespace
	ipamConfig.Kubernetes.KubeConfigPath = mountPath + ipamConfig.Kubernetes.KubeConfigPath // must use the mount path

	return ipamConfig, nil
}

func ipPoolsNamespace() string {
	const wbNamespaceEnvVariableName = "WHEREABOUTS_NAMESPACE"
	if wbNamespace, found := os.LookupEnv(wbNamespaceEnvVariableName); found {
		return wbNamespace
	}

	const wbDefaultNamespace = "kube-system"
	return wbDefaultNamespace
}

func podFromTombstone(obj interface{}) (*v1.Pod, error) {
	pod, isPod := obj.(*v1.Pod)
	if !isPod {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return nil, fmt.Errorf("received unexpected object: %v", obj)
		}
		pod, ok = tombstone.Obj.(*v1.Pod)
		if !ok {
			return nil, fmt.Errorf("deletedFinalStateUnknown contained non-Pod object: %v", tombstone.Obj)
		}
	}
	return pod, nil
}

func stripPod(pod *v1.Pod) *v1.Pod {
	newPod := pod.DeepCopy()
	newPod.Spec = v1.PodSpec{}
	newPod.Status = v1.PodStatus{}
	return newPod
}
