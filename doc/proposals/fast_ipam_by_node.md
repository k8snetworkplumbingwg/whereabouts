# Whereabouts support for fast IPAM by using preallocated node slices

# Table of contents

- [Introduction](#introduction)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Design](#design)
  - [Changes in IPAM Config](#changes-in-ipam-config)
  - [Changes in Modules](#changes-in-modules)
  - [Backward compatibility](#backward-compatibility)
- [Alternative Design](#alternative-design)
- [Summary](#summary)
- [Discussions and Decisions](#discussions-and-decisions)

<hr>

## Introduction

Whereabouts currently uses a single lease per cluster named "whereabouts" for locking for all allocation and deallocation
of IPs across the entire cluster running whereabouts. This causes issues with performance and reliability at scale. 
Even at 256 nodes, if you have 10 network-attachment-definitions per pod and run a pod on each node there will be so much lease contention that kubelet times
out before whereabouts can assign all 10 IPs per pod. This would only get worse at higher scale and whereabouts should be able to support
10+ network-attachment-definition per pod at the kubernetes supported scale of 5,000 nodes.

### Goals

- Support existing whereabouts functionality without breaking changes
- Introduce a new mode that can be configured on NAD to use IPAM by node slices
- Support multiple NADs on same network range

### Non-Goals

- Migrate all users to this new mode

<hr>

## Design

The IPAM configuration format would be updated to include enablement of this feature and configurations for the feature.

We will create a new CRD `NodeSlicePool` which will be used to manage the slices of the network ranges that nodes
are assigned to. A controller where reconcile these NodeSlicePools based on cluster nodes and network-attachment-definitions.

Whereabouts binary will be able to tell this feature is enabled and when creating `IPPools` it will check the `NodeSlicePool` to get the range of the current node.
It will set this on existing IPPools object and use a lease per IPPool. There will be an `IPPool` and `Lease` per network per node.
Where a network is defined by network name i.e. you can have multiple `network-attachment-definitions` with a shared network name and this will result in
a shared `NodeSlicePool`, `IPPool` and `Lease` per node for these `network-attachment-definitions`.

i.e. we have nad1 and nad2 both with network name `test-network`. When a node, `trusted-otter` joins the cluster this will result in
`NodeSlicePool`, `IPPool` and `Lease` objects named `test-network-trusted-otter`. If these are seperate network you would just not set the network name
or set the network-name differently per `network-attachment-defintion`.

### Changes in IPAM Config

This will lead to change in IPAM config something as follows:

<table>
<tr>
<th>Old IPAM Config</th>
<th>Changes</th>
<th>New IPAM Config</th>
</tr>
<tr>
<td>
  
```json
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/8"
      }
}
```
  
</td>
<td>

```diff
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/8"
+       "fast_ipam": true,
+       "node_slice_size": "/22"
      }
}
```

</td>
<td>

```json
{
  "cniVersion": "0.3.0",
  "name": "whereaboutsexample",
  "type": "macvlan",
  "master": "eth0",
  "mode": "bridge",
  "ipam": {
    "type": "whereabouts",
    "range": "192.168.2.225/8",
    "fast_ipam": true,
    "node_slice_size": "/22"
  }
}
```

</td>
</tr>
</table>

### Changes in Modules

#### whereabouts/pkg/types/types.go

```diff
 type IPAMConfig struct {
      Name                string
      Type                string               `json:"type"`
      Routes              []*cnitypes.Route    `json:"routes"`
      Datastore           string               `json:"datastore"`
      IPRanges            []RangeConfiguration `json:"ipRanges"`
+     NodeSliceSize       string               `json:"node_slice_size"`
      Addresses           []Address            `json:"addresses,omitempty"`
      OmitRanges          []string             `json:"exclude,omitempty"`
      DNS                 cnitypes.DNS         `json:"dns"`
      Range               string               `json:"range"`
      RangeStart          net.IP               `json:"range_start,omitempty"`
      RangeEnd            net.IP               `json:"range_end,omitempty"`
      GatewayStr          string               `json:"gateway"`
      EtcdHost            string               `json:"etcd_host,omitempty"`
      EtcdUsername        string               `json:"etcd_username,omitempty"`
      EtcdPassword        string               `json:"etcd_password,omitempty"`
      EtcdKeyFile         string               `json:"etcd_key_file,omitempty"`
      EtcdCertFile        string               `json:"etcd_cert_file,omitempty"`
      EtcdCACertFile      string               `json:"etcd_ca_cert_file,omitempty"`
      LeaderLeaseDuration int                  `json:"leader_lease_duration,omitempty"`
      LeaderRenewDeadline int                  `json:"leader_renew_deadline,omitempty"`
      LeaderRetryPeriod   int                  `json:"leader_retry_period,omitempty"`
      LogFile             string               `json:"log_file"`
      LogLevel            string               `json:"log_level"`
      OverlappingRanges   bool                 `json:"enable_overlapping_ranges,omitempty"`
      SleepForRace        int                  `json:"sleep_for_race,omitempty"`
      Gateway             net.IP
      Kubernetes          KubernetesConfig     `json:"kubernetes,omitempty"`
      ConfigurationPath   string               `json:"configuration_path"`
      PodName             string
      PodNamespace        string
 }
```

```diff
type PoolIdentifier struct {
	IpRange     string
	NetworkName string
+	NodeName    string # this will signal that fast node slicing is enabled
}

func IPPoolName(poolIdentifier PoolIdentifier) string {
-	if poolIdentifier.NetworkName == UnnamedNetwork {
-		return normalizeRange(poolIdentifier.IpRange)
+	if poolIdentifier.NodeName != "" {
+		// fast node range naming convention
+		if poolIdentifier.NetworkName == UnnamedNetwork {
+			return fmt.Sprintf("%v-%v", normalizeRange(poolIdentifier.IpRange), poolIdentifier.NodeName)
+		} else {
+			return fmt.Sprintf("%v-%v", poolIdentifier.NetworkName, poolIdentifier.NodeName)
+		}
	} else {
-		return fmt.Sprintf("%s-%s", poolIdentifier.NetworkName, normalizeRange(poolIdentifier.IpRange))
+		// default naming convention
+		if poolIdentifier.NetworkName == UnnamedNetwork {
+			return normalizeRange(poolIdentifier.IpRange)
+		} else {
+			return fmt.Sprintf("%s-%s", poolIdentifier.NetworkName, normalizeRange(poolIdentifier.IpRange))
+		}
	}
}

```

Additional changes will be required within whereabouts to use the NodeSlice to find the `Range` that the node its running
on is assigned to. From here it can use the range on the IPPool with current code. Another change to set the IPPoolName and 
Lease name as described above will be required. Finally, a new controller will be introduced to assign nodes to NodeSlices.

### NodeSlicePool CRD

```diff
// NodeSlicePoolSpec defines the desired state of NodeSlicePool
type NodeSlicePoolSpec struct {
	// Range is a RFC 4632/4291-style string that represents an IP address and prefix length in CIDR notation
	// this refers to the entire range where the node is allocated a subset
	Range string `json:"range"`

	SliceSize string `json:"sliceSize"`
}

// NodeSlicePoolStatus defines the desired state of NodeSlicePool
type NodeSlicePoolStatus struct {
	Allocations []NodeSliceAllocation `json:"allocations"`
}

type NodeSliceAllocation struct {
	NodeName   string `json:"nodeName"`
	SliceRange string `json:"sliceRange"`
}

// ParseCIDR formats the Range of the IPPool
func (i NodeSlicePool) ParseCIDR() (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(i.Spec.Range)
}

// +genclient
// +kubebuilder:object:root=true

// NodeSlicePool is the Schema for the nodesliceippools API
type NodeSlicePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSlicePoolSpec   `json:"spec,omitempty"`
	Status NodeSlicePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeSlicePoolList contains a list of NodeSlicePool
type NodeSlicePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeSlicePool `json:"items"`
}
```

### Backward Compatibility

This feature will only change the behavior of whereabouts if the enabled flag is on. Otherwise 
whereabouts will work the same for any IPAM config without the `node_slice_size` defined.

## Alternative Design

Another design is that the whereabouts daemonset that runs the install-cni script could be used to have a startup and 
shutdown hook which would handle the assignment of nodes to a node slice. This would require locking for the `NodeSlicePools`
on Node join. The reason to use the controller over this design is because the reconciliation pattern reduces the likelyhood for bugs (like leaked IPs) 
and because it will run as a singleton so it does not need to lock as long as it only has 1 worker processing its workqueue. 


### Summary

Currently, we have above two approaches for supporting fast IPAM through node slice assignment.
Both approaches would require the same `IPAMConfig` and the new `NodeSlicePool` CRD. The first approach would also 
require an additional controller to run in the cluster. The first approach is preferred because controller reconciliation is
less likely to have bugs.

### Discussions and Decisions

TBD