package controlloop

import (
	"fmt"
	"time"

	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/util/wait"
	v1coreinformerfactory "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
)

const (
	ipReconcilerQueueName = "pod-updates"
	syncPeriod            = time.Second * 5
)

type PodController struct {
	arePodsSynched          cache.InformerSynced
	areIPPoolsSynched       cache.InformerSynced
	areNetAttachDefsSynched cache.InformerSynced
	podsInformer            cache.SharedIndexInformer
	ipPoolInformer          cache.SharedIndexInformer
	netAttachDefInformer    cache.SharedIndexInformer
	broadcaster             record.EventBroadcaster
	recorder                record.EventRecorder
	workqueue               workqueue.RateLimitingInterface
	handler                 *handler
}

// NewPodController ...
func NewPodController(k8sCoreInformerFactory v1coreinformerfactory.SharedInformerFactory, wbSharedInformerFactory wbinformers.SharedInformerFactory, netAttachDefInformerFactory nadinformers.SharedInformerFactory, broadcaster record.EventBroadcaster, recorder record.EventRecorder) *PodController {
	return newPodController(k8sCoreInformerFactory, wbSharedInformerFactory, netAttachDefInformerFactory, broadcaster, recorder, wbclient.IPManagement)
}

func newPodController(k8sCoreInformerFactory v1coreinformerfactory.SharedInformerFactory, wbSharedInformerFactory wbinformers.SharedInformerFactory, netAttachDefInformerFactory nadinformers.SharedInformerFactory, broadcaster record.EventBroadcaster, recorder record.EventRecorder, cleanupFunc gargageCollector) *PodController {
	k8sPodFilteredInformer := k8sCoreInformerFactory.Core().V1().Pods().Informer()
	ipPoolInformer := wbSharedInformerFactory.Whereabouts().V1alpha1().IPPools()
	netAttachDefInformer := netAttachDefInformerFactory.K8sCniCncfIo().V1().NetworkAttachmentDefinitions()

	poolInformer := ipPoolInformer.Informer()
	networksInformer := netAttachDefInformer.Informer()
	netAttachDefLister := netAttachDefInformer.Lister()
	ipPoolLister := ipPoolInformer.Lister()

	deleteFuncHanler := handler{
		netAttachDefLister: netAttachDefLister,
		ipPoolsLister:      ipPoolLister,
		cleanupFunc:        cleanupFunc,
	}

	k8sPodFilteredInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			DeleteFunc: deleteFuncHanler.deletePodHandler,
		})

	return &PodController{
		arePodsSynched:          k8sPodFilteredInformer.HasSynced,
		areIPPoolsSynched:       poolInformer.HasSynced,
		areNetAttachDefsSynched: networksInformer.HasSynced,
		broadcaster:             broadcaster,
		recorder:                recorder,
		podsInformer:            k8sPodFilteredInformer,
		ipPoolInformer:          poolInformer,
		netAttachDefInformer:    networksInformer,
		workqueue: workqueue.NewNamedRateLimitingQueue(
			workqueue.DefaultControllerRateLimiter(),
			ipReconcilerQueueName),
		handler: &deleteFuncHanler,
	}
}

func podID(podNamespace string, podName string) string {
	return fmt.Sprintf("%s/%s", podNamespace, podName)
}

// Start runs worker thread after performing cache synchronization
func (pc *PodController) Start(stopChan <-chan struct{}) {
	logging.Verbosef("starting network controller")
	defer pc.workqueue.ShutDown()

	if ok := cache.WaitForCacheSync(stopChan, pc.arePodsSynched, pc.areNetAttachDefsSynched, pc.areIPPoolsSynched); !ok {
		logging.Verbosef("failed waiting for caches to sync")
	}

	go wait.Until(pc.worker, time.Second, stopChan)

	<-stopChan
	logging.Verbosef("shutting down network controller")
}

func (pc *PodController) worker() {
	for pc.processNextWorkItem() {
	}
}

func (pc *PodController) processNextWorkItem() bool {
	key, shouldQuit := pc.workqueue.Get()
	if shouldQuit {
		return false
	}
	defer pc.workqueue.Done(key)

	return true
}
