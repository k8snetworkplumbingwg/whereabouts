package controlloop

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	ipamclaimsv1alpha1 "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1"
	ipamclaimsclient "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1/apis/clientset/versioned"
	ipamclaimsinformers "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1/apis/informers/externalversions"

	wbclientset "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/storage/kubernetes"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// ClaimController releases Whereabouts allocations when an IPAMClaim is deleted.
type ClaimController struct {
	wbClient          wbclientset.Interface
	claimsClient      ipamclaimsclient.Interface
	areClaimsSynched  cache.InformerSynced
	areIPPoolsSynched cache.InformerSynced
	workqueue         workqueue.TypedRateLimitingInterface[string]
}

// NewClaimController builds a controller that watches IPAMClaim deletions.
func NewClaimController(
	wbClient wbclientset.Interface,
	claimsClient ipamclaimsclient.Interface,
	wbSharedInformerFactory wbinformers.SharedInformerFactory,
	claimsInformerFactory ipamclaimsinformers.SharedInformerFactory,
) *ClaimController {
	ipPoolInformer := wbSharedInformerFactory.Whereabouts().V1alpha1().IPPools().Informer()
	claimsSharedInformer := claimsInformerFactory.K8s().V1alpha1().IPAMClaims().Informer()

	queue := workqueue.NewTypedRateLimitingQueue[string](
		workqueue.DefaultTypedControllerRateLimiter[string]())

	claimsSharedInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			onClaimDelete(queue, obj)
		},
	})

	return &ClaimController{
		wbClient:          wbClient,
		claimsClient:      claimsClient,
		areClaimsSynched:  claimsSharedInformer.HasSynced,
		areIPPoolsSynched: ipPoolInformer.HasSynced,
		workqueue:         queue,
	}
}

// Start runs the claim controller worker after cache sync.
func (cc *ClaimController) Start(stopChan <-chan struct{}) {
	logging.Verbosef("starting IPAMClaim controller")
	if ok := cache.WaitForCacheSync(stopChan, cc.areClaimsSynched, cc.areIPPoolsSynched); !ok {
		logging.Verbosef("failed waiting for IPAMClaim caches to sync")
	}
	go wait.Until(cc.worker, syncPeriod, stopChan)
}

// Shutdown stops the claim workqueue.
func (cc *ClaimController) Shutdown() {
	cc.workqueue.ShutDown()
}

func (cc *ClaimController) worker() {
	for cc.processNextWorkItem() {
	}
}

func (cc *ClaimController) processNextWorkItem() bool {
	claimRef, shouldQuit := cc.workqueue.Get()
	if shouldQuit {
		return false
	}
	defer cc.workqueue.Done(claimRef)

	err := cc.releaseClaimAllocations(context.TODO(), claimRef)
	if err != nil {
		logging.Errorf("failed releasing allocations for claim %s: %v", claimRef, err)
		if cc.workqueue.NumRequeues(claimRef) <= maxRetries {
			cc.workqueue.AddRateLimited(claimRef)
			return true
		}
	}
	cc.workqueue.Forget(claimRef)
	return true
}

func (cc *ClaimController) releaseClaimAllocations(ctx context.Context, claimRef string) error {
	logging.Verbosef("releasing Whereabouts allocations for deleted IPAMClaim %s", claimRef)

	client := wbclient.NewKubernetesClient(cc.wbClient, nil, cc.claimsClient)
	pools, err := client.ListIPPools()
	if err != nil {
		return fmt.Errorf("listing IP pools: %w", err)
	}

	for _, pool := range pools {
		reservations := pool.Allocations()
		changed := false
		updated := make([]types.IPReservation, 0, len(reservations))
		for _, r := range reservations {
			if r.IPAMClaimRef == claimRef {
				logging.Verbosef("removing claim-backed reservation %s for claim %s", r.IP, claimRef)
				changed = true
				continue
			}
			updated = append(updated, r)
		}
		if !changed {
			continue
		}
		if err := pool.Update(ctx, updated); err != nil {
			return fmt.Errorf("updating pool after claim %s delete: %w", claimRef, err)
		}
	}

	overlapping, err := client.ListOverlappingIPs()
	if err != nil {
		return fmt.Errorf("listing overlapping IPs: %w", err)
	}
	for i := range overlapping {
		o := &overlapping[i]
		if o.Spec.IPAMClaimRef == claimRef {
			logging.Verbosef("removing overlapping reservation %s for claim %s", o.Name, claimRef)
			if err := client.DeleteOverlappingIP(o); err != nil {
				return fmt.Errorf("deleting overlapping IP %s: %w", o.Name, err)
			}
		}
	}
	return nil
}

func onClaimDelete(queue workqueue.TypedRateLimitingInterface[string], obj interface{}) {
	claim, ok := obj.(*ipamclaimsv1alpha1.IPAMClaim)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			logging.Errorf("error decoding IPAMClaim delete event: unexpected type %T", obj)
			return
		}
		claim, ok = tombstone.Obj.(*ipamclaimsv1alpha1.IPAMClaim)
		if !ok {
			logging.Errorf("error decoding IPAMClaim tombstone: unexpected type %T", tombstone.Obj)
			return
		}
	}
	claimRef := fmt.Sprintf("%s/%s", claim.Namespace, claim.Name)
	logging.Verbosef("IPAMClaim deleted: %s", claimRef)
	queue.Add(claimRef)
}
