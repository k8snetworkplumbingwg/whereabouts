/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package node_controller

import (
	"context"
	"fmt"
	k8snetplumbersv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"os"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	kubeinformers "k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	k8snetplumbersv1fake "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/fake"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned/fake"
	informers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
)

var (
	alwaysReady        = func() bool { return true }
	noResyncPeriodFunc = func() time.Duration { return 0 }
)

type fixture struct {
	t *testing.T

	whereaboutsclient *fake.Clientset
	kubeclient        *k8sfake.Clientset
	nadClient         *k8snetplumbersv1fake.Clientset
	// Objects to put in the store.
	nadLister           []*k8snetplumbersv1.NetworkAttachmentDefinition
	nodeSlicePoolLister []*v1alpha1.NodeSlicePool
	nodeLister          []*v1.Node

	// Actions expected to happen on the client.
	whereaboutsactions []core.Action

	// Objects from here preloaded into NewSimpleFake.
	kubeobjects        []runtime.Object
	whereaboutsObjects []runtime.Object
	nadObjects         []runtime.Object
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{}
	f.t = t
	f.whereaboutsObjects = []runtime.Object{}
	f.kubeobjects = []runtime.Object{}
	f.nadObjects = []runtime.Object{}
	return f
}

func newNad(name string, networkName string, networkRange string, sliceSize string) *k8snetplumbersv1.NetworkAttachmentDefinition {
	return &k8snetplumbersv1.NetworkAttachmentDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: k8snetplumbersv1.SchemeGroupVersion.String(),
			Kind:       "NetworkAttachmentDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: k8snetplumbersv1.NetworkAttachmentDefinitionSpec{
			Config: fmt.Sprintf(`
				{ 
					"cniVersion": "0.3.1", 
					"name": "test-name", 
					"plugins": 
						[ 
							{ 
								"type": "macvlan",
								"master": "test", 
								"mode": "bridge", 
								"mtu": "mtu", 
								"ipam": 
									{ 
										"configuration_path": "/tmp/whereabouts.conf",
										"type": "whereabouts",
										"range": "%s", 
										"node_slice_size": "%s",
										"network_name": "%s",
										"enable_overlapping_ranges": false
									} 
							}
						] 
				}`, networkRange, sliceSize, networkName),
		},
	}
}

func getOwnerRefs(nads []*k8snetplumbersv1.NetworkAttachmentDefinition) []metav1.OwnerReference {
	if len(nads) == 1 {
		return []metav1.OwnerReference{
			*metav1.NewControllerRef(nads[0], k8snetplumbersv1.SchemeGroupVersion.WithKind("NetworkAttachmentDefinition")),
		}
	} else if len(nads) > 1 {
		refs := []metav1.OwnerReference{
			*metav1.NewControllerRef(nads[0], k8snetplumbersv1.SchemeGroupVersion.WithKind("NetworkAttachmentDefinition")),
		}
		for i, nad := range nads {
			if i == 0 {
				continue
			}
			refs = append(refs, metav1.OwnerReference{
				APIVersion: nad.APIVersion,
				Kind:       nad.Kind,
				Name:       nad.Name,
				UID:        nad.UID,
			})
		}
		return refs
	}
	return []metav1.OwnerReference{}
}

func newNodeSlicePool(name string, rangeSize string, sliceSize string, status v1alpha1.NodeSlicePoolStatus, nad ...*k8snetplumbersv1.NetworkAttachmentDefinition) *v1alpha1.NodeSlicePool {
	return &v1alpha1.NodeSlicePool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "NodeSlicePool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       metav1.NamespaceDefault,
			OwnerReferences: getOwnerRefs(nad),
		},
		Spec: v1alpha1.NodeSlicePoolSpec{
			Range:     rangeSize,
			SliceSize: sliceSize,
		},
		Status: status,
	}
}

func newNode(name string) *v1.Node {
	return &v1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
	}
}

func (f *fixture) newController(ctx context.Context) (*Controller, informers.SharedInformerFactory, kubeinformers.SharedInformerFactory, nadinformers.SharedInformerFactory) {
	f.whereaboutsclient = fake.NewSimpleClientset(f.whereaboutsObjects...)
	f.kubeclient = k8sfake.NewSimpleClientset(f.kubeobjects...)
	f.nadClient = k8snetplumbersv1fake.NewSimpleClientset()
	// We have to manually Create the resources in the tracker for nad because
	// k8s.io/client-go/testing/fixture.go uses meta.UnsafeGuessKindToResource(gvk) to convert gvk to gvr
	// this leads to tracker containing resource of 'networkattachmentdefinition' instead of 'network-attachment-definition'
	// which causes the informer to trigger deletes because there is no 'network-attachment-definition'
	for _, nad := range f.nadObjects {
		//TODO: clean way to set GVR
		f.nadClient.Tracker().Create(schema.GroupVersionResource{
			Group:    "k8s.cni.cncf.io",
			Version:  "v1",
			Resource: "network-attachment-definitions",
		}, nad, "default")
	}

	whereaboutsInformerFactory := informers.NewSharedInformerFactory(f.whereaboutsclient, noResyncPeriodFunc())
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(f.kubeclient, noResyncPeriodFunc())
	nadInformerFactory := nadinformers.NewSharedInformerFactory(f.nadClient, noResyncPeriodFunc())

	c := NewController(
		ctx,
		f.kubeclient,
		f.whereaboutsclient,
		f.nadClient,
		kubeInformerFactory.Core().V1().Nodes(),
		whereaboutsInformerFactory.Whereabouts().V1alpha1().NodeSlicePools(),
		nadInformerFactory.K8sCniCncfIo().V1().NetworkAttachmentDefinitions(),
		true)

	//TODO: add sync for IP Pool or remove IP pool if not used
	c.nadSynced = alwaysReady
	c.nodesSynced = alwaysReady
	c.nodeSlicePoolSynced = alwaysReady
	c.recorder = &record.FakeRecorder{}

	for _, node := range f.nodeLister {
		err := kubeInformerFactory.Core().V1().Nodes().Informer().GetIndexer().Add(node)
		if err != nil {
			f.t.Error("error adding nodes to informer mock")
		}
	}

	for _, nad := range f.nadLister {
		err := nadInformerFactory.K8sCniCncfIo().V1().NetworkAttachmentDefinitions().Informer().GetIndexer().Add(nad)
		if err != nil {
			f.t.Error("error adding nads to informer mock")
		}
	}

	for _, nodeSlicePool := range f.nodeSlicePoolLister {
		err := whereaboutsInformerFactory.Whereabouts().V1alpha1().NodeSlicePools().Informer().GetIndexer().Add(nodeSlicePool)
		if err != nil {
			f.t.Error("error adding nodeslicepools to informer mock")
		}
	}

	return c, whereaboutsInformerFactory, kubeInformerFactory, nadInformerFactory
}

func (f *fixture) run(ctx context.Context, name string) {
	//requires conf file to run
	globalconf := `{
      "datastore": "kubernetes",
      "kubernetes": {
        "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
      },
      "log_file": "/tmp/whereabouts.log",
      "log_level": "debug",
      "gateway": "192.168.5.5"
    }`

	err := os.WriteFile("/tmp/whereabouts.conf", []byte(globalconf), 0755)
	if err != nil {
		f.t.Error("error writing /tmp/whereabouts.conf")
	}
	f.runController(ctx, name, true, false)
}

func (f *fixture) runExpectError(ctx context.Context, name string) {
	f.runController(ctx, name, true, true)
}

func (f *fixture) runController(ctx context.Context, nadName string, startInformers bool, expectError bool) {
	c, whereaboutsInformer, kubeInformer, nadInformer := f.newController(ctx)
	if startInformers {
		whereaboutsInformer.Start(ctx.Done())
		kubeInformer.Start(ctx.Done())
		nadInformer.Start(ctx.Done())
	}

	err := c.syncHandler(ctx, nadName)
	if !expectError && err != nil {
		f.t.Errorf("error syncing nad: %v", err)
	} else if expectError && err == nil {
		f.t.Error("expected error syncing nad, got nil")
	}

	whereaboutsActions := filterInformerActions(f.whereaboutsclient.Actions())
	for i, action := range whereaboutsActions {
		if len(f.whereaboutsactions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(whereaboutsActions)-len(f.whereaboutsactions), whereaboutsActions[i:])
			break
		}

		expectedAction := f.whereaboutsactions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.whereaboutsactions) > len(whereaboutsActions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.whereaboutsactions)-len(whereaboutsActions), f.whereaboutsactions[len(whereaboutsActions):])
	}
}

// checkAction verifies that expected and actual actions are equal and both have
// same attached resources
func checkAction(expected, actual core.Action, t *testing.T) {
	if !(expected.Matches(actual.GetVerb(), actual.GetResource().Resource) && actual.GetSubresource() == expected.GetSubresource()) {
		t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expected, actual)
		return
	}

	if reflect.TypeOf(actual) != reflect.TypeOf(expected) {
		t.Errorf("Action has wrong type. Expected: %t. Got: %t", expected, actual)
		return
	}

	switch a := actual.(type) {
	case core.CreateActionImpl:
		e, _ := expected.(core.CreateActionImpl)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case core.UpdateActionImpl:
		e, _ := expected.(core.UpdateActionImpl)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case core.PatchActionImpl:
		e, _ := expected.(core.PatchActionImpl)
		expPatch := e.GetPatch()
		patch := a.GetPatch()

		if !reflect.DeepEqual(expPatch, patch) {
			t.Errorf("Action %s %s has wrong patch\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expPatch, patch))
		}
	case core.DeleteActionImpl:
		e, _ := expected.(core.DeleteActionImpl)
		expName := e.GetName()
		name := a.GetName()
		expNamespace := e.GetNamespace()
		namespace := a.GetNamespace()

		if expName != name || expNamespace != namespace {
			t.Errorf("Action %s %s has wrong namespace or name. Expected %s/%s, actual %s/%s",
				a.GetVerb(), a.GetResource().Resource, expNamespace, expName, namespace, name)
		}
	default:
		t.Errorf("Uncaptured Action %s %s, you should explicitly add a case to capture it",
			actual.GetVerb(), actual.GetResource().Resource)
	}
}

// filterInformerActions filters list and watch actions for testing resources.
// Since list and watch don't change resource state we can filter it to lower
// nose level in our tests.
func filterInformerActions(actions []core.Action) []core.Action {
	ret := []core.Action{}
	for _, action := range actions {
		if len(action.GetNamespace()) == 0 &&
			(action.Matches("list", "network-attachment-definitions") ||
				action.Matches("watch", "network-attachment-definitions") ||
				action.Matches("list", "nodeslicepools") ||
				action.Matches("watch", "nodeslicepools") ||
				action.Matches("list", "nodes") ||
				action.Matches("watch", "nodes") ||
				action.Matches("list", "ippools") ||
				action.Matches("watch", "ippools")) {
			continue
		}
		ret = append(ret, action)
	}

	return ret
}

func (f *fixture) expectNodeSlicePoolCreateAction(nodeSlicePool *v1alpha1.NodeSlicePool) {
	f.whereaboutsactions = append(f.whereaboutsactions, core.NewCreateAction(schema.GroupVersionResource{Resource: "nodeslicepools"}, nodeSlicePool.Namespace, nodeSlicePool))
}

func (f *fixture) expectNodeSlicePoolUpdateAction(nodeSlicePool *v1alpha1.NodeSlicePool) {
	f.whereaboutsactions = append(f.whereaboutsactions, core.NewUpdateAction(schema.GroupVersionResource{Resource: "nodeslicepools"}, nodeSlicePool.Namespace, nodeSlicePool))
}

func (f *fixture) expectNodeSlicePoolDeleteAction(nodeSlicePool *v1alpha1.NodeSlicePool) {
	f.whereaboutsactions = append(f.whereaboutsactions, core.NewDeleteAction(schema.GroupVersionResource{Resource: "nodeslicepools"}, nodeSlicePool.Namespace, nodeSlicePool.Name))
}

// TestCreatesNodeSlicePoolsNoNodes tests nad creation results in a new nodeslicepool being created correctly when no nodes in cluster
func TestCreatesNodeSlicePoolsNoNodes(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/10")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	f.nadLister = append(f.nadLister, nad)
	f.nadObjects = append(f.nadObjects, nad)
	f.expectNodeSlicePoolCreateAction(nodeSlicePool)

	f.run(context.TODO(), getKey(nad, t))
}

// TestCreatesNodeSlicePoolsWithNodes tests that a new nad with existing nodes will be result in nodeslicepool created correctly
func TestCreatesNodeSlicePoolsWithNodes(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	f.nadLister = append(f.nadLister, nad)
	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nadObjects = append(f.nadObjects, nad)
	f.expectNodeSlicePoolCreateAction(nodeSlicePool)

	f.run(context.TODO(), getKey(nad, t))
}

// TestDoNothing checks for no action taken when no nad exists
func TestDoNothing(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)

	f.run(context.TODO(), getKey(nad, t))
}

// TestNodeJoins test for node addition to nodeslicepool after node is added
func TestNodeJoins(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	expectedNodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	f.nadLister = append(f.nadLister, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)
	f.kubeobjects = append(f.kubeobjects, node1)
	f.nodeLister = append(f.nodeLister, node1)
	f.nadObjects = append(f.nadObjects, nad)
	f.expectNodeSlicePoolUpdateAction(expectedNodeSlicePool)
	f.run(context.TODO(), getKey(nad, t))
}

// TestNodeLeaves tests for node removal from nodeslicepool after the node no longer exists
func TestNodeLeaves(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/10")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	expectedNodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	f.nadLister = append(f.nadLister, nad)
	f.nadObjects = append(f.nadObjects, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)
	f.expectNodeSlicePoolUpdateAction(expectedNodeSlicePool)
	f.run(context.TODO(), getKey(nad, t))
}

// TestNadDelete tests the deletion of NodeSlicePool after its only owning NAD is deleted
func TestNadDelete(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nadObjects = append(f.nadObjects, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)
	f.expectNodeSlicePoolDeleteAction(nodeSlicePool)

	f.run(context.TODO(), getKey(nad, t))
}

// TestUpdateNoImpactfulChange tests for a change to NAD with existing node slice pool where the change does
// not cause a reslicing of the nodeslicepool
func TestUpdateNoImpactfulChange(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test2", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)

	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nadLister = append(f.nadLister, nad)
	f.nadObjects = append(f.nadObjects, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)
}

// TestUpdateRangeChangeAndSliceChange tests update where range and slice changes
func TestUpdateRangeChangeAndSliceChange(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/10", "/12")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)
	expectedNodeSlicePool := newNodeSlicePool("test", "10.0.0.0/10", "/12",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/12",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.16.0.0/12",
				},
				{
					NodeName:   "",
					SliceRange: "10.32.0.0/12",
				},
				{
					NodeName:   "",
					SliceRange: "10.48.0.0/12",
				},
			},
		}, nad)

	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nadLister = append(f.nadLister, nad)
	f.nadObjects = append(f.nadObjects, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)

	f.expectNodeSlicePoolUpdateAction(expectedNodeSlicePool)
}

// TestUpdateRangeChangeChange tests update where range changes
func TestUpdateRangeChangeChange(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "11.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)
	expectedNodeSlicePool := newNodeSlicePool("test", "11.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "11.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "11.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "11.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "11.192.0.0/10",
				},
			},
		}, nad)

	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nadLister = append(f.nadLister, nad)
	f.nadObjects = append(f.nadObjects, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)

	f.expectNodeSlicePoolUpdateAction(expectedNodeSlicePool)
}

// TestUpdateChangeSliceChange tests update where slice changes
func TestUpdateChangeSliceChange(t *testing.T) {
	f := newFixture(t)
	nad := newNad("test", "test", "10.0.0.0/8", "/11")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad)
	expectedNodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/11",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/11",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.32.0.0/11",
				},
				{
					NodeName:   "",
					SliceRange: "10.64.0.0/11",
				},
				{
					NodeName:   "",
					SliceRange: "10.96.0.0/11",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/11",
				},
				{
					NodeName:   "",
					SliceRange: "10.160.0.0/11",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/11",
				},
				{
					NodeName:   "",
					SliceRange: "10.224.0.0/11",
				},
			},
		}, nad)

	f.nodeLister = append(f.nodeLister, node1, node2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nadLister = append(f.nadLister, nad)
	f.nadObjects = append(f.nadObjects, nad)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)

	f.expectNodeSlicePoolUpdateAction(expectedNodeSlicePool)
}

// TestMultipleNadsSameNetworkName tests that if nad and node slice already exist and new nad with same network name is
// created it appends the new owner ref
func TestMultipleNadsSameNetworkName(t *testing.T) {
	f := newFixture(t)
	nad1 := newNad("test1", "test", "10.0.0.0/8", "/10")
	nad2 := newNad("test2", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad1)
	expectedNodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad1, nad2)
	f.nadObjects = append(f.nadObjects, nad1, nad2)
	f.nadLister = append(f.nadLister, nad1, nad2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nodeLister = append(f.nodeLister, node1, node2)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)

	f.expectNodeSlicePoolUpdateAction(expectedNodeSlicePool)

	f.run(context.TODO(), getKey(nad2, t))
}

// TestMultipleNadsSameNetworkNameDeleteOneNad tests nothing is done if multiple nads share ownership of nodeslice pool
// and one is deleted
func TestMultipleNadsSameNetworkNameDeleteOneNad(t *testing.T) {
	f := newFixture(t)
	nad1 := newNad("test1", "test", "10.0.0.0/8", "/10")
	nad2 := newNad("test2", "test", "10.0.0.0/8", "/10")
	node1 := newNode("node1")
	node2 := newNode("node2")
	nodeSlicePool := newNodeSlicePool("test", "10.0.0.0/8", "/10",
		v1alpha1.NodeSlicePoolStatus{
			Allocations: []v1alpha1.NodeSliceAllocation{
				{
					NodeName:   "node1",
					SliceRange: "10.0.0.0/10",
				},
				{
					NodeName:   "node2",
					SliceRange: "10.64.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.128.0.0/10",
				},
				{
					NodeName:   "",
					SliceRange: "10.192.0.0/10",
				},
			},
		}, nad1, nad2)
	f.nadObjects = append(f.nadObjects, nad1)
	f.nadLister = append(f.nadLister, nad1)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nodeSlicePoolLister = append(f.nodeSlicePoolLister, nodeSlicePool)
	f.whereaboutsObjects = append(f.whereaboutsObjects, nodeSlicePool)
	f.nodeLister = append(f.nodeLister, node1, node2)

	f.run(context.TODO(), getKey(nad2, t))
}

// TestTwoNetworksRangeAndSliceMismatch tests that error is thrown if multiple nads share network name with dif configs
func TestTwoNetworksRangeAndSliceMismatch(t *testing.T) {
	f := newFixture(t)
	nad1 := newNad("test1", "test", "10.0.0.0/8", "/10")
	nad2 := newNad("test2", "test", "10.0.0.0/8", "/8")
	node1 := newNode("node1")
	node2 := newNode("node2")
	f.nadObjects = append(f.nadObjects, nad1, nad2)
	f.nadLister = append(f.nadLister, nad1, nad2)
	f.kubeobjects = append(f.kubeobjects, node1, node2)
	f.nodeLister = append(f.nodeLister, node1, node2)

	f.runExpectError(context.TODO(), getKey(nad2, t))
}

func getKey(nad *k8snetplumbersv1.NetworkAttachmentDefinition, t *testing.T) string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(nad)
	if err != nil {
		t.Errorf("Unexpected error getting key for nad %v: %v", nad.Name, err)
		return ""
	}
	return key
}
