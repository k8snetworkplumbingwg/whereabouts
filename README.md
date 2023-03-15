# whereabouts
![Travis CI status](https://travis-ci.org/k8snetworkplumbingwg/whereabouts.svg?branch=master) ![Go report card](https://goreportcard.com/badge/github.com/k8snetworkplumbingwg/whereabouts)

![whereabouts-logo](doc/logo.png)

An IP Address Management (IPAM) CNI plugin that assigns IP addresses cluster-wide.

If you need a way to assign IP addresses dynamically across your cluster -- Whereabouts is the tool for you. If you've found that you like how the [host-local](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/host-local) CNI plugin works, but, you need something that works across all the nodes in your cluster (`host-local` only knows how to assign IPs to pods on the same node) -- Whereabouts is just what you're looking for.

Whereabouts can be used for both IPv4 & IPv6 addressing.

## Introduction

CNI (Container Network Interface) plugins typically have a configuration element named `ipam`. CNI IPAM plugins can assign IP addresses, and Whereabouts assigns IP addresses within a range -- without having to use a DHCP server. 

Whereabouts takes an address range, like `192.168.2.0/24` in CIDR notation, and will assign IP addresses within that range. In this case, it will assign IP addresses from `192.168.2.1` to `192.168.2.255`. When an IP address is assigned to a pod, Whereabouts tracks that IP address in a data store for the lifetime of that pod. When the pod is removed, Whereabouts then frees the address and makes it available to assign on subsequent requests. Whereabouts always assigns the lowest value address that's available in the range.

You can also specify ranges to exclude from assignment, so if for example you'd like to assign IP addresses within the range `192.168.2.0/24`, you can exclude IP addresses within it by adding them to an exclude list. For example, if you decide to exclude the range `192.168.2.0/28`, the first IP address assigned in the range will be `192.168.2.16`.

In respect to the old equipment out there that doesn't think that IP addresses that end in `.0` are valid -- Whereabouts will not assign addresses that end in `.0`.

The original inspiration for Whereabouts comes from when users have tried to use the samples from [Multus CNI](https://github.com/intel/multus-cni) (a CNI plugin that attaches multiple network interfaces to your pods), which includes examples that use the `host-local` plugin, and they find that it's... Almost the right thing. Sometimes people even assume it'll work across nodes -- and then wind up with IP address collisions.

Whereabouts is designed with Kubernetes in mind, but, isn't limited to use in just Kubernetes.

To track which IP addresses are in use between nodes, Whereabouts uses [etcd](https://github.com/etcd-io/etcd) or a Kubernetes [Custom Resource](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/#custom-resources) as a backend. The goal is to make Whereabouts more flexible and to use additional storage backends, we welcome any contributions towards this goal.

Issues and PRs are welcome! Some of the known limitations are found at the bottom of the README.

## Installation

There's two steps to installing Whereabouts:

* Installing Whereabouts itself (it's just a binary on disk).
* Creating IPAM CNI configurations.

Further installation options (including etcd usage) and configuration parameters can be found in the [extended configuration document](doc/extended-configuration.md).

### Installing Whereabouts.

You can install this plugin with a Daemonset, using:

```
git clone https://github.com/k8snetworkplumbingwg/whereabouts && cd whereabouts
kubectl apply \
    -f doc/crds/daemonset-install.yaml \
    -f doc/crds/whereabouts.cni.cncf.io_ippools.yaml \
    -f doc/crds/whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml
```

The daemonset installation requires Kubernetes Version 1.16 or later.

### Installing with helm 3
You can also install multus and whereabouts with helm 3 (helm 2 is not supported)

```
git clone https://github.com/k8snetworkplumbingwg/helm-charts.git
cd helm-charts
helm upgrade --install multus ./multus  --namespace kube-system
helm upgrade --install whereabouts ./whereabouts --namespace kube-system

```

Helm will install the crd as well as the daemonset

## Example IPAM Config

Included here is an entire CNI configuration. Whereabouts only cares about the `ipam` section of the CNI config. In particular this example uses the `macvlan` CNI plugin. (If you decide to copy this block and try it too, make sure that the `master` setting is set to a network interface name that exists on your nodes). Typically, you'll already have a CNI configuration for an existing CNI plugin in your cluster, and you'll just copy the `ipam` section and modify the values there.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ]
      }
}
```

### An example configuration using a `NetworkAttachmentDefinition`

Whereabouts is particularly useful in scenarios where you're using additional network interfaces for Kubernetes. A `NetworkAttachmentDefinition` custom resource can be used with a CNI meta plugin such as [Multus CNI](https://github.com/intel/multus-cni) to attach multiple interfaces to your pods in Kubernetes.

In short, a `NetworkAttachmentDefinition` contains a CNI configuration packaged into a custom resource. Here's an example of a `NetworkAttachmentDefinition` containing a CNI configuration which uses Whereabouts for IPAM:

```
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
    }'
```

### Example IPv6 Config

The same applies for the usage of IPv6:

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "2001::0/116",
        "gateway": "2001::f:1"
      }
}
```

### Example IPAM config for assigning multiple IP addresses

`ipRanges` field can be used to provide a list of range configurations for assigning multiple IP addresses.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "ipRanges": [{
            "range": "192.168.10.1/24"
          }, {
            "range": "176.168.10.1/16"
        }]
      }
}
```

The above can also be used in combination with basic `range` field as below:

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "ipRanges": [{
            "range": "192.168.10.1/24"
          }, {
            "range": "176.168.10.1/16"
        }],
        "range": "abcd::1/64"
      }
}
```

### Example DualStack config

Similar to above, `ipRanges` can be used for configuring DualStack

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "ipRanges": [{
            "range": "192.168.10.1/24"
          }, {
            "range": "abcd::1/64"
        }]
      }
}
```

## Core Parameters

**Required**

These parameters are required:

* `type`: This should be set to `whereabouts`.
* `range`: This specifies the range in which IP addresses will be allocated.

If for example the `range` is set to `192.168.2.225/28`, this will allocate IP addresses in the range excluding the first network address and the last broadcast address.

If you need a tool to figure out the range of a given CIDR address, try this online tool, [subnet-calculator.com](http://www.subnet-calculator.com/) or an [IPv6 subnet calculator](https://www.vultr.com/resources/subnet-calculator-ipv6/).

**Range end syntax**

Additionally, the `range` parameter can support a CIDR notation that includes the last IP to use. Example: `range: "192.168.2.225-192.168.2.230/28"`.

**Optional**

The following parameters are optional:

* `range_start` : First IP to use when allocating from the `range`. Optional, if unset is inferred from the `range`.
* `range_end` : Last IP to use when allocating from the `range`. Optional, if unset the last ip within the range is determined.
* `exclude`: This is a list of CIDRs to be excluded from being allocated. 

In the example, we exclude IP addresses in the range `192.168.2.229/30` from being allocated (in this case it's 3 addresses, `.229, .230, .231`), as well as `192.168.2.236/32` (just a single address).

*Note 1*: It's up to you to properly set exclusion ranges that are within your subnet, there's no double checking for you (other than that the CIDR notation parses).
*Note 2*: In case of wide IPv6 CIDRs (`range`≤/64) only the first /65 range is addressable (e.g. from `x:x:x:x::0` to `x:x:x:x:7fff:ffff:ffff:ffff`).

Additionally -- you can set the route, gateway and DNS using anything from the configurations for the [static IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) (as well as additional static IP addresses).

### Overlapping Ranges

The overlapping ranges feature is enabled by default, and will not allow an IP address to be re-assigned across two different ranges which overlap. However, this can be disabled.

* `enable_overlapping_ranges`: *(boolean)* Checks to see if an IP has been allocated across another range before assigning it (defaults to `true`).

Please note: This feature is only implemented for the Kubernetes storage backend.

## Building

Run the build command from the `./hack` directory:

```
./hack/build-go.sh
```

## Running whereabouts CNI in a local kind cluster

You can start a kind cluster to run local changes with:
```
make kind
# or make kind COMPUTE_NODES=<desired number of worker nodes>
```

You can then create a NetworkAttachmentDefinition with:
```
cat <<'EOF' | kubectl apply -f -
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
    }'
EOF
```

Create a deployment that uses the NetworkAttachmentDefinition, for example:
```
cat <<'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: netshoot-deployment
  labels:
    app: netshoot-deployment
spec:
  replicas: 1
  selector:
    matchLabels:
      app: netshoot-pod
  template:
    metadata:
      annotations:
        k8s.v1.cni.cncf.io/networks: whereabouts-conf
      labels:
        app: netshoot-pod
    spec:
      containers:
      - name: netshoot
        image: nicolaka/netshoot
        command:
          - sleep
          - "3600"
        imagePullPolicy: IfNotPresent
EOF
```

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!

The typeface used in the logo is [AZONIX](https://www.dafont.com/azonix.font), by [MixoFX](https://twitter.com/MixoFX).

## Known limitations

* A hard system crash on a node might leave behind stranded IP allocations, so if you have a trashing system, this might exhaust IPs.
  - Potentially we need an operator to ensure data is clean, even if just at some kind of interval (e.g. with a cron job)
* There's probably a lot of comparison of IP addresses that could be optimized, lots of string conversion.
* The etcd method has a number of limitations, in that it uses an all ASCII methodology. If this was binary, it could probably store more and have more efficient IP address comparison.
* Unlikely to work in Canada, apparently it would have to be "where aboots?" for Canadians to be able to operate it.
* In case of wide IPv6 CIDRs (`range`≤/64) only the first /65 range is addressable by Whereabouts due to uint64 offset calculation.
