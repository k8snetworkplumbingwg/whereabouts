# whereabouts

![whereabouts-logo](doc/logo.png)

An IP Address Management (IPAM) CNI plugin that assigns IP addresses cluster-wide.

If you need a way to assign IP addresses dynamically across your cluster -- Whereabouts is the tool for you. If you've found that you like how the [host-local](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/host-local) CNI plugin works, but, you need something that works across all the nodes in your cluster (`host-local` only knows how to assign IPs to pods on the same node) -- Whereabouts is just what you're looking for.

## Introduction

CNI (Container Network Interface) plugins typically have a configuration element named `ipam`. CNI IPAM plugins can assign IP addresses, and Whereabouts assigns IP addresses within a range -- without having to use a DHCP server. 

Whereabouts takes an address range, like `192.168.2.0/24` in CIDR notation, and will assign IP addresses within that range. In this case, it will assign IP addresses from `192.168.2.1` to `192.168.2.255`. When an IP address is assigned to a pod, Whereabouts tracks that IP address and range in an etcd key/value store for the lifetime of that pod. When the pod is removed, Whereabouts then frees the address and makes it available to assign on subsequent requests. Whereabouts always assigns the lowest value address that's available in the range.

In respect to the old equipment out there that doesn't think that IP addresses that end in `.0` are valid -- Whereabouts will not assign addresses that end in `.0`.

The original inspiration for Whereabouts comes from when users have tried to use the samples from [Multus CNI](https://github.com/intel/multus-cni) (a CNI plugin that attaches multiple network interfaces to your pods), which includes examples that use the `host-local` plugin, and they find that it's... Almost the right thing. Sometimes people even assume it'll work across nodes -- and then wind up with IP address collisions.

Whereabouts is designed with Kubernetes in mind, but, isn't limited to use in just Kubernetes.

To track which IP addresses are in use between nodes, Whereabouts uses [etcd](https://github.com/etcd-io/etcd) as a backend. The eventual goal is to make Whereabouts more flexible to use other storage backends in addition to etcd (such as Kubernetes Custom Resources, or other key/value stores), we welcome any contributions towards this goal.

Please note that Whereabouts is very new. Any issues and PRs are welcome, some of the known limitations are found at the bottom of the README.

## Installation

There's three steps to installing Whereabouts

* Installing etcd, for storage for our allocated IP addresses.
* Installing Whereabouts itself (it's just a binary on disk).
* Creating IPAM CNI configurations.

### Installing etcd.

We recommend that you if you're trying it out in a lab, that you use the [etcd-operator](https://github.com/coreos/etcd-operator), the [installation guide](https://github.com/coreos/etcd-operator/blob/master/doc/user/install_guide.md) is just a few steps. 

Once you've got etcd running -- all you'll need to provide Whereabouts is the endpoint(s) for it. In the etcd-operator style installation, you'd find those with:

```
kubectl get svc | grep "etcd-cluster-client"
```

This will give you the service name and the port to use, in this case you'll specify it in the configuration in a `service-name:port` format, the default port for etcd clients is `2379`.

### Installing Whereabouts.

You can install this plugin with a Daemonset, using:

```
git clone https://github.com/dougbtv/whereabouts && cd whereabouts
kubectl apply -f ./doc/daemonset-install.yaml
```

You can compile from this repo (with `./hack/build-go.sh`) and copy the resulting binary onto each node in the `/opt/cni/bin` directory (by default).

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
        "range": "192.168.2.225/28",
        "etcd_host": "127.0.0.1:2379",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
}
```

Three parameters are required:

* `type`: This should be set to `whereabouts`.
* `range`: This specifies the range in which IP addresses will be allocated.
* `etcd_host`: This is a connection string for your etcd hosts. It can take a single address or a list, or any other valid etcd connection string.

In this case the `range` is set to `192.168.2.225/28`, this will allocate IP addresses in the range

If you need a tool to figure out the range of a given CIDR address, try this online tool, [subnet-calculator.com](http://www.subnet-calculator.com/).

The optional parameters are for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`error`,`panic`

Additionally -- you can set the route, gateway and DNS using anything from the configurations for the [static IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) (as well as additional static IP addresses). 

## Building

Run the build command from the `./hack` directory:

```
./hack/build-go.sh
```

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!

The typeface used in the logo is [AZONIX](https://www.dafont.com/azonix.font), by [MixoFX](https://twitter.com/MixoFX).

## Known limitations

* This only works for IPv4 addresses.
* It has read/write locking to prevent race conditions, but, it's not optimized. It's write locked for all ranges.
* If you specify overlapping ranges -- you're almost certain to have collisions, so if you specify one config with `192.168.0.0/16` and another with `192.168.0.0/24`, you'll have collisions.
    - This could be fixed with an admission controller.
    - And admission controller could also prevent you from starting a pod in a given range if you were out of addresses within that range.
* There's approximately a cap of 18,500 possible addresses in a given range before you'll have to configure etcd to allow more than 1.5 megs in a value.
* There's probably a lot of comparison of IP addresses that could be optimized, lots of string conversion.
* The etcd method that I use is all ASCII. If this was binary, it could probably store more and have more efficient IP address comparison.
* Unlikely to work in Canada, apparently it would have to be "where aboots?" for Canadians to be able to operate it.