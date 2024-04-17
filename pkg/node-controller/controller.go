package node_controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	cncfV1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions/k8s.cni.cncf.io/v1"
	nadlisters "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/listers/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	clientset "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	whereaboutsInformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions/whereabouts.cni.cncf.io/v1alpha1"
	whereaboutsListers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/listers/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/iphelpers"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const controllerAgentName = "node-controller"

const (
	whereaboutsConfigPath = "/etc/cni/net.d/whereabouts.d/whereabouts.conf"
)

// Controller is the controller implementation for Foo resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	whereaboutsclientset clientset.Interface

	nadclientset nadclient.Interface

	nodeLister   corelisters.NodeLister
	nodeInformer coreinformers.NodeInformer
	nodesSynced  cache.InformerSynced

	nodeSlicePoolLister   whereaboutsListers.NodeSlicePoolLister
	nodeSlicePoolInformer whereaboutsInformers.NodeSlicePoolInformer
	nodeSlicePoolSynced   cache.InformerSynced

	nadInformer nadinformers.NetworkAttachmentDefinitionInformer
	nadLister   nadlisters.NetworkAttachmentDefinitionLister
	nadSynced   cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder

	//For testing, sort nodes before assigning to get consistent return values
	sortResults bool
}

// NewController returns a new sample controller
func NewController(
	ctx context.Context,
	kubeclientset kubernetes.Interface,
	whereaboutsclientset clientset.Interface,
	nadclientset nadclient.Interface,
	nodeInformer coreinformers.NodeInformer,
	nodeSlicePoolInformer whereaboutsInformers.NodeSlicePoolInformer,
	nadInformer nadinformers.NetworkAttachmentDefinitionInformer,
	sortResults bool,
) *Controller {
	logger := klog.FromContext(ctx)

	logger.V(4).Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})
	ratelimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 1000*time.Second),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)

	c := &Controller{
		kubeclientset:         kubeclientset,
		nodeLister:            nodeInformer.Lister(),
		nodeInformer:          nodeInformer,
		nodesSynced:           nodeInformer.Informer().HasSynced,
		whereaboutsclientset:  whereaboutsclientset,
		nodeSlicePoolLister:   nodeSlicePoolInformer.Lister(),
		nodeSlicePoolInformer: nodeSlicePoolInformer,
		nodeSlicePoolSynced:   nodeSlicePoolInformer.Informer().HasSynced,
		nadclientset:          nadclientset,
		nadInformer:           nadInformer,
		nadLister:             nadInformer.Lister(),
		nadSynced:             nadInformer.Informer().HasSynced,
		workqueue:             workqueue.NewRateLimitingQueue(ratelimiter),
		recorder:              recorder,
		sortResults:           sortResults,
	}

	logger.Info("Setting up event handlers")

	nadInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.onNadEvent,
		UpdateFunc: func(old, cur interface{}) {
			c.onNadEvent(cur)
		},
		DeleteFunc: c.onNadEvent,
	})

	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.requeueNADs,
		UpdateFunc: func(old, cur interface{}) {
			c.requeueNADs(cur)
		},
		DeleteFunc: c.requeueNADs,
	})

	nodeSlicePoolInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.requeueNADs,
		UpdateFunc: func(old, cur interface{}) {
			c.requeueNADs(cur)
		},
		DeleteFunc: c.requeueNADs,
	})

	return c
}

func (c *Controller) onNadEvent(obj interface{}) {
	klog.Infof("handling network attachment definition event")
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
	}
	key, err := cache.MetaNamespaceKeyFunc(object)
	klog.Info(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}
	c.workqueue.Add(key)
}

// TODO: we may want to require nodes to have an annotation similar to what pods have to receive a slice
// in this case we get all applicable NADs for the node rather than requeuing all
// same applies to other node event handlers
func (c *Controller) requeueNADs(obj interface{}) {
	nadlist, err := c.nadLister.List(labels.Everything())
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get network-attachment-definition list from informer: %v", err))
		return
	}
	for _, nad := range nadlist {
		key, err := cache.MetaNamespaceKeyFunc(nad)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", nad, err))
			return
		}
		c.workqueue.Add(key)
	}
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()
	logger := klog.FromContext(ctx)

	// Start the informer factories to begin populating the informer caches
	logger.Info("Starting node-slice controller")

	// Wait for the caches to be synced before starting workers
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.nodesSynced); !ok {
		return fmt.Errorf("failed to wait for nodes caches to sync")
	}
	if ok := cache.WaitForCacheSync(ctx.Done(), c.nodeSlicePoolSynced); !ok {
		return fmt.Errorf("failed to wait for nodeslices caches to sync")
	}
	if ok := cache.WaitForCacheSync(ctx.Done(), c.nadSynced); !ok {
		return fmt.Errorf("failed to wait for nad caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	// Launch two workers to process Foo resources
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.workqueue.Get()
	logger := klog.FromContext(ctx)

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		if err := c.syncHandler(ctx, key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		logger.Info("Successfully synced", "resourceName", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Foo resource
// with the current status of the resource.
func (c *Controller) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	err = c.checkForMultiNadMismatch(name, namespace)
	if err != nil {
		return err
	}

	nad, err := c.nadLister.NetworkAttachmentDefinitions(namespace).Get(name)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		// in this case the nad dne so it must've been deleted so we will cleanup nodeslicepools
		// if we are down during the delete this could be missed similar to endpoints see kubernetes #6877
		nodeSlices, err := c.nodeSlicePoolLister.List(labels.Everything())
		if err != nil {
			return nil
		}
		for _, nodeSlice := range nodeSlices {
			if hasOwnerRef(nodeSlice, name) {
				if len(nodeSlice.OwnerReferences) == 1 {
					//this is the last NAD owning this so delete
					err = c.whereaboutsclientset.WhereaboutsV1alpha1().NodeSlicePools(namespace).Delete(ctx, name, metav1.DeleteOptions{})
					if err != nil && !errors.IsNotFound(err) {
						return err
					}
				}
			}
		}
		return nil
	}
	//nad does exist so did it change node_slice_range or slice_size
	ipamConf, err := ipamConfiguration(nad, "")
	if err != nil {
		return err
	}

	// This is to support several NADs and interfaces on the same network
	logger.Info(fmt.Sprintf("%v", ipamConf))
	logger.Info(fmt.Sprintf("slicesize: %v", ipamConf.NodeSliceSize))
	if ipamConf.NodeSliceSize == "" || len(ipamConf.IPRanges) == 0 {
		logger.Info("skipping update node slices for network-attachment-definition due missing node slice or range configurations",
			"network-attachment-definition", klog.KRef(namespace, name))
		return nil
	}

	logger.Info("About to update node slices for network-attachment-definition",
		"network-attachment-definition", klog.KRef(namespace, name))

	currentNodeSlicePool, err := c.nodeSlicePoolLister.NodeSlicePools(namespace).Get(getSliceName(ipamConf))
	if err != nil {
		logger.Info("node slice pool does not exist, creating")
		if !errors.IsNotFound(err) {
			return err
		}
		//Create
		nodeslice := &v1alpha1.NodeSlicePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodeSlicePool",
				APIVersion: "whereabouts.cni.cncf.io/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      getSliceName(ipamConf),
				Namespace: namespace,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(nad, cncfV1.SchemeGroupVersion.WithKind("NetworkAttachmentDefinition")),
				},
			},
			// only supports single range with node slice
			Spec: v1alpha1.NodeSlicePoolSpec{
				Range:     ipamConf.IPRanges[0].Range,
				SliceSize: ipamConf.NodeSliceSize,
			},
		}
		allocations := []v1alpha1.NodeSliceAllocation{}
		logger.Info(fmt.Sprintf("node slice: %v\n", nodeslice))

		//TODO: handle case when full, we could fire an event
		subnets, err := iphelpers.DivideRangeBySize(nodeslice.Spec.Range, ipamConf.NodeSliceSize)
		if err != nil {
			return err
		}
		logger.Info(fmt.Sprintf("subnets: %v\n", subnets))
		for _, subnet := range subnets {
			allocations = append(allocations, v1alpha1.NodeSliceAllocation{
				SliceRange: subnet,
			})
		}
		nodes, err := c.getNodeList()
		if err != nil {
			return err
		}
		for _, node := range nodes {
			logger.Info(fmt.Sprintf("assigning node to slice: %v\n", node.Name))
			assignNodeToSlice(allocations, node.Name)
		}
		nodeslice.Status = v1alpha1.NodeSlicePoolStatus{
			Allocations: allocations,
		}
		logger.Info(fmt.Sprintf("final allocations: %v\n", allocations))
		_, err = c.whereaboutsclientset.WhereaboutsV1alpha1().NodeSlicePools(namespace).Create(ctx, nodeslice, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		nodeslice := currentNodeSlicePool.DeepCopy()
		// make sure if multiple NADs act on this NodeSlicePool they are all listed as owners
		nadIsOwner := false
		for _, ownerRef := range nodeslice.OwnerReferences {
			if ownerRef.Name == name {
				nadIsOwner = true
			}
		}
		if !nadIsOwner {
			nodeslice.OwnerReferences = append(nodeslice.OwnerReferences, getAuxiliaryOwnerRef(nad))
		}
		logger.Info(fmt.Sprintf("owner references: %v\n", nodeslice.OwnerReferences))
		// node slice currently exists
		if currentNodeSlicePool.Spec.SliceSize != ipamConf.NodeSliceSize ||
			currentNodeSlicePool.Spec.Range != ipamConf.IPRanges[0].Range {
			logger.Info("network-attachment-definition range or slice size changed, re-allocating node slices")
			// slices have changed so redo the slicing and reassign nodes
			subnets, err := iphelpers.DivideRangeBySize(ipamConf.Range, ipamConf.NodeSliceSize)
			if err != nil {
				return err
			}

			allocations := []v1alpha1.NodeSliceAllocation{}
			for _, subnet := range subnets {
				allocations = append(allocations, v1alpha1.NodeSliceAllocation{
					SliceRange: subnet,
				})
			}
			nodes, err := c.getNodeList()
			if err != nil {
				return err
			}
			for _, node := range nodes {
				assignNodeToSlice(allocations, node.Name)
			}

			nodeslice.Status = v1alpha1.NodeSlicePoolStatus{
				Allocations: allocations,
			}
			_, err = c.whereaboutsclientset.WhereaboutsV1alpha1().NodeSlicePools(namespace).Update(ctx, nodeslice, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		} else {
			logger.Info("node slice exists and range configuration did not change, ensuring nodes assigned")
			//slices have not changed so only make sure all nodes are assigned
			allocations := nodeslice.Status.Allocations
			nodes, err := c.getNodeList()
			if err != nil {
				return err
			}
			for _, node := range nodes {
				assignNodeToSlice(allocations, node.Name)
			}
			removeUnusedNodes(allocations, nodes)
			nodeslice.Status.Allocations = allocations

			_, err = c.whereaboutsclientset.WhereaboutsV1alpha1().NodeSlicePools(namespace).Update(context.TODO(), nodeslice, metav1.UpdateOptions{})
			if err != nil {
				logger.Info(fmt.Sprintf("Error updating NSP with no changes: %v", err))
				return err
			}
		}
	}

	//TODO: recorder events
	//c.recorder.Event(foo, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) getNodeList() ([]*corev1.Node, error) {
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	if !c.sortResults {
		return nodes, nil
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
	return nodes, nil
}

// since multiple NADs can be attached to the same BE Network, we need to make sure their settings match in this case
func (c *Controller) checkForMultiNadMismatch(name, namespace string) error {
	nad, err := c.nadLister.NetworkAttachmentDefinitions(namespace).Get(name)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		return nil
	}
	ipamConf, err := ipamConfiguration(nad, "")
	if err != nil {
		return err
	}

	nadList, err := c.nadLister.List(labels.Everything())
	if err != nil {
		return err
	}
	for _, additionalNad := range nadList {
		additionalIpamConf, err := ipamConfiguration(additionalNad, "")
		if err != nil {
			return err
		}
		if !checkIpamConfMatch(ipamConf, additionalIpamConf) {
			return fmt.Errorf("found IPAM conf mismatch for network-attachment-definitions with same network name")
		}
	}
	return nil
}

func checkIpamConfMatch(conf1, conf2 *types.IPAMConfig) bool {
	if conf1.NetworkName == conf2.NetworkName {
		return conf1.IPRanges[0].Range == conf2.IPRanges[0].Range && conf1.NodeSliceSize == conf2.NodeSliceSize
	}
	return true
}

func hasOwnerRef(nodeSlice *v1alpha1.NodeSlicePool, name string) bool {
	for _, ownerRef := range nodeSlice.OwnerReferences {
		if ownerRef.Name == name {
			return true
		}
	}
	return false
}

func getSliceName(ipamConf *types.IPAMConfig) string {
	sliceName := ipamConf.Name
	if ipamConf.NetworkName != "" {
		sliceName = ipamConf.NetworkName
	}
	return sliceName
}

// since multiple nads can share a nodeslicepool we need to set multiple owner refs but only
// one controller owner ref
func getAuxiliaryOwnerRef(nad *cncfV1.NetworkAttachmentDefinition) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: nad.APIVersion,
		Kind:       nad.Kind,
		Name:       nad.Name,
		UID:        nad.UID,
	}
}

func removeUnusedNodes(allocations []v1alpha1.NodeSliceAllocation, nodes []*corev1.Node) {
	//create map for fast lookup, we only care about keys so use empty struct b/c takes up no memory
	nodeMap := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeMap[node.Name] = struct{}{}
	}
	for i, allocation := range allocations {
		if allocation.NodeName != "" {
			if _, ok := nodeMap[allocation.NodeName]; !ok {
				allocations[i] = v1alpha1.NodeSliceAllocation{
					SliceRange: allocation.SliceRange,
				}
			}
		}
	}
}

func ipamConfiguration(nad *cncfV1.NetworkAttachmentDefinition, mountPath string) (*types.IPAMConfig, error) {
	mounterWhereaboutsConfigFilePath := mountPath + whereaboutsConfigPath

	ipamConfig, err := config.LoadIPAMConfiguration([]byte(nad.Spec.Config), "", mounterWhereaboutsConfigFilePath)
	if err != nil {
		return nil, err
	}
	return ipamConfig, nil
}

func assignNodeToSlice(allocations []v1alpha1.NodeSliceAllocation, nodeName string) {
	if nodeHasAllocation(allocations, nodeName) {
		return
	}
	for i, allocation := range allocations {
		if allocation.NodeName == "" {
			allocations[i] = v1alpha1.NodeSliceAllocation{
				SliceRange: allocation.SliceRange,
				NodeName:   nodeName,
			}
			return
		}
	}
}

func nodeHasAllocation(allocations []v1alpha1.NodeSliceAllocation, nodeName string) bool {
	for _, allocation := range allocations {
		if allocation.NodeName == nodeName {
			return true
		}
	}
	return false
}
