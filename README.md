# whereabouts

![whereabouts-logo](doc/logo.png)

An IP Address Management (IPAM) CNI plugin that assigns IP addresses cluster-wide.

If you need a way to assign IP addresses dynamically across your cluster -- Whereabouts is the tool for you. If you've found that you like how the [host-local](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/host-local) works, but, you need something that works across all the nodes in your cluster (`host-local` only knows how to assign IPs to pods on the same node) -- Whereabouts is just what you're looking for. 

The original inspiration for Whereabouts comes from when users have tried to use the samples from [Multus CNI](https://github.com/intel/multus-cni) (a CNI plugin that attaches multiple network interfaces to your pods), which includes samples that use the `host-local` plugin, and they find that it's... Almost the right thing. Sometimes people even assume it'll work across nodes -- and then wind up IP address collisions.

Whereabouts is designed with Kubernetes in mind, but, isn't limited to use in just Kubernetes.

To store IP address allocation, Whereabouts uses [etcd](https://github.com/etcd-io/etcd) as a backend. If you'd like to see another backend -- the patches are welcome!

## Installation

There's two steps to installing Whereabouts

* Installing etcd, for a 
* Installing Whereabouts itself (it's just a binary on disk)

### Installing etcd.

We recommend that you if you're trying it out in a lab, that you use the [etcd-operator](https://github.com/coreos/etcd-operator), the [installation guide](https://github.com/coreos/etcd-operator/blob/master/doc/user/install_guide.md) is just a few steps. 

Once you've got etcd running -- all you'll need to provide Whereabouts is the endpoint(s) for it. In the etcd-operator style installation, you'd find those with:

```
kubectl get svc | grep "etcd-cluster-client"
```

### Installing Whereabouts.

You can install this plugin with a Daemonset, using:

```
git clone https://github.com/dougbtv/whereabouts && cd whereabouts
kubectl apply -f ./doc/daemonset-install.yaml
```

You can compile from this repo (with `./hack/build-go.sh`) and copy the resulting binary onto each node in the `/opt/cni/bin` directory (by default).

## Example Config

Included here is an entire CNI configuration. Whereabouts only cares about the `ipam` section of the CNI config. In particular this uses `macvlan` plugin.

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

If you need a tool to figure out the range of a given CIDR address, try this online tool, [subnet-calculator.com](http://www.subnet-calculator.com/).

The optional parameters are for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`error`,`panic`

## Building

Run the build command from the `./hack` directory:

```
./hack/build-go.sh
```

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!

## Known limitations

* This only works for IPv4 addresses.
* It has write locking, but, it's not optimized. It's write locked for all ranges.
* If you specify overlapping ranges -- you're almost certain to have collisions, so if you specify one config with `192.168.0.0/16` and another with `192.168.0.0/24`, you'll have collisions.
* There's approximately a cap of 18,500 possible addresses in a given range before you'll have to configure etcd to allow more than 1.5 megs in a value.
* There's probably a lot of comparison of IP addresses that could be optimized, lots of string conversion.
* The etcd method that I use is all ASCII. If this was binary, it could probably store more and have more efficient IP address comparison.