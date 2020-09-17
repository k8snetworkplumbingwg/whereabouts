# whereabouts
![Travis CI status](https://travis-ci.org/dougbtv/whereabouts.svg?branch=master) ![Go report card](https://goreportcard.com/badge/github.com/dougbtv/whereabouts)

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

Please note that Whereabouts is very new. Any issues and PRs are welcome, some of the known limitations are found at the bottom of the README.

## Installation

There's two steps to installing Whereabouts:

* Installing Whereabouts itself (it's just a binary on disk).
* Creating IPAM CNI configurations.

Optionally, you can use etcd directly as a backend for data storage. In which case, you'll want to install etcd, a suggested method is provided later in the README.

### Installing Whereabouts.

You can install this plugin with a Daemonset, using:

#### Pre-Kubernetes v1.16+
```
git clone https://github.com/dougbtv/whereabouts && cd whereabouts
kubectl apply -f ./doc/daemonset-install.yaml -f ./doc/v1beta1/whereabouts.cni.cncf.io_ippools.yaml
```

#### On Kubernetes v1.16+
```
git clone https://github.com/dougbtv/whereabouts && cd whereabouts
kubectl apply -f ./doc/daemonset-install.yaml -f ./doc/whereabouts.cni.cncf.io_ippools.yaml
```

*NOTE*: This daemonset is for use with Kubernetes version 1.16 and later. It may also be useful with previous versions, however you'll need to change the `apiVersion` of the daemonset in the provided yaml, [see the deprecation notice](https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16/).

You can compile from this repo (with `./hack/build-go.sh`) and copy the resulting binary onto each node in the `/opt/cni/bin` directory (by default).

Not that we're also including a Custom Resource Definition (CRD) to use the `kubernetes` datastore option. This installs the kubernetes CRD specification for the `ippools.whereabouts.cni.k8s.io/v1alpha1` type.

## Example Config

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
        "datastore": "kubernetes",
        "kubernetes": { "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig" },
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ],
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
}
```

### Example etcd datastore configuration

If you'll use the etcd datastore option, you'll likely want to install etcd first. Follow the instructions to do so in a later section of the README.

*NOTE*: You'll almost certainly want to change `etcd_host`.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "etcd_host": "example-etcd-cluster-client.cluster.local:2379",
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ],
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
}
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
        "log_file" : "/tmp/whereabouts.log",
                "log_level" : "debug",
        "etcd_host": "example-etcd-cluster-client.cluster.local:2379",
        "range": "2001::0/116",
        "gateway": "2001::f:1"
      }
}
```

## Core Parameters

**Required**

Three parameters are required:

* `type`: This should be set to `whereabouts`.
* `range`: This specifies the range in which IP addresses will be allocated.

In this case the `range` is set to `192.168.2.225/28`, this will allocate IP addresses in the range excluding the first network address and the last broadcast address

If you need a tool to figure out the range of a given CIDR address, try this online tool, [subnet-calculator.com](http://www.subnet-calculator.com/).

**Range end syntax**

Additionally, the `range` parameter can support a CIDR notation that includes the last IP to use. Example: `range: "192.168.2.225-192.168.2.230/28"`.

**Optional**

The following parameters are optional:

* `range_start` : First IP to use when allocating from the `range`. Optional, if unset is inferred from the `range`.
* `range_end` : Last IP to use when allocating from the `range`. Optional, if unset the last ip within the range is determined.
* `exclude`: This is a list of CIDRs to be excluded from being allocated. 

In the example, we exclude IP addresses in the range `192.168.2.229/30` from being allocated (in this case it's 3 addresses, `.229, .230, .231`), as well as `192.168.2.236/32` (just a single address).

*Note*: It's up to you to properly set exclusion ranges that are within your subnet, there's no double checking for you (other than that the CIDR notation parses).

Additionally -- you can set the route, gateway and DNS using anything from the configurations for the [static IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) (as well as additional static IP addresses).

### etcd Parameters

**Required:**
* `etcd_host`: This is a connection string for your etcd hosts. It can take a single address or a list, or any other valid etcd connection string.

**Optional:**
* `etcd_username`: Basic Auth username to use when accessing the etcd API.
* `etcd_password`: Basic Auth password to use when accessing the etcd API.
* `etcd_key_file`: Path to the file containing the etcd private key matching the CNI plugin’s client certificate.
* `etcd_cert_file`: Path to the file containing the etcd client certificate issued to the CNI plugin.
* `etcd_ca_cert_file`: Path to the file containing the root certificate of the certificate authority (CA) that issued the etcd server certificate.

### Logging Parameters

There are two optional parameters for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`error`,`panic`

## Flatfile configuration

There is one option for flat file configuration:

* `configuration_path`: A file path to a Whereabouts configuration file.

If you're using [Multus CNI](http://multus-cni.io/) or another meta-plugin, you may wish to reduce the number of parameters you need to specify in the IPAM section by putting commonly used options into a flat file -- primarily to make it simpler to type and to reduce having to copy and paste the same parameters repeatedly.

Whereabouts will look for the configuration in these locations, in this order:

* The location specified by the `configuration_path` option.
* `/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf`
* `/etc/cni/net.d/whereabouts.d/whereabouts.conf`

You may specify the `configuration_path` to point to another location should it be desired.

Any options added to the `whereabouts.conf` are overridden by configuration options that are in the primary CNI configuration (e.g. in a custom resource `NetworkAttachmentDefinition` used by Multus CNI or in the first file ASCII-betically in the CNI configuration directory -- which is `/etc/cni/net.d/` by default).


### Example flat file configuration

You can reduce the number of parameters used if you need to make more than one Whereabouts configuration (such as if you're using [Multus CNI](http://multus-cni.io/))

Create a file named `/etc/cni/net.d/whereabouts.d/whereabouts.conf`, with the contents:

```
{
  "datastore": "kubernetes",
  "kubernetes": {
    "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
  },
  "log_file": "/tmp/whereabouts.log",
  "log_level": "debug"
}
```

With that in place, you can now create an IPAM configuration that has a lot less options, in this case we'll give an example using a `NetworkAttachmentDefinition` as used with Multus CNI (or other implementations of the [Network Plumbing Working Group specification](https://github.com/k8snetworkplumbingwg/multi-net-spec))

An example configuration using a `NetworkAttachmentDefinition`:

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

You'll note that in the `ipam` section there's a lot less parameters than are used in the previous examples.

## Installing etcd. (optional)

etcd installation is optional. By default, we recommend the custom resource backend (given in the first example configuration).

We recommend that you if you're trying it out in a lab, that you use the [etcd-operator](https://github.com/coreos/etcd-operator), the [installation guide](https://github.com/coreos/etcd-operator/blob/master/doc/user/install_guide.md) is just a few steps. 

Once you've got etcd running -- all you'll need to provide Whereabouts is the endpoint(s) for it. In the etcd-operator style installation, you'd find those with:

```
kubectl get svc | grep "etcd-cluster-client"
```

This will give you the service name and the port to use, in this case you'll specify it in the configuration in a `service-name:port` format, the default port for etcd clients is `2379`.

*Note*: It's important to remember that CNI plugins (typically) run directly on the host and not inside pods. This means that if you use the DNS name (which might look something like `example-etcd-cluster-client.default.svc.cluster.local`) for the service (recommended) make sure that you can resolve those hostnames directly from your hosts. You may find some tips regarding that [here](https://blog.heptio.com/configuring-your-linux-host-to-resolve-a-local-kubernetes-clusters-service-urls-a8c7bdb212a7).

## Building

Run the build command from the `./hack` directory:

```
./hack/build-go.sh
```

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!

The typeface used in the logo is [AZONIX](https://www.dafont.com/azonix.font), by [MixoFX](https://twitter.com/MixoFX).

## Known limitations

* If you specify overlapping ranges -- you're almost certain to have collisions, so if you specify one config with `192.168.0.0/16` and another with `192.168.0.0/24`, you'll have collisions.
    - This could be fixed with an admission controller.
    - And admission controller could also prevent you from starting a pod in a given range if you were out of addresses within that range.
* There's probably a lot of comparison of IP addresses that could be optimized, lots of string conversion.
* The etcd method has a number of limitations, in that it uses an all ASCII methodology. If this was binary, it could probably store more and have more efficient IP address comparison.
* Unlikely to work in Canada, apparently it would have to be "where aboots?" for Canadians to be able to operate it.
