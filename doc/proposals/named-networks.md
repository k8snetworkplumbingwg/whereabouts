# Support for named networks

It was decided in March 2023 to implement scheme 3. This means that we added a new field to the `IPAMConfig` called `network_name` that is included in the `IPPool` name to distinguish it from other pools with the same CIDR.

# Table of contents

- [Introduction](#introduction)
  - [Goal](#goal-of-this-proposal)
- [Design](#design)
  - [Analysis of the proposed schemes](#analysis-of-the-proposed-schemes)
  - [Changes in Modules](#changes-in-modules)
- [Summary](#summary)
- [Discussions and Decisions](#discussions-and-decisions)

<hr>

## Introduction

When whereabouts assigns an IP to a Pod this fact is recorded in a CR of kind `IPPool` that has its name derived from the CIDR range in question.
This CR is stored in the namespace of whereabouts (`kube-system` by default).

Should the user configure multiple overlapping ranges, it is possible to configure whereabouts to allow assigning duplicate IPs.

However, since the storage of the assignments is done in a CR that is named like the CIDR range, it is not possible to configure *the same CIDR range* twice and have whereabouts assign from the ranges independently.

This is, for example, useful in multi-tenant situations where more than one group is responsible for selecting CIDR ranges.

### Goal of this Proposal

- Allow configuring the same CIDR range multiple times (e.g. in separate multus-`NetworkAttachmentDefinition`s)

<hr>

## Design

The network configuration already has a field `name`:

```json
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
}
```

This is also parsed into the internal representation of the `IPAMConfig`.

This proposal shows three schemes of implementing using that name to distinguish the assignment of IPs:

1. Store the CR with the name of the network configuration instead of the canonicalized CIDR range
2. Store the CR with the name of the network configuration prepended (or appended) to the canonicalized CIDR range
3. Add a new field into the `IPAMConfig` to allow users to decide when whereabouts should use the name or the CIDR range for identifying the configuration

Further, this proposal changes the namespace of the stored `IPPool` to be the same as the multus `NetworkAttachmentDefinition` if it exists, falling back to storing in the `kube-system` namespace.
Cross-namespace access will be allowed in cases where multus allows cross-namespace access to the `NetworkAttachmentDefinition`

### Analysis of the proposed schemes

#### Store the CR under the name of the network configuration

<table>
<tr>
<th>Pros</th>
<th>Cons</th>
</tr>
<tr>
<td>
:green_circle: Clean design<br/>
:green_circle: Ranges are easy to find during debugging<br/>
:green_circle: No more IP-to-string canonicalization<br/>
</td>
<td>
:red_circle: Not backwards compatible, existing installation would need to carefully migrate the existing `IPPool`s to not get duplicate IPs<br/>
</td>
</tr>
</table>

#### Store the CR under a name combined from the name of the network configuration and the CIDR range

<table>
<tr>
<th>Pros</th>
<th>Cons</th>
</tr>
<tr>
<td>
:green_circle: Clean design<br/>
:green_circle: Ranges are easy to find during debugging<br/>
</td>
<td>
:red_circle: Not backwards compatible, existing installation would need to carefully migrate the existing `IPPool`s to not get duplicate IPs<br/>
</td>
</tr>
</table>

#### Add a new field to decide whether this is a named range or not

<table>
<tr>
<th>Pros</th>
<th>Cons</th>
</tr>
<tr>
<td>
:green_circle: Backwards compatible, existing `IPPool`s are still where we left them<br/>
:yellow_circle: Named ranges are easy to find during debugging, other ranges are unchanged<br/>
</td>
<td>
:red_circle: "API" change<br/>
</td>
</tr>
</table>

### Changes in Modules

#### Schemes 2 and 3

##### whereabouts/pkg/storage/kubernetes/ipam.go

```diff
-func NormalizeRange(ipRange string) string {
+func NormalizeRange(ipRange string, networkName string) string {
    // v6 filter
    normalized := strings.ReplaceAll(ipRange, ":", "-")
    // replace subnet cidr slash
    normalized = strings.ReplaceAll(normalized, "/", "-")
-   return normalized

+   if ThisIsANamedRange {
+       return networkName + "-" + normalized
+   } else {
+       return normalized
+   }
}
```

This will ensure that every time whereabouts looks up the current assignments on a range, it queries not for `192.168.2.225-28` but for `whereaboutsexample-192.168.2.225-28`.
Should the network be configured to use the "old" names (scheme 3), the lookup is for the unchanged name `192.168.2.225-28`.

#### Scheme 1

##### whereabouts/pkg/storage/kubernetes/ipam.go

```diff
-func (i *KubernetesIPAM) GetIPPool(ctx context.Context, ipRange string) (storage.IPPool, error) {
+func (i *KubernetesIPAM) GetIPPool(ctx context.Context, ipRange string, networkName string, namespace string) (storage.IPPool, error) {
-    normalized := NormalizeRange(ipRange)
-
-    pool, err := i.getPool(ctx, normalized, ipRange)
+    // The ipRange is passed in so that the CR can be created if needed
+    pool, err := i.getPool(ctx, networkName, namespace, ipRange)
     if err != nil {
         return nil, err
     }

     firstIP, _, err := pool.ParseCIDR()
     if err != nil {
         return nil, err
     }

     return &KubernetesIPPool{i.client, i.containerID, firstIP, pool}, nil
 }

-func (i *KubernetesIPAM) getPool(ctx context.Context, name string, iprange string) (*whereaboutsv1alpha1.IPPool, error) {
+func (i *KubernetesIPAM) getPool(ctx context.Context, name string, namespace string, iprange string) (*whereaboutsv1alpha1.IPPool, error) {
     pool := &whereaboutsv1alpha1.IPPool{
         ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: i.namespace},
     }
-    if err := i.client.Get(ctx, types.NamespacedName{Name: name, Namespace: i.namespace}, pool); errors.IsNotFound(err) {
+    if err := i.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, pool); errors.IsNotFound(err) {
         // pool does not exist, create it
         pool.ObjectMeta.Name = name
         pool.Spec.Range = iprange
         pool.Spec.Allocations = make(map[string]whereaboutsv1alpha1.IPAllocation)
         if err := i.client.Create(ctx, pool); errors.IsAlreadyExists(err) {
             // the pool was just created -- allow retry
             return nil, &temporaryError{err}
         } else if err != nil {
             return nil, fmt.Errorf("k8s create error: %s", err)
         }
         // if the pool was created for the first time, trigger another retry of the allocation loop
         // so all of the metadata / resourceVersions are populated as necessary by the `client.Get` call
         return nil, &temporaryError{fmt.Errorf("k8s pool initialized")}
     } else if err != nil {
         return nil, fmt.Errorf("k8s get error: %s", err)
     }
     return pool, nil
 }
```

### Summary

whereabouts supports disabling the check for overlapping IP assignments, however it does not alllow actually configuring two identical ranges.

This proposal (and the prototypical implementation in https://github.com/k8snetworkplumbingwg/whereabouts/pull/256) allows doing exactly that by introducing a new IPAM parameter `network_name`.

### Discussions and Decisions

See
- https://github.com/k8snetworkplumbingwg/whereabouts/pull/256
- https://github.com/k8snetworkplumbingwg/whereabouts/issues/50#issuecomment-874040513

#### Migration toolkit

Should the decision be made that scheme 1 or scheme 2 are implemented, we are facing a backwards-compatibility issue: whereabouts would not find existing `IPPool`s as they are stored under the CIDR-derived name.
We propose to supply a small program (ideally also as OCI image for use in kubernetes `Job`s) that migrates an existing cluster from "legacy whereabouts" to the new, named and namespaced scheme.

This tool would do roughly the following:

0. Grab the leader election lock to prevent any whereabouts to run during the operation
1. List all `IPPool`s in the `kube-system` namespace
2. List all `NetworkAttachmentDefinition`s in all namespaces
3. Match the `NetworkAttachmentDefinition`s to the `IPPool`s using the CIDR canonicalization rules
4. Delete the `IPPool`s and re-create it under the namespace of the `NetworkAttachmentDefinition` with the name derived from the CNI config
5. List any unmatched `IPPool`s and ask the user to rename/migrate them manually

#### Guide for decision

It is the authors (@toelke) opinion that scheme 3 is the easiest to implement and deploy.
It needs no re-configuration from current users of whereabouts.
It is, however, a kludge designed to allow exactly this backwards compatibility.
For that reason it will probably cause problems in the future.

Implementing schemes 1 or 2 where either the name of the `NetworkAttachmentDefinition` or both the name and CIDR-Range are used for identifying `IPPool` is the greater development effort but gives a clearer desgned outcome.
It could be possible to feature gate this for existing users of whereabouts.

Pending the communities decision we would like to go forward with implementing scheme 1.
